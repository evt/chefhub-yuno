// Command reconcile reconciles ChefHub's internal orders database against
// one or more payment processor settlement reports, then writes a discrepancy
// report to disk and a summary to stdout.
//
// Usage:
//
//	reconcile -internal orders.json \
//	          -processor-a a.csv -processor-b b.csv \
//	          -out ./out
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/evt/chefhub/internal/domain"
	"github.com/evt/chefhub/internal/reconcile"
	"github.com/evt/chefhub/internal/report"
	"github.com/evt/chefhub/internal/sources"
)

func main() {
	var (
		internalPath = flag.String("internal", "", "path to internal orders JSON export")
		procAPath    = flag.String("processor-a", "", "path to Processor A settlement CSV")
		procBPath    = flag.String("processor-b", "", "path to Processor B settlement CSV")
		outDir       = flag.String("out", "out", "directory for JSON/CSV report files")
		noFuzzy      = flag.Bool("no-fuzzy", false, "disable fuzzy second-pass matching")
	)
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(context.Background(), logger, *internalPath, *procAPath, *procBPath, *outDir, !*noFuzzy); err != nil {
		logger.Error("reconciliation failed", "error", err)
		os.Exit(1)
	}
}

var errMissingInputFlag = errors.New("missing required input flag")

func run(
	ctx context.Context,
	logger *slog.Logger,
	internalPath, procAPath, procBPath, outDir string,
	fuzzy bool,
) error {
	if internalPath == "" || procAPath == "" || procBPath == "" {
		flag.Usage()
		return errMissingInputFlag
	}

	start := time.Now()
	logger.InfoContext(ctx, "loading sources",
		"internal", internalPath,
		"processor_a", procAPath,
		"processor_b", procBPath,
	)

	internalTxns, err := sources.NewInternalLoader().Load(ctx, internalPath)
	if err != nil {
		return fmt.Errorf("load internal: %w", err)
	}
	procATxns, err := sources.NewProcessorALoader().Load(ctx, procAPath)
	if err != nil {
		return fmt.Errorf("load processor A: %w", err)
	}
	procBTxns, err := sources.NewProcessorBLoader().Load(ctx, procBPath)
	if err != nil {
		return fmt.Errorf("load processor B: %w", err)
	}

	opts := reconcile.Defaults()
	opts.EnableFuzzyMatching = fuzzy
	engine := reconcile.NewEngine(opts)
	//nolint:exhaustive // SourceInternal is not a processor
	processors := map[domain.Source][]domain.Transaction{
		domain.SourceProcessorA: procATxns,
		domain.SourceProcessorB: procBTxns,
	}
	r := engine.Reconcile(ctx, internalTxns, processors)

	jsonPath := filepath.Join(outDir, "reconciliation.json")
	csvPath := filepath.Join(outDir, "discrepancies.csv")
	if werr := report.WriteJSON(r, jsonPath); werr != nil {
		return fmt.Errorf("write json: %w", werr)
	}
	if werr := report.WriteCSV(r, csvPath); werr != nil {
		return fmt.Errorf("write csv: %w", werr)
	}

	report.PrintSummary(os.Stdout, r)
	fmt.Fprintf(os.Stdout, "\nreports written:\n  %s\n  %s\n", jsonPath, csvPath)
	logger.InfoContext(ctx, "done", "elapsed", time.Since(start))
	return nil
}
