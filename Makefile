.PHONY: build test lint gendata reconcile demo clean

build:
	go build -o bin/reconcile ./cmd/reconcile
	go build -o bin/gendata ./cmd/gendata

test:
	go test ./... -count=1

lint:
	golangci-lint run ./...

# Generate ~1000 transactions with a realistic distribution of discrepancies
# into ./testdata/ (overwrites existing files).
gendata: build
	./bin/gendata -out testdata -n 1000 -seed 42

# Run reconciliation against the bundled test data and write reports to ./out/.
reconcile: build
	./bin/reconcile \
		-internal testdata/internal_orders.json \
		-processor-a testdata/processor_a_settlements.csv \
		-processor-b testdata/processor_b_settlements.csv \
		-out out

# Generate fresh data + run reconciliation end-to-end.
demo: gendata reconcile

clean:
	rm -rf bin out
