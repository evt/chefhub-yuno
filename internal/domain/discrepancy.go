package domain

import "time"

// DiscrepancyType categorizes a single reconciliation finding.
type DiscrepancyType string

const (
	// DiscrepancyMissingInSettlement marks an internal order that was never
	// reported as settled by any processor. The merchant may not yet be paid
	// for this order.
	DiscrepancyMissingInSettlement DiscrepancyType = "missing_in_settlement"

	// DiscrepancyMissingInDatabase marks a processor settlement that has no
	// corresponding order in the internal database (a phantom charge,
	// duplicate, or fraud).
	DiscrepancyMissingInDatabase DiscrepancyType = "missing_in_database"

	// DiscrepancyAmountMismatch marks a matched pair whose amounts differ by
	// more than the rounding tolerance.
	DiscrepancyAmountMismatch DiscrepancyType = "amount_mismatch"

	// DiscrepancyStatusMismatch marks a matched pair whose lifecycle status
	// disagrees (e.g. processor shows refunded, internal still shows
	// completed). Includes refunds not synced back to the internal database.
	DiscrepancyStatusMismatch DiscrepancyType = "status_mismatch"

	// DiscrepancyRefundNotApplied marks an internal order whose customer was
	// refunded by the processor but whose internal record was not updated to
	// reflect the reversal. This is a specialization of status mismatch that
	// the brief calls out explicitly.
	DiscrepancyRefundNotApplied DiscrepancyType = "refund_not_applied"
)

// Severity ranks discrepancies for triage in the report.
type Severity string

const (
	// SeverityHigh indicates direct financial exposure that requires immediate
	// finance team attention (e.g. missing settlements above threshold).
	SeverityHigh Severity = "high"
	// SeverityMedium indicates a real issue but with lower per-record impact.
	SeverityMedium Severity = "medium"
	// SeverityLow indicates a small or low-confidence finding (e.g. cents-level
	// amount mismatch).
	SeverityLow Severity = "low"
)

// Discrepancy is a single problematic record produced by the reconciliation
// engine, with enough context for a finance analyst to investigate without
// re-running the job.
type Discrepancy struct {
	Type            DiscrepancyType `json:"type"`
	Severity        Severity        `json:"severity"`
	ExternalRef     string          `json:"external_ref,omitempty"`
	CustomerID      string          `json:"customer_id,omitempty"`
	ChefID          string          `json:"chef_id,omitempty"`
	Currency        string          `json:"currency,omitempty"`
	InternalAmount  Money           `json:"internal_amount,omitempty"`
	ProcessorAmount Money           `json:"processor_amount,omitempty"`
	InternalStatus  Status          `json:"internal_status,omitempty"`
	ProcessorStatus Status          `json:"processor_status,omitempty"`
	FinancialImpact Money           `json:"financial_impact"`
	Sources         []Source        `json:"sources"`
	OccurredAt      time.Time       `json:"occurred_at"`
	Detail          string          `json:"detail"`
}
