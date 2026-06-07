// Package report renders a domain.Report onto the artifacts the finance team
// consumes: a JSON file with the full structured payload, a CSV file with one
// row per discrepancy, and a human-readable console summary.
package report

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/evt/chefhub/internal/domain"
)

// WriteJSON serializes the report as indented JSON at path. Parent
// directories are created if necessary.
func WriteJSON(report domain.Report, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create json output dir: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create json report %q: %w", path, err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		return fmt.Errorf("encode json report: %w", err)
	}
	return nil
}

// csvHeader is the canonical column order for the discrepancy CSV export.
// Order is fixed so downstream spreadsheets and finance pipelines can rely
// on it.
var csvHeader = []string{
	"type",
	"severity",
	"external_ref",
	"customer_id",
	"chef_id",
	"currency",
	"internal_amount",
	"processor_amount",
	"internal_status",
	"processor_status",
	"financial_impact",
	"sources",
	"occurred_at",
	"detail",
}

// WriteCSV writes one row per discrepancy in the order they appear on the
// report (already sorted by descending FinancialImpact).
func WriteCSV(report domain.Report, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create csv output dir: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create csv report %q: %w", path, err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write(csvHeader); err != nil {
		return fmt.Errorf("write csv header: %w", err)
	}
	for _, d := range report.Discrepancies {
		row := []string{
			string(d.Type),
			string(d.Severity),
			d.ExternalRef,
			d.CustomerID,
			d.ChefID,
			d.Currency,
			d.InternalAmount.Decimal(),
			d.ProcessorAmount.Decimal(),
			string(d.InternalStatus),
			string(d.ProcessorStatus),
			d.FinancialImpact.Decimal(),
			joinSources(d.Sources),
			d.OccurredAt.Format("2006-01-02T15:04:05Z07:00"),
			d.Detail,
		}
		if err := w.Write(row); err != nil {
			return fmt.Errorf("write csv row: %w", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return fmt.Errorf("flush csv: %w", err)
	}
	return nil
}

func joinSources(srcs []domain.Source) string {
	parts := make([]string, len(srcs))
	for i, s := range srcs {
		parts[i] = string(s)
	}
	sort.Strings(parts)
	return strings.Join(parts, "+")
}
