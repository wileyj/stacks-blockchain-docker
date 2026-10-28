package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// Deferred indexes: some API indexes take hours to rebuild from a dump (on mainnet, the hash index
// nft_events_asset_identifier_index: three NFT identifiers hold about 70% of its rows, refactor/15
// "Phase 3c"). `--defer-index NAME` restores everything else, records the index definition in
// sbd_seed.deferred_indexes, and the `build-indexes` job builds it with CREATE INDEX CONCURRENTLY
// once the stack runs. Until then the API works, but queries that need the index are slow.

const deferredSchema = "sbd_seed"

var tocIndexRe = regexp.MustCompile(`^\d+; \d+ \d+ INDEX (\S+) (\S+) `)

// FilterTOC removes the named indexes from a `pg_restore -l` listing. It returns the filtered list and
// the removed lines, and fails if a name isn't an index in the dump (a typo must not skip nothing).
func FilterTOC(toc string, names []string) (kept string, removed []string, err error) {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	found := map[string]bool{}
	var out strings.Builder
	for _, line := range strings.SplitAfter(toc, "\n") {
		if m := tocIndexRe.FindStringSubmatch(line); m != nil && want[m[2]] {
			found[m[2]] = true
			removed = append(removed, strings.TrimRight(line, "\n"))
			continue
		}
		out.WriteString(line)
	}
	for _, n := range names {
		if !found[n] {
			return "", nil, refuse("--defer-index %s: no such index in the dump", n)
		}
	}
	return out.String(), removed, nil
}

var (
	createIndexRe = regexp.MustCompile(`(?s)CREATE (UNIQUE )?INDEX .*?;`)
	onSchemaRe    = regexp.MustCompile(`ON (ONLY )?` + regexp.QuoteMeta(dumpSchema) + `\.`)
)

// IndexDefinitions extracts the CREATE INDEX statements from `pg_restore -f -` SQL output and moves
// them to the schema the restored data ends up in (PG_SCHEMA).
func IndexDefinitions(sql, schema string) []string {
	var defs []string
	for _, stmt := range createIndexRe.FindAllString(sql, -1) {
		defs = append(defs, onSchemaRe.ReplaceAllString(stmt, "ON ${1}"+quoteIdent(schema)+"."))
	}
	return defs
}

// Concurrently turns "CREATE [UNIQUE] INDEX name ON ..." into its CONCURRENTLY form.
func Concurrently(def string) string {
	return regexp.MustCompile(`^CREATE (UNIQUE )?INDEX `).ReplaceAllString(def, "CREATE ${1}INDEX CONCURRENTLY ")
}

// recordDeferredSQL stores the deferred definitions in the restored database.
func recordDeferredSQL(names, defs []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "CREATE SCHEMA IF NOT EXISTS %s;\n", deferredSchema)
	fmt.Fprintf(&b, "CREATE TABLE IF NOT EXISTS %s.deferred_indexes (name text PRIMARY KEY, definition text NOT NULL, added timestamptz NOT NULL DEFAULT now());\n", deferredSchema)
	for i := range names {
		fmt.Fprintf(&b, "INSERT INTO %s.deferred_indexes (name, definition) VALUES ($sbd$%s$sbd$, $sbd$%s$sbd$) ON CONFLICT (name) DO UPDATE SET definition = EXCLUDED.definition;\n",
			deferredSchema, names[i], defs[i])
	}
	return b.String()
}

// runBuildIndexes is the `build-indexes` job: it runs with the stack (api profile), builds whatever
// the seed deferred, and exits at once when nothing is pending.
func runBuildIndexes(ctx context.Context, args []string, getenv func(string) string) error {
	fs := flag.NewFlagSet("build-indexes", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return exitError{2, err.Error()}
	}
	// Each statement is its own -c: one session (SETs persist), but each runs in its own transaction,
	// which CREATE/DROP INDEX CONCURRENTLY requires
	psql := func(sqls ...string) (string, error) {
		args := []string{"-h", orDefault(getenv("PG_HOST"), "postgres"), "-p", orDefault(getenv("PG_PORT"), "5432"),
			"-U", orDefault(getenv("PG_USER"), "postgres"), "-d", orDefault(getenv("PG_DATABASE"), "postgres"),
			"-v", "ON_ERROR_STOP=1", "-AtX"}
		for _, sql := range sqls {
			args = append(args, "-c", sql)
		}
		cmd := exec.CommandContext(ctx, "psql", args...)
		cmd.Env = append(os.Environ(), "PGPASSWORD="+orDefault(getenv("PG_PASSWORD"), "postgres"))
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("%s: %w: %s", sqls[len(sqls)-1], err, strings.TrimSpace(string(out)))
		}
		return strings.TrimSpace(string(out)), nil
	}
	pending, err := psql(fmt.Sprintf("SELECT to_regclass('%s.deferred_indexes') IS NOT NULL", deferredSchema))
	if err != nil {
		return err
	}
	if pending != "t" {
		fmt.Println("build-indexes: nothing deferred")
		return nil
	}
	rows, err := psql(fmt.Sprintf("SELECT name || E'\\t' || definition FROM %s.deferred_indexes ORDER BY added", deferredSchema))
	if err != nil {
		return err
	}
	for _, row := range strings.Split(rows, "\n") {
		name, def, ok := strings.Cut(row, "\t")
		if !ok {
			continue
		}
		// An interrupted CONCURRENTLY build leaves an INVALID index that is maintained but never used
		invalid, err := psql(fmt.Sprintf("SELECT count(*) FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid WHERE c.relname = '%s' AND NOT i.indisvalid", name))
		if err != nil {
			return err
		}
		if invalid != "0" {
			fmt.Printf("build-indexes: dropping the invalid %s left by an interrupted build\n", name)
			target := onIndexName(def)
			if target == "" {
				target = name
			}
			if _, err := psql("DROP INDEX CONCURRENTLY IF EXISTS " + target); err != nil {
				return err
			}
		}
		fmt.Printf("build-indexes: building %s (CONCURRENTLY; the API keeps running)\n", name)
		if _, err := psql("SET statement_timeout = 0", "SET maintenance_work_mem = '1GB'", Concurrently(def)); err != nil {
			return err
		}
		if _, err := psql(fmt.Sprintf("DELETE FROM %s.deferred_indexes WHERE name = $sbd$%s$sbd$", deferredSchema, name)); err != nil {
			return err
		}
		fmt.Printf("build-indexes: %s done\n", name)
	}
	if _, err := psql(fmt.Sprintf("DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM %[1]s.deferred_indexes) THEN DROP SCHEMA %[1]s CASCADE; END IF; END $$", deferredSchema)); err != nil {
		return err
	}
	return nil
}

// onIndexName returns the schema-qualified index name for DROP INDEX: the index lives in the same
// schema as its table ("CREATE INDEX name ON schema.table ...").
func onIndexName(def string) string {
	m := regexp.MustCompile(`^CREATE (?:UNIQUE )?INDEX (\S+) ON (?:ONLY )?("?[^".]+"?)\.`).FindStringSubmatch(def)
	if m == nil {
		return ""
	}
	return m[2] + "." + m[1]
}
