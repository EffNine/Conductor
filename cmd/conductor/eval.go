// The `conductor eval` subcommand runs the Phase C2 offline JEV shadow
// evaluation. It reads shadow observations (from a snapshot JSON or the
// live SQLite DB, read-only) and writes a machine-readable JSON result and a
// human-readable text report. It never mutates the observation store, never
// influences routing, and never calls the JEV service.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/EffNine/conductor/internal/jev/shadow/eval"
	_ "github.com/mattn/go-sqlite3"
)

// evalFlags are the flags understood by `conductor eval`.
type evalFlags struct {
	dbPath       string
	snapshot     string
	outJSON      string
	outText      string
	dumpSnapshot string
	bucketEdges  string
}

// runEval executes the offline evaluation and returns the exit code.
func runEval(args []string) int {
	flags := parseEvalFlags(args)

	var records []eval.Observation
	var source string
	if flags.snapshot != "" {
		recs, err := eval.LoadSnapshotJSON(flags.snapshot)
		if err != nil {
			fmt.Fprintf(os.Stderr, "eval: %v\n", err)
			return 1
		}
		records = recs
		source = flags.snapshot
	} else {
		if flags.dbPath == "" {
			flags.dbPath = "./data/conductor.db"
		}
		recs, err := loadShadowRows(flags.dbPath)
		if err != nil {
			// A missing file, a missing table, or an empty table all mean
			// "no observations yet". C2 still produces a valid result
			// (INSUFFICIENT_EVIDENCE); only genuine read errors fail.
			if isNoShadowData(err) {
				fmt.Fprintf(os.Stderr, "eval: no shadow observations found at %s (no usable data)\n", flags.dbPath)
				records = nil
				source = flags.dbPath
			} else {
				fmt.Fprintf(os.Stderr, "eval: load observations: %v\n", err)
				return 1
			}
		} else {
			records = recs
			source = flags.dbPath
		}
	}

	// Optionally dump the records to a snapshot for reproducibility.
	if flags.dumpSnapshot != "" {
		if err := eval.WriteSnapshotJSON(flags.dumpSnapshot, source, records); err != nil {
			fmt.Fprintf(os.Stderr, "eval: dump snapshot: %v\n", err)
			return 1
		}
	}

	cfg := eval.DefaultConfig()
	if flags.bucketEdges != "" {
		edges, err := parseEdges(flags.bucketEdges)
		if err != nil {
			fmt.Fprintf(os.Stderr, "eval: bad --bucket-edges: %v\n", err)
			return 1
		}
		cfg.BucketEdges = edges
	}

	ev := eval.NewEvaluator(cfg, nil)
	res := ev.Evaluate(records)

	// Write outputs with 0600 so evaluation artifacts are not world-
	// readable (they may reflect production routing data).
	if flags.outJSON != "" {
		js, err := res.JSON()
		if err != nil {
			fmt.Fprintf(os.Stderr, "eval: %v\n", err)
			return 1
		}
		if err := os.WriteFile(flags.outJSON, []byte(js), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "eval: write json: %v\n", err)
			return 1
		}
	}
	if flags.outText != "" {
		if err := os.WriteFile(flags.outText, []byte(res.Text()), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "eval: write text: %v\n", err)
			return 1
		}
	}

	// Default: print both to stdout.
	if flags.outJSON == "" && flags.outText == "" {
		js, _ := res.JSON()
		fmt.Println(js)
		fmt.Println(strings.Repeat("=", 72))
		fmt.Println(res.Text())
	}
	// Also print a one-line summary to stderr so the outcome is visible.
	fmt.Fprintf(os.Stderr, "eval: verdict=%s total=%d complete=%d source=%s\n",
		res.Verdict, res.Dataset.Total, res.Dataset.Complete, source)
	return 0
}

// loadShadowRows opens the SQLite DB read-only and reads every
// shadow_observations row into eval observations.
func loadShadowRows(dbPath string) ([]eval.Observation, error) {
	// mode=ro guarantees the DB (and its WAL) is never written by the
	// evaluator. If the file does not exist, Open succeeds lazily and the
	// first query fails with "unable to open database file" or the table
	// check fails with "no such table" — both are no-data conditions.
	db, openErr := sql.Open("sqlite3", dbPath+"?mode=ro&foreign_keys=off")
	if openErr != nil {
		return nil, fmt.Errorf("open db: %w", openErr)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Check whether the table exists.
	var exists int
	if qErr := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='shadow_observations'").Scan(&exists); qErr != nil {
		return nil, qErr
	}
	if exists == 0 {
		return nil, errNoShadowTable
	}

	rows, qErr := db.QueryContext(ctx, `
		SELECT decision_id, resolved_mode,
		       COALESCE(selected_provider,''), COALESCE(selected_model,''),
		       candidate_count, COALESCE(candidate_providers,''),
		       COALESCE(advisory_provider,''),
		       advisory_confidence, COALESCE(advisory_probabilities,''),
		       COALESCE(advisory_status,''), COALESCE(outcome,''),
		       provider_latency_ms
		FROM shadow_observations
		ORDER BY id ASC
	`)
	if qErr != nil {
		return nil, qErr
	}
	defer rows.Close()

	out := make([]eval.Observation, 0, 256)
	for rows.Next() {
		var o eval.Observation
		var conf *float64 // nullable REAL; nil when no confidence recorded.
		if scanErr := rows.Scan(
			&o.DecisionID, &o.ResolvedMode,
			&o.SelectedProvider, &o.SelectedModel,
			&o.CandidateCount, &o.CandidateProviders,
			&o.AdvisoryProvider,
			&conf, &o.AdvisoryProbabilities,
			&o.AdvisoryStatus, &o.Outcome,
			&o.ProviderLatencyMs,
		); scanErr != nil {
			return nil, scanErr
		}
		o.AdvisoryConfidence = conf
		out = append(out, o)
	}
	if rowErr := rows.Err(); rowErr != nil {
		return nil, rowErr
	}
	return out, nil
}

// errNoShadowTable signals "table not present" so the caller can report a
// clean zero-observation result instead of a hard error.
var errNoShadowTable = fmt.Errorf("eval: shadow_observations table not present")

// isNoShadowData reports whether err is one of the expected "no data"
// conditions: file not found, table missing, or empty table.
func isNoShadowData(err error) bool {
	if err == nil {
		return false
	}
	if err == errNoShadowTable {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "no such file") ||
		strings.Contains(msg, "file does not exist") ||
		strings.Contains(msg, "no such table") ||
		strings.Contains(msg, "unable to open database file")
}

// parseEdges parses a comma-separated list of bucket boundaries.
func parseEdges(s string) ([]float64, error) {
	parts := strings.Split(s, ",")
	out := make([]float64, 0, len(parts))
	for _, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

func parseEvalFlags(args []string) evalFlags {
	var f evalFlags
	next := func(i *int, name string) string {
		if *i+1 < len(args) {
			*i++
			return args[*i]
		}
		fmt.Fprintf(os.Stderr, "eval: %s requires a value\n", name)
		return ""
	}
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--db":
			f.dbPath = next(&i, a)
		case "--snapshot":
			f.snapshot = next(&i, a)
		case "--out-json":
			f.outJSON = next(&i, a)
		case "--out-text":
			f.outText = next(&i, a)
		case "--dump-snapshot":
			f.dumpSnapshot = next(&i, a)
		case "--bucket-edges":
			f.bucketEdges = next(&i, a)
		default:
			fmt.Fprintf(os.Stderr, "eval: unknown flag %q\n", a)
		}
	}
	return f
}
