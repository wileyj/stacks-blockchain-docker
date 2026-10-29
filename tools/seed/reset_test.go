package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEmptyDirKeepsTheDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "postgres") // stands in for a mount point
	os.MkdirAll(filepath.Join(dir, "base", "16384"), 0o700)
	os.WriteFile(filepath.Join(dir, "PG_VERSION"), []byte("17"), 0o600)
	os.MkdirAll(filepath.Join(dir, StagingName, "x"), 0o700)
	before, _ := os.Stat(dir)
	if err := EmptyDir(dir); err != nil {
		t.Fatal(err)
	}
	if n, _ := entries(dir); n != 0 {
		t.Errorf("dir not emptied: %d entries left", n)
	}
	after, err := os.Stat(dir)
	if err != nil || !os.SameFile(before, after) {
		t.Errorf("the dir itself must be kept: %v", err)
	}
}

func TestResetRefusesBadNetwork(t *testing.T) {
	err := runReset(t.Context(), nil, func(string) string { return "" })
	if ee, ok := err.(exitError); !ok || ee.code != 2 {
		t.Errorf("want exit 2 for a missing NETWORK, got %v", err)
	}
}
