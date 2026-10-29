package main

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
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
		{name: "krypton/peer.sqlite", body: "peers", typ: tar.TypeReg},
		{name: "krypton/peer.sqlite-wal", body: "peers", typ: tar.TypeReg},
		{name: "event_observers.sqlite", body: "pending", typ: tar.TypeReg},
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
	if err := ExtractArchive(t.Context(), archive, staging, owner, &Progress{Total: 1}); err != nil {
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
	for _, gone := range []string{"mainnet-old", StagingName, "krypton/peer.sqlite", "krypton/peer.sqlite-wal", "event_observers.sqlite"} {
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

// GNU tar strips or refuses dangerous members; whatever it does, nothing may land outside dest.
func TestExtractKeepsEverythingInsideDest(t *testing.T) {
	for _, e := range []entry{
		{name: "../escape", body: "x", typ: tar.TypeReg},
		{name: "a/../../escape", body: "x", typ: tar.TypeReg},
		{name: "/escape-abs", body: "x", typ: tar.TypeReg},
		{name: "a/evil", link: "../..", typ: tar.TypeSymlink},
		{name: "hard", link: "../escape", typ: tar.TypeLink},
	} {
		tmp := t.TempDir()
		archive := filepath.Join(tmp, "bad.tar.zst")
		entries := []entry{{name: "a/", typ: tar.TypeDir}, e}
		if e.typ == tar.TypeSymlink {
			entries = append(entries, entry{name: "a/evil/escape", body: "x", typ: tar.TypeReg})
		}
		writeTarZst(t, archive, entries)
		dest := filepath.Join(tmp, "dest")
		os.Mkdir(dest, 0o755)
		_ = ExtractArchive(t.Context(), archive, dest, me(t, dest), &Progress{})
		for _, outside := range []string{filepath.Join(tmp, "escape"), "/escape-abs"} {
			if _, err := os.Lstat(outside); err == nil {
				t.Errorf("%s -> %s: %s was written outside the destination", e.name, e.link, outside)
			}
		}
	}
}

func TestExtractOtherFormats(t *testing.T) {
	entries := []entry{{name: "krypton/", typ: tar.TypeDir}, {name: "krypton/headers.sqlite", body: "headers", typ: tar.TypeReg}}
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: 0o755, Size: int64(len(e.body))}
		tw.WriteHeader(h)
		tw.Write([]byte(e.body))
	}
	tw.Close()
	for ext, compress := range map[string]func([]byte) []byte{
		".tar": func(b []byte) []byte { return b },
		".tar.gz": func(b []byte) []byte {
			var out bytes.Buffer
			w := gzip.NewWriter(&out)
			w.Write(b)
			w.Close()
			return out.Bytes()
		},
		".tar.xz": func(b []byte) []byte {
			var out bytes.Buffer
			w, _ := xz.NewWriter(&out)
			w.Write(b)
			w.Close()
			return out.Bytes()
		},
	} {
		tmp := t.TempDir()
		archive := filepath.Join(tmp, "chain"+ext)
		os.WriteFile(archive, compress(tarBuf.Bytes()), 0o644)
		dest := filepath.Join(tmp, "dest")
		os.Mkdir(dest, 0o755)
		if err := ExtractArchive(t.Context(), archive, dest, me(t, dest), &Progress{}); err != nil {
			t.Fatalf("%s: %v", ext, err)
		}
		if b, _ := os.ReadFile(filepath.Join(dest, "krypton", "headers.sqlite")); string(b) != "headers" {
			t.Errorf("%s: content not extracted", ext)
		}
	}
	bogus := filepath.Join(t.TempDir(), "chain.tar.zst")
	os.WriteFile(bogus, []byte("not an archive"), 0o644)
	if err := ExtractArchive(t.Context(), bogus, t.TempDir(), Owner{}, &Progress{}); err == nil {
		t.Errorf("a file that isn't an archive must fail")
	}
}

func TestArchiveBase(t *testing.T) {
	for in, want := range map[string]string{
		"/d/mainnet-stacks-blockchain-4.0.4-20261015.tar.zst": "/d/mainnet-stacks-blockchain-4.0.4-20261015",
		"/d/mainnet-stacks-blockchain-4.0.4-20261015.tar.xz":  "/d/mainnet-stacks-blockchain-4.0.4-20261015",
		"/d/stacks-blockchain-api-pg-17-9.7.0-20261015.dump":  "/d/stacks-blockchain-api-pg-17-9.7.0-20261015",
	} {
		if got := ArchiveBase(in); got != want {
			t.Errorf("ArchiveBase(%s) = %s", in, got)
		}
	}
}

func TestAllowTraverse(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0o700)
	restore, err := allowTraverse(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o701 {
		t.Errorf("during the restore: mode %o, want 701", fi.Mode().Perm())
	}
	restore()
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Errorf("after: mode %o, want 700", fi.Mode().Perm())
	}
}

func TestBothCancelsTheOtherOnFailure(t *testing.T) {
	boom := errors.New("restore failed")
	err := both(t.Context(),
		func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, // a long extraction
		func(context.Context) error { return boom })
	if !errors.Is(err, boom) {
		t.Errorf("got %v, want the first failure", err)
	}
	if err := both(t.Context(), func(context.Context) error { return nil }, func(context.Context) error { return nil }); err != nil {
		t.Errorf("both succeeded, got %v", err)
	}
}
