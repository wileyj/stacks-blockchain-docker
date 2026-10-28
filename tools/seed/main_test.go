package main

import (
	"os"
	"strings"
	"testing"
)

func TestLocalPairRules(t *testing.T) {
	pins := Pins{"4.0.4", "9.7.0", 17}
	for _, tc := range []struct {
		chain, dump, want string // want: "" = accepted, else a refusal substring
	}{
		{"mainnet-stacks-blockchain-4.0.4-20261015.tar.zst", "stacks-blockchain-api-pg-17-9.6.0-20261015.dump", "not found in the download dir"},
		{"mainnet-stacks-blockchain-4.0.4-20261015.tar.zst", "stacks-blockchain-api-pg-17-9.6.0-20261016.dump", "different dates"},
		{"testnet-stacks-blockchain-4.0.1-20261015.tar.zst", "stacks-blockchain-api-pg-17-9.0.2-20261015.dump", "is a testnet archive"},
		{"mainnet-stacks-blockchain-4.0.5-20261015.tar.zst", "stacks-blockchain-api-pg-17-9.7.0-20261015.dump", "not compatible"},
		{"chain.tar.zst", "dump.dump", "must keep their archive names"},
	} {
		_, _, err := localPair("mainnet", pins, options{chainFile: tc.chain, dumpFile: tc.dump})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s + %s: want %q, got %v", tc.chain, tc.dump, tc.want, err)
		}
		if ee, ok := err.(exitError); !ok || ee.code != 2 {
			t.Errorf("%s: a refusal must exit 2, got %#v", tc.chain, err)
		}
	}
}

func TestRenameSQL(t *testing.T) {
	got := renameSQL(PG{Database: "postgres", Schema: "public"})
	want := [][2]string{
		{"stacks_blockchain_api", "DROP SCHEMA IF EXISTS public"},
		{"stacks_blockchain_api", `ALTER SCHEMA stacks_blockchain_api RENAME TO "public"`},
		{"template1", `DROP DATABASE IF EXISTS "postgres"`},
		{"template1", `ALTER DATABASE stacks_blockchain_api RENAME TO "postgres"`},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("step %d: %v, want %v", i, got[i], want[i])
		}
	}
	if n := len(renameSQL(PG{Database: "stacks_blockchain_api", Schema: "stacks_blockchain_api"})); n != 0 {
		t.Errorf("keeping the dump's names needs no renames, got %d", n)
	}
	if q := quoteIdent(`a"b`); q != `"a""b"` {
		t.Errorf("identifier quoting: %s", q)
	}
}

func TestConfirmWithoutTTYRefuses(t *testing.T) {
	r, w, _ := os.Pipe()
	defer r.Close()
	defer w.Close()
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old }()
	err := confirm(false, "replace the mainnet data")
	if ee, ok := err.(exitError); !ok || ee.code != 2 || !strings.Contains(ee.msg, "--yes") {
		t.Errorf("no TTY and no --yes must refuse with exit 2 naming --yes, got %v", err)
	}
	if err := confirm(true, "replace the mainnet data"); err != nil {
		t.Errorf("--yes must not prompt: %v", err)
	}
}

func TestBadInputExits2(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"no network":   {},
		"mocknet":      {"NETWORK": "mocknet"},
		"no pins":      {"NETWORK": "mainnet"},
		"bad pg major": {"NETWORK": "mainnet", "POSTGRES_VERSION": "x", "STACKS_BLOCKCHAIN_VERSION": "4.0.4", "STACKS_BLOCKCHAIN_API_VERSION": "9.7.0"},
	} {
		err := runSeed(t.Context(), []string{"--dry-run"}, func(k string) string { return env[k] })
		if ee, ok := err.(exitError); !ok || ee.code != 2 {
			t.Errorf("%s: want exit 2, got %v", name, err)
		}
	}
	if err := runSeed(t.Context(), []string{"--chainstate", "x"}, func(string) string { return "" }); err == nil || !strings.Contains(err.Error(), "go together") {
		t.Errorf("--chainstate without --pgdump must be refused, got %v", err)
	}
}
