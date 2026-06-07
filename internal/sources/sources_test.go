package sources_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/evt/chefhub/internal/domain"
	"github.com/evt/chefhub/internal/sources"
)

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestInternalLoader_HappyPath(t *testing.T) {
	t.Parallel()

	path := writeFile(t, "orders.json", `[
		{"order_id":"ord_1","customer_id":"c1","chef_id":"ch1","amount":12.34,"currency":"php","status":"completed","created_at":"2026-06-01T10:00:00Z"},
		{"order_id":"ord_2","customer_id":"c2","chef_id":"ch2","amount":99.95,"currency":"IDR","status":"refunded","created_at":"2026-06-02T11:30:00Z"}
	]`)

	got, err := sources.NewInternalLoader().Load(context.Background(), path)
	require.NoError(t, err)
	require.Len(t, got, 2)

	assert.Equal(t, "ord_1", got[0].ExternalRef)
	assert.Equal(t, domain.Money(1234), got[0].Amount)
	assert.Equal(t, "PHP", got[0].Currency)
	assert.Equal(t, domain.StatusCompleted, got[0].Status)
	assert.Equal(t, domain.SourceInternal, got[0].Source)
	assert.Equal(t, domain.StatusRefunded, got[1].Status)
}

func TestProcessorALoader_HappyPath(t *testing.T) {
	t.Parallel()

	path := writeFile(t, "a.csv", `transaction_id,merchant_reference,gross_amount,currency_code,state,processed_at
txn_A_1,ord_1,12.34,PHP,SETTLED,2026-06-01T10:00:05Z
txn_A_2,ord_2,99.95,IDR,REFUNDED,2026-06-02T11:31:00Z
`)

	got, err := sources.NewProcessorALoader().Load(context.Background(), path)
	require.NoError(t, err)
	require.Len(t, got, 2)

	assert.Equal(t, "ord_1", got[0].ExternalRef)
	assert.Equal(t, "txn_A_1", got[0].SourceID)
	assert.Equal(t, domain.Money(1234), got[0].Amount)
	assert.Equal(t, domain.StatusCompleted, got[0].Status)
	assert.Equal(t, domain.SourceProcessorA, got[0].Source)
	assert.Equal(t, domain.StatusRefunded, got[1].Status)
}

func TestProcessorBLoader_HappyPath(t *testing.T) {
	t.Parallel()

	path := writeFile(t, "b.csv", `txn_id,payment_reference,total_amount,currency,result,timestamp
PB-1,ord_1,12.34,PHP,success,1748772000
PB-2,ord_2,99.95,thb,refunded,1748861460
`)

	got, err := sources.NewProcessorBLoader().Load(context.Background(), path)
	require.NoError(t, err)
	require.Len(t, got, 2)

	assert.Equal(t, "ord_1", got[0].ExternalRef)
	assert.Equal(t, "PB-1", got[0].SourceID)
	assert.Equal(t, domain.Money(1234), got[0].Amount)
	assert.Equal(t, "PHP", got[0].Currency)
	assert.Equal(t, domain.StatusCompleted, got[0].Status)
	assert.Equal(t, domain.SourceProcessorB, got[0].Source)
	assert.Equal(t, "THB", got[1].Currency)
	assert.Equal(t, domain.StatusRefunded, got[1].Status)
}

func TestProcessorALoader_MissingColumn(t *testing.T) {
	t.Parallel()
	path := writeFile(t, "a.csv", "transaction_id,gross_amount\nA,1.00\n")
	_, err := sources.NewProcessorALoader().Load(context.Background(), path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing required column")
}
