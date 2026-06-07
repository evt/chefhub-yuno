// Package reconcile contains the matching engine that compares internal
// orders against payment-processor settlement reports and produces a
// domain.Report describing every discrepancy.
package reconcile

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/evt/chefhub/internal/domain"
)

// Options tunes the matching engine. Zero values yield sensible production
// defaults — see Defaults().
type Options struct {
	// AmountTolerance is the maximum unsigned amount difference (in minor
	// units) that is treated as a rounding artefact rather than a true
	// mismatch. The brief calls out $0.05 (5 cents).
	AmountTolerance domain.Money

	// FuzzyTimeWindow is the maximum timestamp delta used when attempting to
	// match leftover records that had no shared external reference.
	FuzzyTimeWindow time.Duration

	// EnableFuzzyMatching toggles the second-pass fuzzy matcher (stretch
	// goal). When false the engine only performs exact-reference matching.
	EnableFuzzyMatching bool

	// HighImpactThreshold is the per-record financial impact in minor units
	// at and above which a discrepancy is ranked SeverityHigh.
	HighImpactThreshold domain.Money

	// MediumImpactThreshold is the per-record threshold for SeverityMedium.
	MediumImpactThreshold domain.Money
}

// Defaults returns the production-default matching options.
func Defaults() Options {
	return Options{
		AmountTolerance:       5, // $0.05
		FuzzyTimeWindow:       5 * time.Second,
		EnableFuzzyMatching:   true,
		HighImpactThreshold:   10000, // $100.00
		MediumImpactThreshold: 1000,  // $10.00
	}
}

// Engine performs settlement reconciliation across one internal orders
// dataset and one or more processor datasets.
type Engine struct {
	opts Options
	log  *slog.Logger
}

// NewEngine returns a reconciliation engine configured with opts.
func NewEngine(opts Options) *Engine {
	return &Engine{
		opts: opts,
		log:  slog.Default(),
	}
}

// Reconcile runs the full matching pipeline and returns a populated Report.
// The slices are not retained beyond the call.
func (e *Engine) Reconcile(
	ctx context.Context,
	internal []domain.Transaction,
	processors map[domain.Source][]domain.Transaction,
) domain.Report {
	report := domain.Report{
		GeneratedAt: time.Now().UTC(),
		SourceCounts: map[domain.Source]int{ //nolint:exhaustive // processor entries are filled by the loop below
			domain.SourceInternal: len(internal),
		},
		DiscrepancyCounts: map[domain.DiscrepancyType]int{},
		FinancialExposure: map[string]domain.Money{},
	}
	for src, txns := range processors {
		report.SourceCounts[src] = len(txns)
	}

	// Build per-processor indexes keyed by ExternalRef along with a "claimed"
	// bitmap so we can detect phantom charges in the second pass.
	procIndexes := make(map[domain.Source]map[string]*domain.Transaction, len(processors))
	claimed := make(map[domain.Source]map[string]bool, len(processors))
	for src, txns := range processors {
		idx := make(map[string]*domain.Transaction, len(txns))
		used := make(map[string]bool, len(txns))
		for i := range txns {
			t := &txns[i]
			if t.ExternalRef != "" {
				idx[t.ExternalRef] = t
			}
			used[refKey(t)] = false
		}
		procIndexes[src] = idx
		claimed[src] = used
	}

	// Pass 1: walk the internal source of truth and confirm each order
	// matches at least one processor record. Orders that should have
	// settled but didn't are *deferred* — fuzzy matching gets a chance to
	// rescue them in pass 2 before they become MissingInSettlement findings.
	var deferredMissing []*domain.Transaction
	for i := range internal {
		o := &internal[i]
		if missing := e.checkInternalAgainstProcessors(o, procIndexes, claimed, &report); missing != nil {
			deferredMissing = append(deferredMissing, missing)
		}
	}

	// Pass 2 (fuzzy): try to pair each deferred-missing order with a
	// leftover processor record by (currency, amount, timestamp ±window).
	// Matches are added as clean matches; the remainders fall through to
	// the missing/phantom emitters below.
	if e.opts.EnableFuzzyMatching {
		deferredMissing = e.runFuzzyPass(deferredMissing, processors, claimed, &report)
	}

	for _, o := range deferredMissing {
		report.Discrepancies = append(report.Discrepancies, missingSettlementDiscrepancy(e.opts, o))
	}
	for _, p := range collectUnclaimed(processors, claimed) {
		report.Discrepancies = append(report.Discrepancies, phantomDiscrepancy(e.opts, p))
	}

	finalizeReport(&report)
	e.log.InfoContext(ctx, "reconciliation complete",
		"matched", report.MatchedCount,
		"discrepancies", len(report.Discrepancies),
		"sources", len(report.SourceCounts),
	)
	return report
}

// checkInternalAgainstProcessors compares one internal order against every
// processor's index by ExternalRef and emits the appropriate per-pair
// discrepancies. When the order should have settled but no processor record
// exists yet, the order is returned for the fuzzy pass to consider; nil
// means no further action is needed.
func (e *Engine) checkInternalAgainstProcessors(
	o *domain.Transaction,
	procIndexes map[domain.Source]map[string]*domain.Transaction,
	claimed map[domain.Source]map[string]bool,
	report *domain.Report,
) *domain.Transaction {
	matchedAny := false
	cleanAcrossAll := true

	for src, idx := range procIndexes {
		p, ok := idx[o.ExternalRef]
		if !ok {
			continue
		}
		matchedAny = true
		claimed[src][refKey(p)] = true

		if d, found := compareMatchedPair(e.opts, o, p); found {
			report.Discrepancies = append(report.Discrepancies, d)
			cleanAcrossAll = false
		}
	}

	switch {
	case !matchedAny && expectsSettlement(o.Status):
		return o
	case matchedAny && cleanAcrossAll:
		report.MatchedCount++
	}
	return nil
}

// compareMatchedPair returns a discrepancy (and true) if the matched internal
// and processor records disagree on amount or status. It returns the zero
// value and false when the pair is clean.
func compareMatchedPair(opts Options, internal, processor *domain.Transaction) (domain.Discrepancy, bool) {
	if d, ok := statusDiscrepancy(opts, internal, processor); ok {
		return d, true
	}

	diff := (internal.Amount - processor.Amount).Abs()
	if diff > opts.AmountTolerance {
		return domain.Discrepancy{
			Type:            domain.DiscrepancyAmountMismatch,
			Severity:        rankSeverity(opts, diff),
			ExternalRef:     internal.ExternalRef,
			CustomerID:      internal.CustomerID,
			ChefID:          internal.ChefID,
			Currency:        internal.Currency,
			InternalAmount:  internal.Amount,
			ProcessorAmount: processor.Amount,
			InternalStatus:  internal.Status,
			ProcessorStatus: processor.Status,
			FinancialImpact: diff,
			Sources:         []domain.Source{domain.SourceInternal, processor.Source},
			OccurredAt:      processor.OccurredAt,
			Detail: "amount differs by " + diff.Format(internal.Currency) +
				" (internal " + internal.Amount.Format(internal.Currency) +
				" vs " + string(processor.Source) + " " +
				processor.Amount.Format(processor.Currency) + ")",
		}, true
	}

	return domain.Discrepancy{}, false
}

// statusDiscrepancy returns a status-related discrepancy if the lifecycle
// states disagree. The processor-says-refunded / internal-says-completed
// case is the explicit "refund not synced back" finding from the brief and
// is reported as its own type for finance triage.
func statusDiscrepancy(opts Options, internal, processor *domain.Transaction) (domain.Discrepancy, bool) {
	if internal.Status == processor.Status {
		return domain.Discrepancy{}, false
	}

	dType := domain.DiscrepancyStatusMismatch
	detail := "status disagreement: internal=" + string(internal.Status) +
		" vs " + string(processor.Source) + "=" + string(processor.Status)

	if internal.Status == domain.StatusCompleted && processor.Status == domain.StatusRefunded {
		dType = domain.DiscrepancyRefundNotApplied
		detail = "processor refunded customer; internal record still shows completed"
	}

	// Financial exposure is the full processor amount: until status is
	// reconciled the merchant could pay out the wrong number to the chef.
	impact := processor.Amount.Abs()

	return domain.Discrepancy{
		Type:            dType,
		Severity:        rankSeverity(opts, impact),
		ExternalRef:     internal.ExternalRef,
		CustomerID:      internal.CustomerID,
		ChefID:          internal.ChefID,
		Currency:        internal.Currency,
		InternalAmount:  internal.Amount,
		ProcessorAmount: processor.Amount,
		InternalStatus:  internal.Status,
		ProcessorStatus: processor.Status,
		FinancialImpact: impact,
		Sources:         []domain.Source{domain.SourceInternal, processor.Source},
		OccurredAt:      processor.OccurredAt,
		Detail:          detail,
	}, true
}

// missingSettlementDiscrepancy produces the "internal order, never settled"
// finding. Financial impact is the full order amount because the merchant is
// at risk of not collecting it at all.
func missingSettlementDiscrepancy(opts Options, o *domain.Transaction) domain.Discrepancy {
	impact := o.Amount.Abs()
	return domain.Discrepancy{
		Type:            domain.DiscrepancyMissingInSettlement,
		Severity:        rankSeverity(opts, impact),
		ExternalRef:     o.ExternalRef,
		CustomerID:      o.CustomerID,
		ChefID:          o.ChefID,
		Currency:        o.Currency,
		InternalAmount:  o.Amount,
		InternalStatus:  o.Status,
		FinancialImpact: impact,
		Sources:         []domain.Source{domain.SourceInternal},
		OccurredAt:      o.OccurredAt,
		Detail:          "internal order has no matching processor settlement",
	}
}

// phantomDiscrepancy produces a "processor charge with no internal order"
// finding. Highest urgency — could be duplicate charges or fraud.
func phantomDiscrepancy(opts Options, p *domain.Transaction) domain.Discrepancy {
	impact := p.Amount.Abs()
	return domain.Discrepancy{
		Type:            domain.DiscrepancyMissingInDatabase,
		Severity:        rankSeverity(opts, impact),
		ExternalRef:     p.ExternalRef,
		Currency:        p.Currency,
		ProcessorAmount: p.Amount,
		ProcessorStatus: p.Status,
		FinancialImpact: impact,
		Sources:         []domain.Source{p.Source},
		OccurredAt:      p.OccurredAt,
		Detail: "processor " + string(p.Source) + " settled " +
			p.Amount.Format(p.Currency) +
			" with no matching internal order (reference " + p.ExternalRef + ")",
	}
}

// rankSeverity converts a per-record financial impact into a triage band.
func rankSeverity(opts Options, impact domain.Money) domain.Severity {
	switch {
	case impact >= opts.HighImpactThreshold:
		return domain.SeverityHigh
	case impact >= opts.MediumImpactThreshold:
		return domain.SeverityMedium
	default:
		return domain.SeverityLow
	}
}

// expectsSettlement reports whether an internal order should have produced
// a corresponding processor settlement record. Failed authorizations
// understandably never settle, and pending orders may settle later.
func expectsSettlement(s domain.Status) bool {
	switch s {
	case domain.StatusCompleted, domain.StatusRefunded:
		return true
	case domain.StatusPending, domain.StatusFailed:
		return false
	default:
		return false
	}
}

// finalizeReport sorts discrepancies by impact and rolls up summary counters.
func finalizeReport(r *domain.Report) {
	sort.SliceStable(r.Discrepancies, func(i, j int) bool {
		if r.Discrepancies[i].FinancialImpact != r.Discrepancies[j].FinancialImpact {
			return r.Discrepancies[i].FinancialImpact > r.Discrepancies[j].FinancialImpact
		}
		return r.Discrepancies[i].ExternalRef < r.Discrepancies[j].ExternalRef
	})

	for _, d := range r.Discrepancies {
		r.DiscrepancyCounts[d.Type]++
		if d.Currency != "" {
			r.FinancialExposure[d.Currency] += d.FinancialImpact
		}
	}
}

// refKey is the per-processor key used to dedupe claimed records. It allows
// processor records that share a ref (e.g. retries) to be claimed once each.
func refKey(t *domain.Transaction) string {
	return t.SourceID + "|" + t.ExternalRef
}

// collectUnclaimed returns the processor records that no internal order
// claimed during the exact-match pass.
func collectUnclaimed(
	processors map[domain.Source][]domain.Transaction,
	claimed map[domain.Source]map[string]bool,
) []*domain.Transaction {
	var out []*domain.Transaction
	for src, txns := range processors {
		used := claimed[src]
		for i := range txns {
			t := &txns[i]
			if !used[refKey(t)] {
				out = append(out, t)
			}
		}
	}
	return out
}
