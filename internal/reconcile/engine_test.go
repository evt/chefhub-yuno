package reconcile_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/evt/chefhub/internal/domain"
	"github.com/evt/chefhub/internal/reconcile"
)

// baseTime is the anchor for all test timestamps to keep fuzzy-window math
// readable.
var baseTime = time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)

func internalTxn(ref string, amount domain.Money, status domain.Status) domain.Transaction {
	return domain.Transaction{
		ExternalRef: ref,
		SourceID:    ref,
		CustomerID:  "cust_1",
		ChefID:      "chef_1",
		Amount:      amount,
		Currency:    "PHP",
		Status:      status,
		OccurredAt:  baseTime,
		Source:      domain.SourceInternal,
	}
}

func processorTxn(src domain.Source, ref string, amount domain.Money, status domain.Status) domain.Transaction {
	return domain.Transaction{
		ExternalRef: ref,
		SourceID:    string(src) + "_" + ref,
		Amount:      amount,
		Currency:    "PHP",
		Status:      status,
		OccurredAt:  baseTime.Add(2 * time.Second),
		Source:      src,
	}
}

func TestEngine_CleanMatch(t *testing.T) {
	t.Parallel()

	e := reconcile.NewEngine(reconcile.Defaults())
	r := e.Reconcile(context.Background(),
		[]domain.Transaction{internalTxn("ord_1", 1000, domain.StatusCompleted)},
		map[domain.Source][]domain.Transaction{
			domain.SourceProcessorA: {processorTxn(domain.SourceProcessorA, "ord_1", 1000, domain.StatusCompleted)},
		},
	)

	assert.Equal(t, 1, r.MatchedCount)
	assert.Empty(t, r.Discrepancies)
}

func TestEngine_DetectsAllFiveDiscrepancyTypes(t *testing.T) {
	t.Parallel()

	internal := []domain.Transaction{
		// 1. clean match — should not appear
		internalTxn("ord_clean", 1500, domain.StatusCompleted),
		// 2. missing in settlement — completed but no processor record
		internalTxn("ord_missing", 25000, domain.StatusCompleted),
		// 3. amount mismatch — processor says $0.50 less
		internalTxn("ord_amount", 10000, domain.StatusCompleted),
		// 4. status mismatch (non-refund kind) — processor says failed
		internalTxn("ord_status", 5000, domain.StatusCompleted),
		// 5. refund not synced — internal=completed, processor=refunded
		internalTxn("ord_refund", 7500, domain.StatusCompleted),
	}
	procA := []domain.Transaction{
		processorTxn(domain.SourceProcessorA, "ord_clean", 1500, domain.StatusCompleted),
		processorTxn(domain.SourceProcessorA, "ord_amount", 9950, domain.StatusCompleted),
		processorTxn(domain.SourceProcessorA, "ord_status", 5000, domain.StatusFailed),
		processorTxn(domain.SourceProcessorA, "ord_refund", 7500, domain.StatusRefunded),
		// 6. phantom — settled by processor but no internal record
		processorTxn(domain.SourceProcessorA, "ord_phantom", 30000, domain.StatusCompleted),
	}

	r := reconcile.NewEngine(reconcile.Defaults()).Reconcile(context.Background(), internal,
		map[domain.Source][]domain.Transaction{domain.SourceProcessorA: procA})

	assert.Equal(t, 1, r.MatchedCount, "only the clean ord_clean should be matched cleanly")
	assert.Len(t, r.Discrepancies, 5, "five discrepancy types should be detected")

	byType := groupByType(r.Discrepancies)
	require.Contains(t, byType, domain.DiscrepancyMissingInSettlement)
	require.Contains(t, byType, domain.DiscrepancyMissingInDatabase)
	require.Contains(t, byType, domain.DiscrepancyAmountMismatch)
	require.Contains(t, byType, domain.DiscrepancyStatusMismatch)
	require.Contains(t, byType, domain.DiscrepancyRefundNotApplied)

	// Financial impact: amount mismatch is the $0.50 diff (50 cents), not full amount.
	assert.Equal(t, domain.Money(50), byType[domain.DiscrepancyAmountMismatch][0].FinancialImpact)
	// Missing-in-settlement exposes the full internal amount.
	assert.Equal(t, domain.Money(25000), byType[domain.DiscrepancyMissingInSettlement][0].FinancialImpact)
	// Phantom exposes the full processor amount.
	assert.Equal(t, domain.Money(30000), byType[domain.DiscrepancyMissingInDatabase][0].FinancialImpact)
	// Refund-not-applied uses the processor amount (what the customer got back).
	assert.Equal(t, domain.Money(7500), byType[domain.DiscrepancyRefundNotApplied][0].FinancialImpact)

	// Sorted by descending impact: phantom $300 > missing $250 > refund $75 > status $50 > amount $0.50.
	assert.Equal(t, domain.DiscrepancyMissingInDatabase, r.Discrepancies[0].Type)
	assert.Equal(t, domain.DiscrepancyAmountMismatch, r.Discrepancies[len(r.Discrepancies)-1].Type)

	// Exposure aggregates by currency.
	assert.Equal(t, domain.Money(25000+30000+7500+5000+50), r.FinancialExposure["PHP"])
}

func TestEngine_AmountWithinToleranceIsClean(t *testing.T) {
	t.Parallel()

	r := reconcile.NewEngine(reconcile.Defaults()).Reconcile(context.Background(),
		[]domain.Transaction{internalTxn("ord_1", 10000, domain.StatusCompleted)},
		map[domain.Source][]domain.Transaction{
			// 3-cent diff — under the 5-cent default tolerance.
			domain.SourceProcessorA: {processorTxn(domain.SourceProcessorA, "ord_1", 10003, domain.StatusCompleted)},
		},
	)

	assert.Equal(t, 1, r.MatchedCount)
	assert.Empty(t, r.Discrepancies)
}

func TestEngine_FailedAndPendingOrdersAreNotMissing(t *testing.T) {
	t.Parallel()

	r := reconcile.NewEngine(reconcile.Defaults()).Reconcile(context.Background(),
		[]domain.Transaction{
			internalTxn("ord_failed", 1000, domain.StatusFailed),
			internalTxn("ord_pending", 2000, domain.StatusPending),
		},
		map[domain.Source][]domain.Transaction{
			domain.SourceProcessorA: {},
		},
	)

	assert.Equal(t, 0, r.MatchedCount)
	assert.Empty(t, r.Discrepancies, "failed and pending orders should not be flagged as missing settlements")
}

func TestEngine_FuzzyMatchingRescuesDifferentReferences(t *testing.T) {
	t.Parallel()

	internal := []domain.Transaction{{
		ExternalRef: "ord_internal_xyz",
		SourceID:    "ord_internal_xyz",
		Amount:      5000,
		Currency:    "PHP",
		Status:      domain.StatusCompleted,
		OccurredAt:  baseTime,
		Source:      domain.SourceInternal,
	}}
	procA := []domain.Transaction{{
		ExternalRef: "PROCESSOR_DIFFERENT_ID", // intentionally not equal
		SourceID:    "txn_A_xyz",
		Amount:      5002, // 2 cents off — within tolerance
		Currency:    "PHP",
		Status:      domain.StatusCompleted,
		OccurredAt:  baseTime.Add(3 * time.Second), // within 5s window
		Source:      domain.SourceProcessorA,
	}}

	withFuzzy := reconcile.Defaults()
	r := reconcile.NewEngine(withFuzzy).Reconcile(context.Background(), internal,
		map[domain.Source][]domain.Transaction{domain.SourceProcessorA: procA})

	assert.Equal(t, 1, r.MatchedCount, "fuzzy matcher should pair the two records")
	assert.Empty(t, r.Discrepancies, "no missing/phantom emissions after fuzzy rescue")

	// With fuzzy disabled, the same input should produce both a
	// missing-in-settlement and a phantom finding.
	noFuzzy := reconcile.Defaults()
	noFuzzy.EnableFuzzyMatching = false
	r2 := reconcile.NewEngine(noFuzzy).Reconcile(context.Background(), internal,
		map[domain.Source][]domain.Transaction{domain.SourceProcessorA: procA})

	assert.Equal(t, 0, r2.MatchedCount)
	assert.Len(t, r2.Discrepancies, 2)
}

func TestEngine_FuzzyDoesNotMatchOutsideWindow(t *testing.T) {
	t.Parallel()

	internal := []domain.Transaction{{
		ExternalRef: "ord_a",
		Amount:      5000,
		Currency:    "PHP",
		Status:      domain.StatusCompleted,
		OccurredAt:  baseTime,
		Source:      domain.SourceInternal,
	}}
	procA := []domain.Transaction{{
		ExternalRef: "PROC_REF",
		Amount:      5000,
		Currency:    "PHP",
		Status:      domain.StatusCompleted,
		OccurredAt:  baseTime.Add(time.Hour), // way outside fuzzy window
		Source:      domain.SourceProcessorA,
	}}

	r := reconcile.NewEngine(reconcile.Defaults()).Reconcile(context.Background(), internal,
		map[domain.Source][]domain.Transaction{domain.SourceProcessorA: procA})

	assert.Equal(t, 0, r.MatchedCount)
	assert.Len(t, r.Discrepancies, 2)
}

func TestEngine_MatchesAcrossMultipleProcessors(t *testing.T) {
	t.Parallel()

	internal := []domain.Transaction{
		internalTxn("ord_routed_a", 1000, domain.StatusCompleted),
		internalTxn("ord_routed_b", 2000, domain.StatusCompleted),
		internalTxn("ord_b_status", 3000, domain.StatusCompleted),
	}
	procA := []domain.Transaction{
		processorTxn(domain.SourceProcessorA, "ord_routed_a", 1000, domain.StatusCompleted),
		// Phantom only Processor B has no internal counterpart.
		processorTxn(domain.SourceProcessorA, "ord_phantom_a", 4000, domain.StatusCompleted),
	}
	procB := []domain.Transaction{
		processorTxn(domain.SourceProcessorB, "ord_routed_b", 2000, domain.StatusCompleted),
		// Processor B settled but with refunded status — should yield
		// refund_not_applied via the Processor B side.
		processorTxn(domain.SourceProcessorB, "ord_b_status", 3000, domain.StatusRefunded),
	}

	r := reconcile.NewEngine(reconcile.Defaults()).Reconcile(context.Background(), internal,
		map[domain.Source][]domain.Transaction{
			domain.SourceProcessorA: procA,
			domain.SourceProcessorB: procB,
		})

	assert.Equal(t, 2, r.MatchedCount, "ord_routed_a and ord_routed_b should match cleanly")
	byType := groupByType(r.Discrepancies)

	require.Contains(t, byType, domain.DiscrepancyMissingInDatabase)
	assert.Equal(t, domain.SourceProcessorA, byType[domain.DiscrepancyMissingInDatabase][0].Sources[0])

	require.Contains(t, byType, domain.DiscrepancyRefundNotApplied)
	assert.Equal(t, []domain.Source{domain.SourceInternal, domain.SourceProcessorB},
		byType[domain.DiscrepancyRefundNotApplied][0].Sources)
}

func TestEngine_SeverityRanking(t *testing.T) {
	t.Parallel()

	internal := []domain.Transaction{
		internalTxn("ord_high", 20000, domain.StatusCompleted),   // $200 missing → HIGH
		internalTxn("ord_med", 2000, domain.StatusCompleted),     // $20 missing → MEDIUM
		internalTxn("ord_low_amt", 5000, domain.StatusCompleted), // $0.20 diff → LOW
	}
	procA := []domain.Transaction{
		processorTxn(domain.SourceProcessorA, "ord_low_amt", 5020, domain.StatusCompleted),
	}

	r := reconcile.NewEngine(reconcile.Defaults()).Reconcile(context.Background(), internal,
		map[domain.Source][]domain.Transaction{domain.SourceProcessorA: procA})

	byType := groupByType(r.Discrepancies)
	missings := byType[domain.DiscrepancyMissingInSettlement]
	require.Len(t, missings, 2)

	severities := map[string]domain.Severity{}
	for _, d := range r.Discrepancies {
		severities[d.ExternalRef] = d.Severity
	}
	assert.Equal(t, domain.SeverityHigh, severities["ord_high"])
	assert.Equal(t, domain.SeverityMedium, severities["ord_med"])
	assert.Equal(t, domain.SeverityLow, severities["ord_low_amt"])
}

func groupByType(ds []domain.Discrepancy) map[domain.DiscrepancyType][]domain.Discrepancy {
	out := map[domain.DiscrepancyType][]domain.Discrepancy{}
	for _, d := range ds {
		out[d.Type] = append(out[d.Type], d)
	}
	return out
}
