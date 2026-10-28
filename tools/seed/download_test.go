package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// archiveServer serves one file with Range support, its .sha256, and can drop a connection once.
func archiveServer(t *testing.T, data []byte, dropAfter int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	sum := sha256.Sum256(data)
	var requests atomic.Int32
	dropped := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if strings.HasSuffix(r.URL.Path, ".sha256") {
			fmt.Fprintf(w, "%s  /data/file.tar.zst\n", hex.EncodeToString(sum[:]))
			return
		}
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusForbidden) // like the pre-signed archive URLs
			return
		}
		if strings.HasSuffix(r.URL.Path, "/missing") {
			http.NotFound(w, r)
			return
		}
		if dropAfter > 0 && !dropped && r.Header.Get("Range") == "" {
			// First full GET: send part of the body, then cut the connection
			dropped = true
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			w.WriteHeader(http.StatusOK)
			w.Write(data[:dropAfter])
			if hj, ok := w.(http.Hijacker); ok {
				conn, _, _ := hj.Hijack()
				conn.Close()
			}
			return
		}
		http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(data))
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

func testData(n int) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(1)).Read(b)
	return b
}

func downloader() *Downloader {
	return &Downloader{Client: http.DefaultClient, Attempts: 3, Backoff: 10 * time.Millisecond}
}

func TestFetchHashesWhileDownloading(t *testing.T) {
	data := testData(1 << 20)
	srv, _ := archiveServer(t, data, 0)
	d := downloader()
	ctx := context.Background()
	size, err := d.RemoteSize(ctx, srv.URL+"/file.tar.zst")
	if err != nil || size != int64(len(data)) {
		t.Fatalf("RemoteSize = %d, %v", size, err)
	}
	want, err := d.Checksum(ctx, srv.URL+"/file.sha256")
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "file.tar.zst")
	got, err := d.Fetch(ctx, srv.URL+"/file.tar.zst", dest, &Progress{Name: "file", Total: size})
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("hash %s, want %s", got, want)
	}
	if b, _ := os.ReadFile(dest); !bytes.Equal(b, data) {
		t.Errorf("file content differs")
	}
}

func TestFetchResumesAfterDroppedConnection(t *testing.T) {
	data := testData(3 << 20)
	srv, requests := archiveServer(t, data, 1<<20)
	d := downloader()
	dest := filepath.Join(t.TempDir(), "file.tar.zst")
	got, err := d.Fetch(context.Background(), srv.URL+"/file.tar.zst", dest, &Progress{Name: "file", Total: int64(len(data))})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if got != hex.EncodeToString(sum[:]) {
		t.Errorf("resumed hash differs")
	}
	if requests.Load() != 2 {
		t.Errorf("want 1 dropped + 1 ranged request, got %d", requests.Load())
	}
}

func TestFetchResumesPartialFileFromEarlierRun(t *testing.T) {
	data := testData(2 << 20)
	srv, _ := archiveServer(t, data, 0)
	dest := filepath.Join(t.TempDir(), "file.tar.zst")
	os.WriteFile(dest, data[:777777], 0o644)
	got, err := downloader().Fetch(context.Background(), srv.URL+"/file.tar.zst", dest, &Progress{Name: "file", Total: int64(len(data))})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if got != hex.EncodeToString(sum[:]) {
		t.Errorf("hash of a resumed partial file differs")
	}
}

func TestFetchCompleteFileIsNotDownloadedAgain(t *testing.T) {
	data := testData(1 << 20)
	srv, requests := archiveServer(t, data, 0)
	dest := filepath.Join(t.TempDir(), "file.tar.zst")
	os.WriteFile(dest, data, 0o644)
	if _, err := downloader().Fetch(context.Background(), srv.URL+"/file.tar.zst", dest, &Progress{Name: "file", Total: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 {
		t.Errorf("a complete local file must not be fetched, got %d requests", requests.Load())
	}
}

func TestFetchMissingFileFailsFast(t *testing.T) {
	srv, requests := archiveServer(t, testData(10), 0)
	_, err := downloader().Fetch(context.Background(), srv.URL+"/missing", filepath.Join(t.TempDir(), "x"), &Progress{Name: "x"})
	if err == nil || requests.Load() != 1 {
		t.Errorf("a 404 must fail without retries: err=%v requests=%d", err, requests.Load())
	}
}
