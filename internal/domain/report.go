package domain

import "time"

// Report is the full output of a reconciliation run, suitable for
// serialization to JSON or projection onto a CSV/dashboard.
type Report struct {
	GeneratedAt       time.Time               `json:"generated_at"`
	SourceCounts      map[Source]int          `json:"source_counts"`
	MatchedCount      int                     `json:"matched_count"`
	DiscrepancyCounts map[DiscrepancyType]int `json:"discrepancy_counts"`
	FinancialExposure map[string]Money        `json:"financial_exposure"`
	Discrepancies     []Discrepancy           `json:"discrepancies"`
}
