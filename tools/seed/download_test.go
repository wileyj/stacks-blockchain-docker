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
	size, ranges, err := d.RemoteSize(ctx, srv.URL+"/file.tar.zst")
	if err != nil || size != int64(len(data)) || !ranges {
		t.Fatalf("RemoteSize = %d, ranges=%v, %v", size, ranges, err)
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

func segmented() *Downloader {
	return &Downloader{Client: http.DefaultClient, Attempts: 3, Backoff: 10 * time.Millisecond, Connections: 4, ChunkSize: 100_000}
}

func TestSegmentedFetch(t *testing.T) {
	data := testData(1_234_567) // not a multiple of the chunk size
	srv, requests := archiveServer(t, data, 0)
	dest := filepath.Join(t.TempDir(), "file.tar.zst")
	got, err := segmented().Fetch(context.Background(), srv.URL+"/file.tar.zst", dest, &Progress{Name: "file", Total: int64(len(data))})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if got != hex.EncodeToString(sum[:]) {
		t.Errorf("segmented hash differs")
	}
	if b, _ := os.ReadFile(dest); !bytes.Equal(b, data) {
		t.Errorf("segmented content differs")
	}
	if requests.Load() != 13 {
		t.Errorf("want 13 chunk requests, got %d", requests.Load())
	}
	if _, err := os.Stat(dest + ".parts"); err == nil {
		t.Errorf("the parts record must be removed when complete")
	}
}

func TestSegmentedResumesFromPartsRecord(t *testing.T) {
	data := testData(1_000_000)
	srv, requests := archiveServer(t, data, 0)
	dest := filepath.Join(t.TempDir(), "file.tar.zst")
	// An earlier run finished chunks 0, 3 and 7 of 10 (the rest of the file is zeros)
	partial := make([]byte, len(data))
	parts := make([]byte, 10)
	for _, i := range []int{0, 3, 7} {
		copy(partial[i*100_000:(i+1)*100_000], data[i*100_000:(i+1)*100_000])
		parts[i] = 1
	}
	os.WriteFile(dest, partial, 0o644)
	os.WriteFile(dest+".parts", parts, 0o644)
	got, err := segmented().Fetch(context.Background(), srv.URL+"/file.tar.zst", dest, &Progress{Name: "file", Total: int64(len(data))})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if got != hex.EncodeToString(sum[:]) {
		t.Errorf("resumed segmented hash differs")
	}
	if requests.Load() != 7 {
		t.Errorf("only the 7 missing chunks may be fetched, got %d requests", requests.Load())
	}
}

func TestSegmentedContinuesSingleStreamPartial(t *testing.T) {
	data := testData(1_000_000)
	srv, requests := archiveServer(t, data, 0)
	dest := filepath.Join(t.TempDir(), "file.tar.zst")
	os.WriteFile(dest, data[:350_000], 0o644) // chunks 0-2 complete, chunk 3 partial
	got, err := segmented().Fetch(context.Background(), srv.URL+"/file.tar.zst", dest, &Progress{Name: "file", Total: int64(len(data))})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if got != hex.EncodeToString(sum[:]) || requests.Load() != 7 {
		t.Errorf("hash ok=%v, requests=%d (want 7)", got == hex.EncodeToString(sum[:]), requests.Load())
	}
}

func TestSegmentedRetriesAFailingChunk(t *testing.T) {
	data := testData(500_000)
	sum := sha256.Sum256(data)
	var failed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "bytes=200000-299999" && !failed.Swap(true) {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		http.ServeContent(w, r, "f", time.Time{}, bytes.NewReader(data))
	}))
	defer srv.Close()
	got, err := segmented().Fetch(context.Background(), srv.URL+"/f", filepath.Join(t.TempDir(), "f"), &Progress{Name: "f", Total: int64(len(data))})
	if err != nil || got != hex.EncodeToString(sum[:]) || !failed.Load() {
		t.Errorf("err=%v hashOK=%v failedOnce=%v", err, got == hex.EncodeToString(sum[:]), failed.Load())
	}
}

func TestSegmentedPermanentFailureStops(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusForbidden)
	}))
	defer srv.Close()
	_, err := segmented().Fetch(context.Background(), srv.URL+"/f", filepath.Join(t.TempDir(), "f"), &Progress{Name: "f", Total: 1_000_000})
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("a 403 must stop the download, got %v", err)
	}
}
