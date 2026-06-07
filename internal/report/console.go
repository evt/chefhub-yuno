package report

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/evt/chefhub/internal/domain"
)

// topDiscrepancies is how many of the highest-impact findings are shown in
// the console summary. Keeps stdout digestible while pointing operators at
// the JSON/CSV exports for the full list.
const topDiscrepancies = 10

// PrintSummary writes a finance-facing summary of the reconciliation run to
// w. The full structured output lives in the JSON and CSV files written
// alongside it.
func PrintSummary(w io.Writer, report domain.Report) {
	fmt.Fprintln(w, "=== ChefHub Settlement Reconciliation ===")
	fmt.Fprintf(w, "generated at: %s\n\n", report.GeneratedAt.Format("2006-01-02T15:04:05Z07:00"))

	fmt.Fprintln(w, "-- Sources --")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, src := range sortedSources(report.SourceCounts) {
		fmt.Fprintf(tw, "  %s\t%d records\n", src, report.SourceCounts[src])
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\nmatched cleanly: %d internal orders\n", report.MatchedCount)
	fmt.Fprintf(w, "discrepancies:   %d\n\n", len(report.Discrepancies))

	if len(report.DiscrepancyCounts) > 0 {
		fmt.Fprintln(w, "-- Discrepancies by type --")
		tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, t := range sortedTypes(report.DiscrepancyCounts) {
			fmt.Fprintf(tw, "  %s\t%d\n", t, report.DiscrepancyCounts[t])
		}
		_ = tw.Flush()
		fmt.Fprintln(w)
	}

	if len(report.FinancialExposure) > 0 {
		fmt.Fprintln(w, "-- Financial exposure --")
		tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, cur := range sortedCurrencies(report.FinancialExposure) {
			fmt.Fprintf(tw, "  %s\t%s\n", cur, report.FinancialExposure[cur].Decimal())
		}
		_ = tw.Flush()
		fmt.Fprintln(w)
	}

	if len(report.Discrepancies) == 0 {
		fmt.Fprintln(w, "no discrepancies found.")
		return
	}

	limit := min(topDiscrepancies, len(report.Discrepancies))
	fmt.Fprintf(w, "-- Top %d by financial impact --\n", limit)
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  severity\timpact\ttype\tref\tdetail")
	for _, d := range report.Discrepancies[:limit] {
		fmt.Fprintf(tw, "  %s\t%s %s\t%s\t%s\t%s\n",
			strings.ToUpper(string(d.Severity)),
			d.Currency,
			d.FinancialImpact.Decimal(),
			d.Type,
			truncate(d.ExternalRef, 20),
			truncate(d.Detail, 60),
		)
	}
	_ = tw.Flush()
}

func sortedSources(m map[domain.Source]int) []domain.Source {
	out := make([]domain.Source, 0, len(m))
	for s := range m {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

func sortedTypes(m map[domain.DiscrepancyType]int) []domain.DiscrepancyType {
	out := make([]domain.DiscrepancyType, 0, len(m))
	for t := range m {
		out = append(out, t)
	}
	slices.Sort(out)
	return out
}

func sortedCurrencies(m map[string]domain.Money) []string {
	out := make([]string, 0, len(m))
	for c := range m {
		out = append(out, c)
	}
	slices.Sort(out)
	return out
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit-1] + "…"
}
