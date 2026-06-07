// Package sources provides loaders that read raw settlement data from each
// upstream system (internal orders database, payment processor reports) and
// normalize it onto domain.Transaction so the reconciliation engine can
// operate on a single canonical shape.
package sources

import (
	"context"

	"github.com/evt/chefhub/internal/domain"
)

// Loader reads transactions for a single source from the given file path and
// returns them normalized onto domain.Transaction.
//
// Implementations are expected to be stateless and safe to call from a single
// goroutine. Loading is performed in batch (not streamed) because the
// reconciliation engine needs the full dataset in memory to build match
// indexes; see README "Scaling beyond memory" for the streaming approach.
type Loader interface {
	Load(ctx context.Context, path string) ([]domain.Transaction, error)
}
