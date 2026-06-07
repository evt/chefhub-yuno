package sources

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/evt/chefhub/internal/domain"
)

// ProcessorBLoader reads settlement reports from Processor B — a local
// e-wallet provider (think GCash or GoPay). Its export is CSV with unix
// epoch timestamps, lowercase status words, and a different set of column
// names from Processor A on purpose.
//
// Expected columns (header row is required):
//
//	txn_id, payment_reference, total_amount, currency, result, timestamp
//
// timestamp is unix epoch seconds.
type ProcessorBLoader struct{}

// NewProcessorBLoader returns a loader for Processor B's CSV settlement
// reports.
func NewProcessorBLoader() *ProcessorBLoader {
	return &ProcessorBLoader{}
}

// Load parses a Processor B settlement CSV and normalizes each row.
func (l *ProcessorBLoader) Load(_ context.Context, path string) ([]domain.Transaction, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open processor B report %q: %w", path, err)
	}
	defer f.Close()

	reader := csv.NewReader(f)
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true

	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read processor B header %q: %w", path, err)
	}
	cols, err := indexHeader(header, []string{
		"txn_id", "payment_reference", "total_amount",
		"currency", "result", "timestamp",
	})
	if err != nil {
		return nil, fmt.Errorf("processor B %q: %w", path, err)
	}

	var out []domain.Transaction
	for line := 2; ; line++ {
		row, rerr := reader.Read()
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			return nil, fmt.Errorf("processor B row %d: %w", line, rerr)
		}

		amount, perr := domain.ParseMoney(row[cols["total_amount"]])
		if perr != nil {
			return nil, fmt.Errorf("processor B row %d: amount: %w", line, perr)
		}

		epoch, eerr := strconv.ParseInt(row[cols["timestamp"]], 10, 64)
		if eerr != nil {
			return nil, fmt.Errorf("processor B row %d: timestamp: %w", line, eerr)
		}

		status, serr := parseProcessorBStatus(row[cols["result"]])
		if serr != nil {
			return nil, fmt.Errorf("processor B row %d: %w", line, serr)
		}

		out = append(out, domain.Transaction{
			ExternalRef: row[cols["payment_reference"]],
			SourceID:    row[cols["txn_id"]],
			Amount:      amount,
			Currency:    strings.ToUpper(row[cols["currency"]]),
			Status:      status,
			OccurredAt:  time.Unix(epoch, 0).UTC(),
			Source:      domain.SourceProcessorB,
		})
	}
	return out, nil
}

func parseProcessorBStatus(s string) (domain.Status, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pending", "processing":
		return domain.StatusPending, nil
	case "success", "successful", "completed":
		return domain.StatusCompleted, nil
	case "refunded", "reversed":
		return domain.StatusRefunded, nil
	case "failed", "error", "declined":
		return domain.StatusFailed, nil
	default:
		return "", fmt.Errorf("unknown processor B result %q", s)
	}
}
