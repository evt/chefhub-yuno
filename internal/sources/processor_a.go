package sources

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/evt/chefhub/internal/domain"
)

// ProcessorALoader reads settlement reports from Processor A — a regional
// card processor. Its export is CSV with ISO-8601 timestamps and uppercase
// status codes.
//
// Expected columns (header row is required):
//
//	transaction_id, merchant_reference, gross_amount, currency_code,
//	state, processed_at
type ProcessorALoader struct{}

// NewProcessorALoader returns a loader for Processor A's CSV settlement
// reports.
func NewProcessorALoader() *ProcessorALoader {
	return &ProcessorALoader{}
}

// Load parses a Processor A settlement CSV and normalizes each row.
func (l *ProcessorALoader) Load(_ context.Context, path string) ([]domain.Transaction, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open processor A report %q: %w", path, err)
	}
	defer f.Close()

	reader := csv.NewReader(f)
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true

	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read processor A header %q: %w", path, err)
	}
	cols, err := indexHeader(header, []string{
		"transaction_id", "merchant_reference", "gross_amount",
		"currency_code", "state", "processed_at",
	})
	if err != nil {
		return nil, fmt.Errorf("processor A %q: %w", path, err)
	}

	var out []domain.Transaction
	for line := 2; ; line++ {
		row, rerr := reader.Read()
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			return nil, fmt.Errorf("processor A row %d: %w", line, rerr)
		}

		amount, perr := domain.ParseMoney(row[cols["gross_amount"]])
		if perr != nil {
			return nil, fmt.Errorf("processor A row %d: amount: %w", line, perr)
		}

		ts, terr := time.Parse(time.RFC3339, row[cols["processed_at"]])
		if terr != nil {
			return nil, fmt.Errorf("processor A row %d: processed_at: %w", line, terr)
		}

		status, serr := parseProcessorAStatus(row[cols["state"]])
		if serr != nil {
			return nil, fmt.Errorf("processor A row %d: %w", line, serr)
		}

		out = append(out, domain.Transaction{
			ExternalRef: row[cols["merchant_reference"]],
			SourceID:    row[cols["transaction_id"]],
			Amount:      amount,
			Currency:    strings.ToUpper(row[cols["currency_code"]]),
			Status:      status,
			OccurredAt:  ts.UTC(),
			Source:      domain.SourceProcessorA,
		})
	}
	return out, nil
}

func parseProcessorAStatus(s string) (domain.Status, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "PENDING", "AUTHORIZED":
		return domain.StatusPending, nil
	case "SETTLED", "CAPTURED":
		return domain.StatusCompleted, nil
	case "REFUNDED", "REVERSED":
		return domain.StatusRefunded, nil
	case "FAILED", "DECLINED":
		return domain.StatusFailed, nil
	default:
		return "", fmt.Errorf("unknown processor A state %q", s)
	}
}
