package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/evt/chefhub/internal/domain"
)

// InternalLoader reads ChefHub's internal orders database export. The export
// is a JSON array of order records and is treated as the source of truth for
// what customers ordered.
type InternalLoader struct{}

// NewInternalLoader returns a loader for the internal orders JSON export.
func NewInternalLoader() *InternalLoader {
	return &InternalLoader{}
}

// internalOrder mirrors the on-disk JSON schema. It is intentionally
// unexported: callers receive the normalized domain.Transaction.
type internalOrder struct {
	OrderID    string  `json:"order_id"`
	CustomerID string  `json:"customer_id"`
	ChefID     string  `json:"chef_id"`
	Amount     float64 `json:"amount"`
	Currency   string  `json:"currency"`
	Status     string  `json:"status"`
	CreatedAt  string  `json:"created_at"`
}

// Load parses the internal orders JSON file and normalizes each record.
func (l *InternalLoader) Load(_ context.Context, path string) ([]domain.Transaction, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read internal orders %q: %w", path, err)
	}

	var raw []internalOrder
	if uerr := json.Unmarshal(data, &raw); uerr != nil {
		return nil, fmt.Errorf("parse internal orders %q: %w", path, uerr)
	}

	out := make([]domain.Transaction, 0, len(raw))
	for i, r := range raw {
		amount, perr := domain.ParseMoney(fmt.Sprintf("%.2f", r.Amount))
		if perr != nil {
			return nil, fmt.Errorf("internal orders row %d: %w", i, perr)
		}

		ts, terr := time.Parse(time.RFC3339, r.CreatedAt)
		if terr != nil {
			return nil, fmt.Errorf("internal orders row %d: parse created_at %q: %w", i, r.CreatedAt, terr)
		}

		status, serr := parseInternalStatus(r.Status)
		if serr != nil {
			return nil, fmt.Errorf("internal orders row %d: %w", i, serr)
		}

		out = append(out, domain.Transaction{
			ExternalRef: r.OrderID,
			SourceID:    r.OrderID,
			CustomerID:  r.CustomerID,
			ChefID:      r.ChefID,
			Amount:      amount,
			Currency:    strings.ToUpper(r.Currency),
			Status:      status,
			OccurredAt:  ts.UTC(),
			Source:      domain.SourceInternal,
		})
	}
	return out, nil
}

// parseInternalStatus normalizes the internal database's status vocabulary.
func parseInternalStatus(s string) (domain.Status, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pending":
		return domain.StatusPending, nil
	case "completed", "paid":
		return domain.StatusCompleted, nil
	case "refunded":
		return domain.StatusRefunded, nil
	case "failed", "cancelled", "canceled":
		return domain.StatusFailed, nil
	default:
		return "", fmt.Errorf("unknown internal status %q", s)
	}
}
