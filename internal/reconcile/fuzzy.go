package reconcile

import (
	"sort"
	"time"

	"github.com/evt/chefhub/internal/domain"
)

// fuzzyCandidate is a leftover processor record that fuzzy matching may
// claim. It carries the source so we can mark it claimed in the right map.
type fuzzyCandidate struct {
	src domain.Source
	t   *domain.Transaction
}

// runFuzzyPass attempts to rescue internal orders that had no exact-reference
// match by pairing them with leftover processor records that share currency,
// amount (within AmountTolerance), and a timestamp inside FuzzyTimeWindow.
//
// Successful pairs are added as clean matches and emitted as MatchedCount
// increments; everything still unmatched is returned for normal
// missing/phantom handling.
func (e *Engine) runFuzzyPass(
	deferredMissing []*domain.Transaction,
	processors map[domain.Source][]domain.Transaction,
	claimed map[domain.Source]map[string]bool,
	report *domain.Report,
) []*domain.Transaction {
	if len(deferredMissing) == 0 {
		return deferredMissing
	}

	byCurrency := bucketLeftoversByCurrency(processors, claimed)

	var stillMissing []*domain.Transaction
	for _, o := range deferredMissing {
		if !e.tryFuzzyMatch(o, byCurrency[o.Currency], claimed, report) {
			stillMissing = append(stillMissing, o)
		}
	}
	return stillMissing
}

// bucketLeftoversByCurrency groups every unclaimed processor record by
// currency code and sorts each bucket by ascending timestamp so the matcher
// can pick the nearest-time candidate cheaply.
func bucketLeftoversByCurrency(
	processors map[domain.Source][]domain.Transaction,
	claimed map[domain.Source]map[string]bool,
) map[string][]fuzzyCandidate {
	out := map[string][]fuzzyCandidate{}
	for src, txns := range processors {
		used := claimed[src]
		for i := range txns {
			t := &txns[i]
			if used[refKey(t)] {
				continue
			}
			out[t.Currency] = append(out[t.Currency], fuzzyCandidate{src: src, t: t})
		}
	}
	for cur := range out {
		sort.Slice(out[cur], func(i, j int) bool {
			return out[cur][i].t.OccurredAt.Before(out[cur][j].t.OccurredAt)
		})
	}
	return out
}

// tryFuzzyMatch finds the best candidate (nearest timestamp, within all
// thresholds) for o inside bucket and claims it. Returns true when a match
// was found and the report has been updated accordingly.
func (e *Engine) tryFuzzyMatch(
	o *domain.Transaction,
	bucket []fuzzyCandidate,
	claimed map[domain.Source]map[string]bool,
	report *domain.Report,
) bool {
	best, ok := e.bestFuzzyCandidate(o, bucket, claimed)
	if !ok {
		return false
	}
	claimed[best.src][refKey(best.t)] = true
	report.MatchedCount++
	// Status disagreement still matters when matched fuzzily — emit it so
	// finance sees the lifecycle problem.
	if d, found := statusDiscrepancy(e.opts, o, best.t); found {
		report.Discrepancies = append(report.Discrepancies, d)
	}
	return true
}

// bestFuzzyCandidate returns the unclaimed candidate in bucket with the
// smallest timestamp delta to o, subject to amount and time-window
// constraints. Returns ok=false when nothing qualifies.
func (e *Engine) bestFuzzyCandidate(
	o *domain.Transaction,
	bucket []fuzzyCandidate,
	claimed map[domain.Source]map[string]bool,
) (fuzzyCandidate, bool) {
	bestIdx := -1
	var bestDelta time.Duration
	for i, p := range bucket {
		if claimed[p.src][refKey(p.t)] {
			continue
		}
		if (o.Amount - p.t.Amount).Abs() > e.opts.AmountTolerance {
			continue
		}
		delta := absDuration(o.OccurredAt.Sub(p.t.OccurredAt))
		if delta > e.opts.FuzzyTimeWindow {
			continue
		}
		if bestIdx == -1 || delta < bestDelta {
			bestIdx = i
			bestDelta = delta
		}
	}
	if bestIdx == -1 {
		return fuzzyCandidate{}, false
	}
	return bucket[bestIdx], true
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
