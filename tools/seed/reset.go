package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const signerDir = "/data/stacks-signer"

// runReset empties this network's data dirs (chainstate, API database, signer) so the next start
// syncs from scratch. Run as `docker compose run --rm reset [--yes]` with the stack stopped; it needs
// no sudo on the host, and it keeps the dirs themselves (they may be mount points, refactor/09 D48).
func runReset(ctx context.Context, args []string, getenv func(string) string) error {
	fs := flag.NewFlagSet("reset", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "don't ask before deleting (CONFIRM=yes does the same)")
	if err := fs.Parse(args); err != nil {
		return exitError{2, err.Error()}
	}
	network := getenv("NETWORK")
	if network != "mainnet" && network != "testnet" {
		return refuse("NETWORK=%q: set NETWORK in .env (mainnet or testnet)", network)
	}
	if err := guardNotRunning("reset"); err != nil {
		return err
	}
	var dirs []string
	for _, d := range []string{chainDir, pgDir, signerDir} {
		if n, err := entries(d); err == nil && n > 0 {
			dirs = append(dirs, d)
		}
	}
	if len(dirs) == 0 {
		fmt.Printf("reset: no %s data to delete\n", network)
		return nil
	}
	fmt.Printf("reset: this deletes the %s data in:\n", network)
	for _, d := range dirs {
		fmt.Printf("reset:   %s\n", strings.TrimPrefix(d, "/data/"))
	}
	if err := confirm(*yes || strings.EqualFold(getenv("CONFIRM"), "yes"), fmt.Sprintf("delete the %s data", network)); err != nil {
		return err
	}
	for _, d := range dirs {
		if err := EmptyDir(d); err != nil {
			return err
		}
	}
	fmt.Printf("reset: %s data deleted. The next start syncs from genesis, or seed first: docker compose run --rm seed\n", network)
	return nil
}

// EmptyDir removes everything inside dir but keeps dir itself (a mount point can't be removed).
func EmptyDir(dir string) error {
	list, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range list {
		errs = append(errs, os.RemoveAll(filepath.Join(dir, e.Name())))
	}
	return errors.Join(errs...)
}

func entries(dir string) (int, error) {
	list, err := os.ReadDir(dir)
	return len(list), err
}
