# ChefHub Settlement Reconciliation

Reconciles ChefHub's internal orders database against multiple payment-processor
settlement reports, surfacing every discrepancy that would block chef payouts.

Built for the Yuno backend take-home challenge. The brief: reconcile ~47K
transactions across three sources (one internal DB export and two processor
settlement reports), classify every discrepancy by type, quantify the financial
exposure, and produce an actionable report for the finance team.

## Quick start

Requires Go 1.26+.

```bash
make build      # build the two CLIs into ./bin/
make demo       # generate fresh fixtures and reconcile them end-to-end
make test       # run the unit tests
make lint       # golangci-lint
```

`make demo` writes:

* `testdata/internal_orders.json` — 1,000 simulated internal orders.
* `testdata/processor_a_settlements.csv` — regional card processor report.
* `testdata/processor_b_settlements.csv` — local e-wallet processor report.
* `out/reconciliation.json` — full structured report (every discrepancy with
  context).
* `out/discrepancies.csv` — one row per discrepancy, ready for spreadsheets.
* A finance-facing summary on stdout, including the top 10 findings by
  financial impact.

The repository ships pre-generated fixtures and an example report so you can
inspect outputs without running anything: see [`testdata/`](testdata/) for the
inputs and [`examples/`](examples/) for the JSON, CSV, and console summary
they produce.

Run the reconciliation CLI directly to point it at your own files:

```bash
./bin/reconcile \
  -internal    /path/to/internal_orders.json \
  -processor-a /path/to/processor_a_settlements.csv \
  -processor-b /path/to/processor_b_settlements.csv \
  -out         /path/to/output_dir
# optional flags:
#   -no-fuzzy   disable second-pass fuzzy matching
```

## What it detects

The brief calls out five discrepancy categories. The engine maps each to a
distinct `DiscrepancyType` so the finance team can route them to different
playbooks.

| Type | Meaning | Financial impact |
|---|---|---|
| `missing_in_settlement` | Internal order with no matching processor record. The merchant may never collect this money. | Full internal amount |
| `missing_in_database` | Processor settled a charge with no matching internal order (phantom charge, duplicate, possible fraud). | Full processor amount |
| `amount_mismatch` | Matched pair whose amounts differ by more than $0.05. | Unsigned amount diff |
| `status_mismatch` | Matched pair whose lifecycle states disagree (e.g. processor says failed but internal says completed). | Full processor amount |
| `refund_not_applied` | Special case of status mismatch: processor refunded the customer but the internal record still shows completed. Called out separately because the operational response (re-issue payout reversal) is unique. | Full processor amount |

Every finding is tagged `high`/`medium`/`low` severity by dollar value
(`>=$100`/`>=$10`/`<$10`) and the detailed list is sorted by descending impact
so the most urgent items appear first.

## Sources and normalization

Each source has a deliberately different schema to model real-world integration
chaos:

| | Internal DB | Processor A | Processor B |
|---|---|---|---|
| Format | JSON | CSV | CSV |
| Reference field | `order_id` | `merchant_reference` | `payment_reference` |
| Amount field | `amount` (number) | `gross_amount` | `total_amount` |
| Currency field | `currency` | `currency_code` | `currency` |
| Status field | `status` (lowercase) | `state` (UPPERCASE) | `result` (lowercase) |
| Timestamp | `created_at` (RFC3339) | `processed_at` (RFC3339) | `timestamp` (unix epoch) |

Loaders live in [`internal/sources/`](internal/sources/). Each one reads its
native format and maps onto a canonical [`domain.Transaction`](internal/domain/transaction.go)
before handing off to the engine. Adding a fourth processor means writing one
new loader file — nothing in the engine changes.

Statuses are normalized at the loader boundary (e.g. `SETTLED`/`CAPTURED` →
`completed`, `success`/`successful` → `completed`, `REVERSED` → `refunded`).
Unknown statuses are rejected with a clear error rather than silently bucketed
as something safe, so schema drift fails loudly.

Money is stored as `int64` minor units (cents) to make `==` and `abs()` exact.
Floating-point amounts are parsed via [`domain.ParseMoney`](internal/domain/money.go)
which rounds to two decimals.

## Reconciliation logic

```
internal orders ──┐
                  │     ┌─── exact match by ExternalRef ──┐
processor A ──────┼────►│                                  ├──► clean / discrepant pair
processor B ──────┘     └─── leftovers ──► fuzzy pass ────┘
                                              │
                                              ▼
                              MissingInSettlement / MissingInDatabase
```

Pass 1 — exact match by reference. For every internal order, look it up by
`ExternalRef` in each processor's index:

* If a pair exists, compare amount (±$0.05 tolerance) and status. Emit any
  per-pair discrepancy.
* If no processor has a record AND the internal order should have settled
  (status was `completed` or `refunded`), defer the order as a candidate for
  the fuzzy pass below. `pending` and `failed` orders are skipped — they are
  not expected to produce settlement records.

Pass 2 — fuzzy match (stretch goal, on by default; disable with `-no-fuzzy`).
For each deferred-missing order, scan leftover processor records of the same
currency and pick the one whose amount is within tolerance and timestamp is
within a 5-second window. Sub-millisecond on 47K records: the leftovers are
bucketed by currency and sorted by time, so each candidate scan is small.

Whatever remains unmatched after pass 2 becomes `MissingInSettlement` (internal
side) or `MissingInDatabase` (processor side).

The full algorithm is in [`internal/reconcile/engine.go`](internal/reconcile/engine.go).
The five discrepancy types and the severity ranking are exercised by
[`engine_test.go`](internal/reconcile/engine_test.go).

## Output format

`reconciliation.json` is the canonical report. Structure:

```json
{
  "generated_at": "...",
  "source_counts": { "internal": 965, "processor_a": 541, "processor_b": 379 },
  "matched_count": 859,
  "discrepancy_counts": {
    "amount_mismatch": 18,
    "missing_in_database": 35,
    "missing_in_settlement": 43,
    "refund_not_applied": 8
  },
  "financial_exposure": { "IDR": "7130.90", "PHP": "9930.74", "THB": "5314.22" },
  "discrepancies": [ /* sorted by descending financial_impact */ ]
}
```

`discrepancies.csv` projects the same `discrepancies` array onto a fixed
column order suitable for Excel/Sheets. Each row carries the external ref,
customer ID, chef ID, both amounts, both statuses, the financial impact, and
a one-line `detail` field a finance analyst can read at a glance.

The stdout summary prints source counts, type counts, exposure by currency,
and the top 10 findings by impact — so an operator who runs the command
without looking at the files still gets the headline numbers.

## Architectural decisions

* **Canonical model at the boundary.** Source loaders translate native schemas
  into `domain.Transaction` immediately; the engine never sees a CSV row or a
  JSON map. This keeps the matching logic source-agnostic and makes adding a
  processor a self-contained file diff.
* **Money in minor units.** All amount math is `int64` cents. Tolerances,
  diffs, totals all stay exact, and the JSON marshaller renders them as
  fixed-precision decimal strings so downstream tools never re-introduce float
  drift.
* **In-memory two-pass.** At 47K records the working set is a few MB. An
  exact-match pass with map lookups (`O(N)`) followed by an optional fuzzy
  pass that buckets leftovers by currency keeps the wall clock under a
  quarter-second on a laptop. See "Scaling beyond memory" below for what
  changes at 10M.
* **Severity ranking by exposure.** Severity is derived from the per-record
  financial impact, not the discrepancy type, so the report's top entries are
  always "where is the most money at risk" regardless of category. The bands
  ($100, $10, anything less) are tunable in `reconcile.Options`.
* **Failing loud on schema drift.** Loaders reject unknown statuses, missing
  CSV columns, and bad timestamps with a wrapped error pointing at the line
  number. Silently mapping unknown states to a default is the kind of bug
  this whole tool exists to catch — the loader is not the place to introduce
  it.
* **Pure functions in the engine.** The engine takes slices and returns a
  `Report`; it does no I/O and holds no state across calls. This is what makes
  the engine test cover all five discrepancy types in one ~50-line table.

## Testing

```bash
make test
```

Highest-value coverage lives in
[`internal/reconcile/engine_test.go`](internal/reconcile/engine_test.go), which
verifies all five discrepancy types in one scenario, plus the amount
tolerance, fuzzy-match rescue, fuzzy-window cutoff, and severity ranking.
Source loaders are smoke-tested in
[`internal/sources/sources_test.go`](internal/sources/sources_test.go) and the
`Money` helpers in [`internal/domain/money_test.go`](internal/domain/money_test.go).

## Performance

On an M-series MacBook, `gendata -n 47000 && reconcile ...` produces a full
reconciliation in roughly 250 ms (the engine alone is ~30 ms — most of the
time is JSON/CSV parsing). The brief's "ideal" target is 10 seconds.

## Scaling beyond memory

The in-memory design is a deliberate fit for the brief's 47K – 500K range.
Beyond that, a few discrete steps:

1. **Stream loaders.** Replace `[]domain.Transaction` with a `chan` (or an
   iterator) so each source streams from disk. CSV/JSON can both be parsed
   row-by-row.
2. **Partition by reference prefix.** Hash `ExternalRef` into N shards and
   reconcile each shard independently. The exact-match pass is embarrassingly
   parallel because no order can match a processor record in a different
   shard.
3. **External sort for fuzzy matching.** Sort each shard's leftovers by
   `(currency, timestamp)` to disk, then merge-walk: for any internal leftover
   you only need the small window of processor leftovers nearby in time.
   Memory stays O(window) per shard.
4. **Push state into a database.** At ~10M records the right move is to load
   each source into a temporary table (Postgres or DuckDB), index on
   `external_ref`, and let SQL do the joins. The matching logic above maps
   cleanly to `FULL OUTER JOIN`s. The Go service then becomes a job
   orchestrator that emits the same `domain.Report` shape — the report
   contract and the CLI/UX don't change.
5. **Idempotent re-runs.** Tag each report with the input file checksums and
   write findings to a `discrepancies` table keyed by
   `(external_ref, discrepancy_type, run_id)`. Operators can diff yesterday's
   findings against today's without re-mailing the full report.

The matching engine itself ports unchanged into any of these — the only thing
that grows is the I/O layer around it.

## Repository layout

```
cmd/reconcile/      # CLI: load sources, run engine, write report
cmd/gendata/        # CLI: generate realistic test fixtures
internal/domain/    # Canonical Transaction, Money, Discrepancy, Report
internal/sources/   # Per-source loaders (JSON, two CSV variants)
internal/reconcile/ # Matching engine + fuzzy second pass
internal/report/    # JSON, CSV, console writers
testdata/           # Committed fixtures generated by `make gendata`
```
