package main

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

type entry struct {
	name, body, link string
	typ              byte
}

func writeTarZst(t *testing.T, path string, entries []entry) {
	t.Helper()
	var buf bytes.Buffer
	zw, _ := zstd.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: 0o644, Uid: 1001, Gid: 1001, Linkname: e.link}
		if e.typ == tar.TypeDir {
			h.Mode = 0o755
		}
		if e.typ == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.typ == tar.TypeReg {
			tw.Write([]byte(e.body))
		}
	}
	tw.Close()
	zw.Close()
	os.WriteFile(path, buf.Bytes(), 0o644)
}

func me(t *testing.T, dir string) Owner {
	o, err := OwnerOf(dir)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestExtractStagingAndSwap(t *testing.T) {
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "chain.tar.zst")
	writeTarZst(t, archive, []entry{
		{name: "krypton/", typ: tar.TypeDir},
		{name: "krypton/headers.sqlite", body: "headers", typ: tar.TypeReg},
		{name: "krypton/chainstate/vm/index.sqlite", body: "index", typ: tar.TypeReg},
		{name: "krypton/current", link: "chainstate", typ: tar.TypeSymlink},
	})
	data := filepath.Join(tmp, "stacks-blockchain") // stands in for a mount point
	os.MkdirAll(filepath.Join(data, "mainnet-old"), 0o755)
	os.WriteFile(filepath.Join(data, "mainnet-old", "stale"), []byte("x"), 0o644)
	os.Mkdir(filepath.Join(data, StagingName), 0o755) // left by a failed earlier run
	os.WriteFile(filepath.Join(data, StagingName, "junk"), []byte("x"), 0o644)
	dataInfo, _ := os.Stat(data)

	owner := me(t, data)
	staging, err := PrepareStaging(data, owner, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(staging, "junk")); err == nil {
		t.Fatalf("an old staging dir must be cleared")
	}
	if err := ExtractTarZst(archive, staging, owner, &Progress{}); err != nil {
		t.Fatal(err)
	}
	// The old data is untouched until the swap
	if _, err := os.Stat(filepath.Join(data, "mainnet-old", "stale")); err != nil {
		t.Fatalf("old data must survive until the swap: %v", err)
	}
	if err := SwapIn(data); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(data, "krypton", "chainstate", "vm", "index.sqlite")); string(b) != "index" {
		t.Errorf("extracted file missing after swap")
	}
	if l, _ := os.Readlink(filepath.Join(data, "krypton", "current")); l != "chainstate" {
		t.Errorf("symlink not restored: %q", l)
	}
	for _, gone := range []string{"mainnet-old", StagingName} {
		if _, err := os.Stat(filepath.Join(data, gone)); err == nil {
			t.Errorf("%s must be gone after the swap", gone)
		}
	}
	after, _ := os.Stat(data)
	if !os.SameFile(dataInfo, after) {
		t.Errorf("the data dir itself must be kept (mount point), not replaced")
	}
	if o := me(t, filepath.Join(data, "krypton", "headers.sqlite")); o != owner {
		t.Errorf("files must get the data dir's owner %v, got %v", owner, o)
	}
}

func TestExtractRefusesPathTraversal(t *testing.T) {
	for _, e := range []entry{
		{name: "../escape", body: "x", typ: tar.TypeReg},
		{name: "a/../../escape", body: "x", typ: tar.TypeReg},
		{name: "evil", link: "/etc/passwd", typ: tar.TypeSymlink},
		{name: "a/evil", link: "../../outside", typ: tar.TypeSymlink},
		{name: "hard", link: "../outside", typ: tar.TypeLink},
	} {
		tmp := t.TempDir()
		archive := filepath.Join(tmp, "bad.tar.zst")
		writeTarZst(t, archive, []entry{{name: "a/", typ: tar.TypeDir}, e})
		dest := filepath.Join(tmp, "dest")
		os.Mkdir(dest, 0o755)
		err := ExtractTarZst(archive, dest, me(t, dest), &Progress{})
		if err == nil || !strings.Contains(err.Error(), "outside the archive root") {
			t.Errorf("%s -> %s: want refusal, got %v", e.name, e.link, err)
		}
		if _, statErr := os.Lstat(filepath.Join(tmp, "escape")); statErr == nil {
			t.Errorf("%s escaped the destination", e.name)
		}
	}
}
