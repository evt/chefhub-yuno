// Command gendata generates realistic test fixtures for the reconciliation
// engine: an internal-orders JSON file and two processor-settlement CSV files
// with a controlled distribution of clean matches and intentional
// discrepancies.
//
// The defaults model the distribution described in the task brief:
//
//	~85% clean matches across all sources
//	  5% missing in settlement (internal only)
//	  3% phantom charges (processor only, no internal order)
//	  2% amount mismatches
//	  1% status mismatches (processor refunded, internal still completed)
//	  4% internal-only non-settling orders (pending/failed)
//
// Usage:
//
//	gendata -out testdata -n 1000 -seed 42
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

func main() {
	var (
		outDir = flag.String("out", "testdata", "output directory")
		n      = flag.Int("n", 1000, "number of internal orders to generate")
		seed   = flag.Uint64("seed", 42, "PRNG seed for reproducible output")
	)
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(logger, *outDir, *n, *seed); err != nil {
		logger.Error("data generation failed", "error", err)
		os.Exit(1)
	}
}

// Distribution percentages (must sum to 100). Tuning here changes what the
// generated fixtures stress-test.
const (
	pctCleanMatch    = 85
	pctMissingInSet  = 5
	pctPhantom       = 3
	pctAmountDrift   = 2
	pctRefundNotSync = 1
	pctNonSettling   = 4
)

// Compile-time guard: distribution must total 100. The default branch in
// generate() consumes the remainder, but keeping the constant documents the
// intent and trips this check if percentages drift.
var _ = [1]struct{}{}[pctCleanMatch+pctMissingInSet+pctPhantom+pctAmountDrift+pctRefundNotSync+pctNonSettling-100]

// currencies and their relative weights (rough Southeast-Asia mix).
var currencies = []struct {
	code   string
	weight int
}{
	{"PHP", 5},
	{"IDR", 3},
	{"THB", 2},
}

type generator struct {
	rng    *rand.Rand
	now    time.Time
	orders []internalOrder
	procA  []procARow
	procB  []procBRow
}

func run(logger *slog.Logger, outDir string, n int, seed uint64) error {
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return fmt.Errorf("create out dir: %w", err)
	}

	g := &generator{
		// math/rand/v2 is deliberate here: this generator only produces fixture
		// data for tests, not anything security-sensitive.
		rng: rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15)), //nolint:gosec // test fixtures
		now: time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC),
	}
	g.generate(n)

	if err := writeJSON(filepath.Join(outDir, "internal_orders.json"), g.orders); err != nil {
		return err
	}
	if err := writeProcessorA(filepath.Join(outDir, "processor_a_settlements.csv"), g.procA); err != nil {
		return err
	}
	if err := writeProcessorB(filepath.Join(outDir, "processor_b_settlements.csv"), g.procB); err != nil {
		return err
	}

	logger.Info("generated fixtures",
		"out", outDir,
		"internal_orders", len(g.orders),
		"processor_a", len(g.procA),
		"processor_b", len(g.procB),
	)
	return nil
}

// generate populates orders, procA and procB with the configured mix of
// clean and discrepant records.
func (g *generator) generate(n int) {
	for i := range n {
		bucket := g.rng.IntN(100)
		switch {
		case bucket < pctCleanMatch:
			g.emitClean(i)
		case bucket < pctCleanMatch+pctMissingInSet:
			g.emitMissingInSettlement(i)
		case bucket < pctCleanMatch+pctMissingInSet+pctPhantom:
			g.emitPhantom(i)
		case bucket < pctCleanMatch+pctMissingInSet+pctPhantom+pctAmountDrift:
			g.emitAmountDrift(i)
		case bucket < pctCleanMatch+pctMissingInSet+pctPhantom+pctAmountDrift+pctRefundNotSync:
			g.emitRefundNotSynced(i)
		default:
			g.emitNonSettling(i)
		}
	}
}

func (g *generator) emitClean(i int) {
	o := g.makeOrder(i, "completed")
	g.orders = append(g.orders, o)
	g.routeToProcessor(o, o.Amount, "settled", "success")
}

func (g *generator) emitMissingInSettlement(i int) {
	// Internal record only — no processor row written. Status must be one
	// that the engine treats as "should have settled".
	g.orders = append(g.orders, g.makeOrder(i, "completed"))
}

func (g *generator) emitPhantom(i int) {
	// Processor records only — no internal order. Use a reference that does
	// not collide with any real order.
	ref := fmt.Sprintf("ord_phantom_%05d", i)
	amount, currency := g.makeAmount(), g.pickCurrency()
	ts := g.randomTimestamp()
	if g.rng.IntN(2) == 0 {
		g.procA = append(g.procA, procARow{
			TransactionID:     fmt.Sprintf("txn_A_%06d", i),
			MerchantReference: ref,
			GrossAmount:       amount,
			CurrencyCode:      currency,
			State:             "SETTLED",
			ProcessedAt:       ts.Format(time.RFC3339),
		})
	} else {
		g.procB = append(g.procB, procBRow{
			TxnID:            fmt.Sprintf("PB-%06d", i),
			PaymentReference: ref,
			TotalAmount:      amount,
			Currency:         currency,
			Result:           "success",
			Timestamp:        ts.Unix(),
		})
	}
}

func (g *generator) emitAmountDrift(i int) {
	o := g.makeOrder(i, "completed")
	g.orders = append(g.orders, o)
	// Drift between $0.10 and $4.50 to clear the $0.05 rounding tolerance,
	// or a much larger drift 20% of the time to seed high-severity findings.
	driftCents := g.rng.IntN(440) + 10
	if g.rng.IntN(5) == 0 {
		driftCents = g.rng.IntN(40000) + 1000
	}
	processorAmount := o.Amount - decimalFromCents(int64(driftCents))
	if g.rng.IntN(2) == 0 {
		processorAmount = o.Amount + decimalFromCents(int64(driftCents))
	}
	g.routeToProcessor(o, processorAmount, "settled", "success")
}

func (g *generator) emitRefundNotSynced(i int) {
	o := g.makeOrder(i, "completed")
	g.orders = append(g.orders, o)
	g.routeToProcessor(o, o.Amount, "refunded", "refunded")
}

func (g *generator) emitNonSettling(i int) {
	status := "pending"
	if g.rng.IntN(2) == 0 {
		status = "failed"
	}
	g.orders = append(g.orders, g.makeOrder(i, status))
}

// makeOrder builds a base internalOrder; the caller chooses the status.
func (g *generator) makeOrder(i int, status string) internalOrder {
	return internalOrder{
		OrderID:    fmt.Sprintf("ord_%06d", i),
		CustomerID: fmt.Sprintf("cust_%04d", g.rng.IntN(2000)),
		ChefID:     fmt.Sprintf("chef_%03d", g.rng.IntN(300)),
		Amount:     g.makeAmount(),
		Currency:   g.pickCurrency(),
		Status:     status,
		CreatedAt:  g.randomTimestamp().Format(time.RFC3339),
	}
}

// routeToProcessor writes the matching processor row for a given order.
// stateA and resultB are the per-processor status vocabulary words.
func (g *generator) routeToProcessor(o internalOrder, amount float64, stateA, resultB string) {
	// Settlement happens shortly after the order — within a few seconds, with
	// occasional minute-scale outliers to give fuzzy matching something
	// realistic to chew on.
	created, _ := time.Parse(time.RFC3339, o.CreatedAt)
	delta := time.Duration(g.rng.IntN(3))*time.Second + time.Duration(g.rng.IntN(900))*time.Millisecond
	ts := created.Add(delta)

	if g.rng.IntN(10) < 6 {
		g.procA = append(g.procA, procARow{
			TransactionID:     fmt.Sprintf("txn_A_%s", o.OrderID),
			MerchantReference: o.OrderID,
			GrossAmount:       amount,
			CurrencyCode:      o.Currency,
			State:             upper(stateA),
			ProcessedAt:       ts.Format(time.RFC3339),
		})
		return
	}
	g.procB = append(g.procB, procBRow{
		TxnID:            fmt.Sprintf("PB-%s", o.OrderID),
		PaymentReference: o.OrderID,
		TotalAmount:      amount,
		Currency:         o.Currency,
		Result:           resultB,
		Timestamp:        ts.Unix(),
	})
}

// makeAmount returns a realistic order amount between $5 and $500.
func (g *generator) makeAmount() float64 {
	cents := g.rng.IntN(49500) + 500
	return float64(cents) / 100.0 //nolint:mnd // explicit cents-to-dollar conversion
}

// pickCurrency returns a weighted random ISO currency code.
func (g *generator) pickCurrency() string {
	total := 0
	for _, c := range currencies {
		total += c.weight
	}
	roll := g.rng.IntN(total)
	for _, c := range currencies {
		if roll < c.weight {
			return c.code
		}
		roll -= c.weight
	}
	return currencies[0].code
}

// randomTimestamp returns a timestamp uniformly distributed in the past week
// relative to g.now.
func (g *generator) randomTimestamp() time.Time {
	weekSeconds := int64(7 * 24 * 60 * 60)
	offset := time.Duration(g.rng.Int64N(weekSeconds)) * time.Second
	return g.now.Add(-offset)
}

func decimalFromCents(cents int64) float64 {
	return float64(cents) / 100.0 //nolint:mnd // explicit cents-to-dollar conversion
}

func upper(s string) string {
	out := make([]byte, len(s))
	for i := range s {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}

// --- on-disk schemas ---------------------------------------------------------

type internalOrder struct {
	OrderID    string  `json:"order_id"`
	CustomerID string  `json:"customer_id"`
	ChefID     string  `json:"chef_id"`
	Amount     float64 `json:"amount"`
	Currency   string  `json:"currency"`
	Status     string  `json:"status"`
	CreatedAt  string  `json:"created_at"`
}

type procARow struct {
	TransactionID     string
	MerchantReference string
	GrossAmount       float64
	CurrencyCode      string
	State             string
	ProcessedAt       string
}

type procBRow struct {
	TxnID            string
	PaymentReference string
	TotalAmount      float64
	Currency         string
	Result           string
	Timestamp        int64
}

func writeJSON(path string, orders []internalOrder) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create internal orders %q: %w", path, err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if encErr := enc.Encode(orders); encErr != nil {
		return fmt.Errorf("encode internal orders: %w", encErr)
	}
	return nil
}

func writeProcessorA(path string, rows []procARow) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create processor A %q: %w", path, err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if werr := w.Write([]string{
		"transaction_id", "merchant_reference", "gross_amount",
		"currency_code", "state", "processed_at",
	}); werr != nil {
		return fmt.Errorf("write header: %w", werr)
	}
	for _, r := range rows {
		if werr := w.Write([]string{
			r.TransactionID,
			r.MerchantReference,
			fmt.Sprintf("%.2f", r.GrossAmount),
			r.CurrencyCode,
			r.State,
			r.ProcessedAt,
		}); werr != nil {
			return fmt.Errorf("write row: %w", werr)
		}
	}
	w.Flush()
	if ferr := w.Error(); ferr != nil {
		return fmt.Errorf("flush: %w", ferr)
	}
	return nil
}

func writeProcessorB(path string, rows []procBRow) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create processor B %q: %w", path, err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if werr := w.Write([]string{
		"txn_id", "payment_reference", "total_amount",
		"currency", "result", "timestamp",
	}); werr != nil {
		return fmt.Errorf("write header: %w", werr)
	}
	for _, r := range rows {
		if werr := w.Write([]string{
			r.TxnID,
			r.PaymentReference,
			fmt.Sprintf("%.2f", r.TotalAmount),
			r.Currency,
			r.Result,
			strconv.FormatInt(r.Timestamp, 10),
		}); werr != nil {
			return fmt.Errorf("write row: %w", werr)
		}
	}
	w.Flush()
	if ferr := w.Error(); ferr != nil {
		return fmt.Errorf("flush: %w", ferr)
	}
	return nil
}
