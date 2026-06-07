package domain

import "time"

// Source identifies the system that produced a Transaction record.
type Source string

const (
	// SourceInternal is ChefHub's internal orders database (the source of truth
	// for what customers ordered).
	SourceInternal Source = "internal"
	// SourceProcessorA is the regional card processor settlement report.
	SourceProcessorA Source = "processor_a"
	// SourceProcessorB is the local e-wallet processor settlement report.
	SourceProcessorB Source = "processor_b"
)

// Status is the lifecycle state of a transaction as understood by the source
// that emitted it. Statuses are normalized at the source-loader boundary so
// the reconciliation engine can compare them directly.
type Status string

const (
	// StatusPending means the payment has been authorized but not yet captured
	// or settled.
	StatusPending Status = "pending"
	// StatusCompleted means the payment was captured/settled successfully.
	StatusCompleted Status = "completed"
	// StatusRefunded means the payment was reversed back to the customer.
	StatusRefunded Status = "refunded"
	// StatusFailed means the payment did not go through (declined, error, etc.).
	StatusFailed Status = "failed"
)

// Transaction is the canonical, source-agnostic representation of a payment
// record after normalization. Each source loader is responsible for mapping
// its native schema onto this shape so the reconciliation engine can compare
// records uniformly.
type Transaction struct {
	// ExternalRef is the order identifier shared across systems and is the
	// primary key used for exact matching. For the internal source this is the
	// order ID; for processor sources it is the merchant/payment reference
	// field that points back at the order.
	ExternalRef string

	// SourceID is the identifier the source itself assigned to the record
	// (e.g. processor transaction ID). Kept for traceability in reports.
	SourceID string

	// CustomerID is the buyer placing the order. Processor reports may omit
	// this, in which case it is empty.
	CustomerID string

	// ChefID is the chef receiving the payout. Only the internal source
	// carries this directly.
	ChefID string

	// Amount is the gross transaction amount in minor units.
	Amount Money

	// Currency is the ISO-4217 code (PHP, IDR, THB, ...).
	Currency string

	// Status reflects the record's lifecycle state in its originating source.
	Status Status

	// OccurredAt is when the transaction happened, normalized to UTC.
	OccurredAt time.Time

	// Source is the system that emitted this record.
	Source Source
}
