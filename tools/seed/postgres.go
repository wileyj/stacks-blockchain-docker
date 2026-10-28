package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// PG describes the API database the dump is restored into (from .env, as the API uses it).
type PG struct {
	User, Password, Database, Schema string
	Jobs                             int
}

const (
	pgUID      = 70 // the postgres user in the postgres:*-alpine image this job is built from
	pgSocket   = "/tmp/seed-pg"
	dumpSchema = "stacks_blockchain_api" // database and schema name inside the Hiro dumps
)

// RestoreDump builds a postgres data dir in pgdata (the staging dir) from a custom-format dump:
// initdb with the .env user/password, a socket-only server tuned for bulk loading, pg_restore -j,
// then the database/schema renames the API expects (same as the old seed-chainstate.sh). The server
// is stopped cleanly and the data flushed before returning.
func RestoreDump(ctx context.Context, dump, pgdata string, pg PG) error {
	if err := os.Chown(pgdata, pgUID, pgUID); err != nil {
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
	if err := os.Chown(pwfile, pgUID, pgUID); err != nil {
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
	if err := os.Chown(pgSocket, pgUID, pgUID); err != nil {
		return err
	}
	// Bulk-load settings: safe because a failed restore's staging dir is thrown away, and the data
	// is flushed (clean shutdown + sync) before the swap.
	server := exec.CommandContext(ctx, "gosu", "postgres", "postgres", "-D", pgdata,
		"-c", "listen_addresses=", "-c", "unix_socket_directories="+pgSocket,
		"-c", "fsync=off", "-c", "full_page_writes=off", "-c", "synchronous_commit=off",
		"-c", "max_wal_size=16GB", "-c", "maintenance_work_mem=1GB", "-c", "autovacuum=off")
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

	fmt.Printf("seed: pg_restore -j %d (this takes a long time for mainnet)\n", pg.Jobs)
	if err := run(ctx, "pg_restore", "-h", pgSocket, "-U", pg.User, "--no-owner", "--no-privileges",
		"--create", "--dbname=postgres", fmt.Sprintf("--jobs=%d", pg.Jobs), dump); err != nil {
		return fmt.Errorf("pg_restore: %w", err)
	}
	for _, sql := range renameSQL(pg) {
		if err := run(ctx, "psql", "-h", pgSocket, "-U", pg.User, "-v", "ON_ERROR_STOP=1", "-d", sql[0], "-c", sql[1]); err != nil {
			return fmt.Errorf("%s: %w", sql[1], err)
		}
	}
	return stop()
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
