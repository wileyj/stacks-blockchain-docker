package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// PG describes the API database the dump is restored into (from .env, as the API uses it).
type PG struct {
	User, Password, Database, Schema string
	Jobs                             int
	MaintenanceMem                   string   // per index build; up to Jobs builds run at once
	Defer                            []string // indexes built after the stack starts (build-indexes job)
}

const (
	pgSocket   = "/tmp/seed-pg"
	dumpSchema = "stacks_blockchain_api" // database and schema name inside the Hiro dumps
)

// RestoreDump builds a postgres data dir in pgdata (the staging dir) from a custom-format dump:
// initdb with the .env user/password, a socket-only server tuned for bulk loading, pg_restore -j,
// then the database/schema renames the API expects (same as the old seed-chainstate.sh). The server
// is stopped cleanly and the data flushed before returning.
func RestoreDump(ctx context.Context, dump, pgdata string, pg PG) error {
	// The image's postgres user (999 in the Debian images, 70 in Alpine): whatever this image has
	u, err := user.Lookup("postgres")
	if err != nil {
		return fmt.Errorf("no postgres user in the image: %w", err)
	}
	pgUID, _ := strconv.Atoi(u.Uid)
	pgGID, _ := strconv.Atoi(u.Gid)
	if err := os.Chown(pgdata, pgUID, pgGID); err != nil {
		return err
	}
	if err := os.Chmod(pgdata, 0o700); err != nil {
		return err
	}
	pwfile := filepath.Join(os.TempDir(), "seed-pg-pw")
	if err := os.WriteFile(pwfile, []byte(pg.Password+"\n"), 0o600); err != nil {
		return err
	}
	defer os.Remove(pwfile)
	if err := os.Chown(pwfile, pgUID, pgGID); err != nil {
		return err
	}
	if err := run(ctx, "gosu", "postgres", "initdb", "-D", pgdata, "--username="+pg.User, "--pwfile="+pwfile, "--auth-local=trust", "--auth-host=scram-sha-256"); err != nil {
		return fmt.Errorf("initdb: %w", err)
	}
	// What the postgres image's entrypoint adds, so the API can connect over the compose network
	hba, err := os.OpenFile(filepath.Join(pgdata, "pg_hba.conf"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_, err = hba.WriteString("\nhost all all all scram-sha-256\n")
	hba.Close()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(pgSocket, 0o775); err != nil {
		return err
	}
	if err := os.Chown(pgSocket, pgUID, pgGID); err != nil {
		return err
	}
	// Bulk-load settings: safe because a failed restore's staging dir is thrown away, and the data
	// is flushed (clean shutdown + sync) before the swap.
	server := exec.CommandContext(ctx, "gosu", "postgres", "postgres", "-D", pgdata,
		"-c", "listen_addresses=", "-c", "unix_socket_directories="+pgSocket,
		"-c", "fsync=off", "-c", "full_page_writes=off", "-c", "synchronous_commit=off",
		"-c", "max_wal_size=16GB", "-c", "maintenance_work_mem="+pg.MaintenanceMem, "-c", "autovacuum=off",
		// One large index (nft_events) took hours on a single core on mainnet: let each build use workers
		"-c", "max_worker_processes=16", "-c", "max_parallel_workers=16", "-c", "max_parallel_maintenance_workers=4")
	server.Stdout, server.Stderr = os.Stdout, os.Stderr
	if err := server.Start(); err != nil {
		return err
	}
	stopped := false
	stop := func() error {
		if stopped {
			return nil
		}
		stopped = true
		if err := run(context.Background(), "gosu", "postgres", "pg_ctl", "-D", pgdata, "-m", "fast", "-w", "stop"); err != nil {
			server.Process.Kill()
			return err
		}
		server.Wait()
		syscall.Sync()
		return nil
	}
	defer stop()
	if err := waitReady(ctx, pg.User); err != nil {
		return err
	}

	restoreArgs := []string{"-h", pgSocket, "-U", pg.User, "--no-owner", "--no-privileges",
		"--create", "--dbname=postgres", fmt.Sprintf("--jobs=%d", pg.Jobs)}
	var deferredNames, deferredDefs []string
	if len(pg.Defer) > 0 {
		list, names, defs, err := deferIndexes(ctx, dump, pgdata, pg)
		if err != nil {
			return err
		}
		restoreArgs = append(restoreArgs, "--use-list="+list)
		deferredNames, deferredDefs = names, defs
		fmt.Printf("seed: deferring %s until the stack runs (build-indexes job)\n", strings.Join(names, ", "))
	}
	fmt.Printf("seed: pg_restore -j %d (this takes a long time for mainnet)\n", pg.Jobs)
	stopReport := make(chan struct{})
	go reportRestore(stopReport, pg.User, time.Minute)
	defer close(stopReport)
	if err := run(ctx, "pg_restore", append(restoreArgs, dump)...); err != nil {
		return fmt.Errorf("pg_restore: %w", err)
	}
	for _, sql := range renameSQL(pg) {
		if err := run(ctx, "psql", "-h", pgSocket, "-U", pg.User, "-v", "ON_ERROR_STOP=1", "-d", sql[0], "-c", sql[1]); err != nil {
			return fmt.Errorf("%s: %w", sql[1], err)
		}
	}
	if len(deferredNames) > 0 {
		if err := run(ctx, "psql", "-h", pgSocket, "-U", pg.User, "-v", "ON_ERROR_STOP=1", "-d", pg.Database, "-c", recordDeferredSQL(deferredNames, deferredDefs)); err != nil {
			return fmt.Errorf("recording the deferred indexes: %w", err)
		}
	}
	return stop()
}

// deferIndexes writes a restore list without the deferred indexes (into the staging dir, which is
// discarded on failure and cleaned on success) and returns the deferred names and definitions.
func deferIndexes(ctx context.Context, dump, pgdata string, pg PG) (list string, names, defs []string, err error) {
	toc, err := exec.CommandContext(ctx, "pg_restore", "-l", dump).Output()
	if err != nil {
		return "", nil, nil, fmt.Errorf("pg_restore -l: %w", err)
	}
	kept, removed, err := FilterTOC(string(toc), pg.Defer)
	if err != nil {
		return "", nil, nil, err
	}
	dir, err := os.MkdirTemp("", "seed-toc")
	if err != nil {
		return "", nil, nil, err
	}
	list = filepath.Join(dir, "restore.list")
	only := filepath.Join(dir, "deferred.list")
	if err := os.WriteFile(list, []byte(kept), 0o644); err != nil {
		return "", nil, nil, err
	}
	if err := os.WriteFile(only, []byte(strings.Join(removed, "\n")+"\n"), 0o644); err != nil {
		return "", nil, nil, err
	}
	sql, err := exec.CommandContext(ctx, "pg_restore", "--use-list="+only, "-f", "-", dump).Output()
	if err != nil {
		return "", nil, nil, fmt.Errorf("reading the deferred index definitions: %w", err)
	}
	defs = IndexDefinitions(string(sql), pg.Schema)
	for _, line := range removed {
		names = append(names, tocIndexRe.FindStringSubmatch(line)[2])
	}
	if len(defs) != len(names) {
		return "", nil, nil, fmt.Errorf("found %d definitions for %d deferred indexes", len(defs), len(names))
	}
	return list, names, defs, nil
}

// renameSQL returns (database, statement) pairs that move the restored stacks_blockchain_api
// database/schema to PG_DATABASE/PG_SCHEMA.
func renameSQL(pg PG) [][2]string {
	var out [][2]string
	if pg.Database != dumpSchema && pg.Schema != dumpSchema {
		out = append(out,
			[2]string{dumpSchema, "DROP SCHEMA IF EXISTS public"},
			[2]string{dumpSchema, fmt.Sprintf("ALTER SCHEMA %s RENAME TO %s", dumpSchema, quoteIdent(pg.Schema))})
	}
	if pg.Database != dumpSchema {
		out = append(out,
			[2]string{"template1", fmt.Sprintf("DROP DATABASE IF EXISTS %s", quoteIdent(pg.Database))},
			[2]string{"template1", fmt.Sprintf("ALTER DATABASE %s RENAME TO %s", dumpSchema, quoteIdent(pg.Database))})
	}
	return out
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// reportRestore prints the restored database's size and pg_restore's current steps until stop closes.
func reportRestore(stop <-chan struct{}, user string, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	start := time.Now()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			out, err := exec.Command("psql", "-h", pgSocket, "-U", user, "-d", "postgres", "-Atc",
				"select pg_size_pretty(coalesce(pg_database_size('"+dumpSchema+"'),0)), "+
					"coalesce(string_agg(DISTINCT substring(query from '^(COPY \\S+|CREATE (?:UNIQUE )?INDEX \\S+)'), ', '), '') "+
					"from pg_stat_activity where application_name = 'pg_restore' and state = 'active'").Output()
			if err != nil {
				continue
			}
			size, steps, _ := strings.Cut(strings.TrimSpace(string(out)), "|")
			fmt.Printf("seed:   restore %s: database %s; running: %s\n", time.Since(start).Truncate(time.Second), size, steps)
		}
	}
}

func waitReady(ctx context.Context, user string) error {
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if exec.CommandContext(ctx, "pg_isready", "-q", "-h", pgSocket, "-U", user).Run() == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("postgres didn't become ready within 2 minutes")
}

func run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
