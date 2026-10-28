package main

import (
	"strings"
	"testing"
)

// A listing shaped like https://archive.hiro.so/mainnet/... on 2026-10-26 (refactor/15 archive inventory)
const mainnetChains = `
mainnet-stacks-blockchain-4.0.4-20261022.tar.zst mainnet-stacks-blockchain-4.0.4-20261022.sha256
mainnet-stacks-blockchain-4.0.4-20261023.tar.zst
mainnet-stacks-blockchain-4.0.4-20261024.tar.zst
mainnet-stacks-blockchain-4.0.4-20261026.tar.zst
mainnet-stacks-blockchain-latest.tar.zst
`
const mainnetDumps = `
stacks-blockchain-api-pg-17-9.6.0-20261022.dump
stacks-blockchain-api-pg-17-9.6.0-20261023.dump
stacks-blockchain-api-pg-17-9.7.0-20261024.dump
stacks-blockchain-api-pg-17-9.7.0-20261026.dump
stacks-blockchain-api-pg-17-latest.dump
`

func TestSelect(t *testing.T) {
	for _, tc := range []struct {
		name     string
		pins     Pins
		want     string // chosen snapshot, or "error: <substring>"
		warnings []string
	}{
		{"exact pins take the newest pair", Pins{"4.0.4", "9.7.0", 17}, "20261026: core 4.0.4, API 9.7.0, pg17", nil},
		{"older API pin falls back to the newest compatible date", Pins{"4.0.4", "9.6.0", 17}, "20261023: core 4.0.4, API 9.6.0, pg17", []string{"a newer snapshot exists (20261026"}},
		{"newer pins accept older archives with a warning", Pins{"4.0.9", "9.9.0", 18}, "20261026: core 4.0.4, API 9.7.0, pg17", []string{"older than the pinned versions"}},
		{"another core major is refused", Pins{"5.0.0", "9.7.0", 17}, "error: the archive is on core 4.x", nil},
		{"another API major is refused", Pins{"4.0.4", "10.0.0", 17}, "error: the archive is on API 9.x", nil},
		{"pg older than the dumps is refused", Pins{"4.0.4", "9.7.0", 16}, "error: the dumps need postgres 17", nil},
		{"pins older than every snapshot are refused", Pins{"4.0.3", "9.7.0", 17}, "error: the snapshots are newer than core 4.0.3", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sel, err := Select("mainnet", mainnetChains, mainnetDumps, tc.pins)
			if strings.HasPrefix(tc.want, "error: ") {
				if err == nil || !strings.Contains(err.Error(), strings.TrimPrefix(tc.want, "error: ")) {
					t.Fatalf("want error %q, got %v (chosen %v)", tc.want, err, sel.Chosen)
				}
				if sel.Latest == nil || sel.Latest.Date != "20261026" {
					t.Errorf("the error path must still name the latest snapshot, got %v", sel.Latest)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := sel.Chosen.String(); got != tc.want {
				t.Errorf("chose %s, want %s", got, tc.want)
			}
			if len(sel.Warnings) != len(tc.warnings) {
				t.Fatalf("warnings %q, want %d", sel.Warnings, len(tc.warnings))
			}
			for i, w := range tc.warnings {
				if !strings.Contains(sel.Warnings[i], w) {
					t.Errorf("warning %q lacks %q", sel.Warnings[i], w)
				}
			}
		})
	}
}

func TestSelectNeverMixesDates(t *testing.T) {
	// 20261025 has a chainstate but no dump, 20261024 a dump but no chainstate
	chains := "testnet-stacks-blockchain-4.0.1-20261025.tar.zst testnet-stacks-blockchain-4.0.1-20261023.tar.zst"
	dumps := "stacks-blockchain-api-pg-17-9.0.2-20261024.dump stacks-blockchain-api-pg-17-9.0.2-20261023.dump"
	sel, err := Select("testnet", chains, dumps, Pins{"4.0.4", "9.7.0", 17})
	if err != nil {
		t.Fatal(err)
	}
	if sel.Chosen.Date != "20261023" {
		t.Errorf("must pick the newest date with both files, got %s", sel.Chosen)
	}
	if sel.Chosen.ChainFile("testnet") != "testnet-stacks-blockchain-4.0.1-20261023.tar.zst" || sel.Chosen.DumpFile() != "stacks-blockchain-api-pg-17-9.0.2-20261023.dump" {
		t.Errorf("file names: %s %s", sel.Chosen.ChainFile("testnet"), sel.Chosen.DumpFile())
	}
}

func TestSelectIgnoresOtherNetworks(t *testing.T) {
	if _, err := Select("testnet", mainnetChains, mainnetDumps, Pins{"4.0.4", "9.7.0", 17}); err == nil {
		t.Errorf("mainnet archives must not satisfy a testnet seed")
	}
}

func TestVersionLE(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		le   bool
	}{
		{"4.0.4", "4.0.10", true}, {"4.0.10", "4.0.4", false}, {"4.0.4", "4.0.4", true},
		{"3.3.0.0.1", "3.3.0.0.2", true}, {"9.7.0", "9.6.0", false}, {"4.0", "4.0.0", true},
	} {
		if versionLE(tc.a, tc.b) != tc.le {
			t.Errorf("versionLE(%s, %s) != %v", tc.a, tc.b, tc.le)
		}
	}
}
