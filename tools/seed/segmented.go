package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// fetchSegmented downloads url with d.Connections parallel Range requests, chunk by chunk, and still
// hashes while downloading: a hasher reads each chunk back in order right after it is written (from
// the page cache). One stream from the archive runs at about 44 MB/s; four saturate a 1 Gbit/s link
// (refactor/15 "Phase 3c"). Finished chunks are recorded in <dest>.parts, so an interrupted download
// resumes where it stopped.
func (d *Downloader) fetchSegmented(ctx context.Context, url, dest string, p *Progress) (string, error) {
	size := p.Total
	chunk := d.ChunkSize
	if chunk <= 0 {
		chunk = 64 << 20
	}
	n := int((size + chunk - 1) / chunk)
	span := func(i int) (int64, int64) {
		start := int64(i) * chunk
		return start, min(start+chunk, size)
	}

	f, err := os.OpenFile(dest, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errPermanent, err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	parts := newPartsFile(dest+".parts", n)
	done := parts.load()
	switch {
	case done == nil && fi.Size() == size:
		// A complete file without a parts record (single-stream download, or placed by the user)
		done = make([]bool, n)
		for i := range done {
			done[i] = true
		}
	case done == nil:
		// A partial single-stream download: its first bytes are contiguous
		done = make([]bool, n)
		for i := range done {
			if _, end := span(i); end <= fi.Size() {
				done[i] = true
			}
		}
	}
	if fi.Size() != size {
		if err := f.Truncate(size); err != nil {
			return "", err
		}
	}

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var mu sync.Mutex
	cond := sync.NewCond(&mu)
	todo := make(chan int, n)
	for i := 0; i < n; i++ {
		if !done[i] {
			todo <- i
		}
	}
	close(todo)

	// Workers fetch chunks; each chunk retries on its own
	var wg sync.WaitGroup
	for w := 0; w < max(d.Connections, 1); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range todo {
				start, end := span(i)
				if err := d.fetchChunk(ctx, url, f, start, end, p); err != nil {
					cancel(fmt.Errorf("chunk %d (%d-%d): %w", i, start, end-1, err))
					mu.Lock()
					cond.Broadcast()
					mu.Unlock()
					return
				}
				mu.Lock()
				done[i] = true
				parts.save(done)
				cond.Broadcast()
				mu.Unlock()
			}
		}()
	}

	// Hash in order as chunks complete
	h := sha256.New()
	buf := make([]byte, 1<<20)
	var hashErr error
	for i := 0; i < n && hashErr == nil; i++ {
		mu.Lock()
		for !done[i] && ctx.Err() == nil {
			cond.Wait()
		}
		ready := done[i]
		mu.Unlock()
		if !ready {
			hashErr = context.Cause(ctx)
			break
		}
		start, end := span(i)
		if _, err := io.CopyBuffer(h, io.NewSectionReader(f, start, end-start), buf); err != nil {
			hashErr = err
		}
	}
	cancel(hashErr)
	wg.Wait()
	if hashErr != nil {
		return "", hashErr
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	parts.remove()
	p.Done.Store(size)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// fetchChunk writes bytes [start, end) of url at their offset in f, retrying with backoff.
func (d *Downloader) fetchChunk(ctx context.Context, url string, f *os.File, start, end int64, p *Progress) error {
	backoff := d.Backoff
	var err error
	for attempt := 1; attempt <= d.Attempts; attempt++ {
		var written int64
		written, err = d.chunkOnce(ctx, url, f, start, end, p)
		if err == nil {
			return nil
		}
		p.Done.Add(-written) // counted again on the retry
		if ctx.Err() != nil || errors.Is(err, errPermanent) {
			return err
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
		backoff *= 2
	}
	return err
}

func (d *Downloader) chunkOnce(ctx context.Context, url string, f *os.File, start, end int64, p *Progress) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end-1))
	resp, err := d.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusPartialContent:
	case http.StatusNotFound, http.StatusForbidden:
		return 0, fmt.Errorf("%w: HTTP %s", errPermanent, resp.Status)
	default:
		return 0, fmt.Errorf("HTTP %s", resp.Status)
	}
	w := &offsetWriter{f: f, off: start, p: p}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return w.off - start, err
	}
	if w.off != end {
		return w.off - start, fmt.Errorf("short chunk: %d of %d bytes", w.off-start, end-start)
	}
	return w.off - start, nil
}

type offsetWriter struct {
	f   *os.File
	off int64
	p   *Progress
}

func (w *offsetWriter) Write(b []byte) (int, error) {
	n, err := w.f.WriteAt(b, w.off)
	w.off += int64(n)
	w.p.Done.Add(int64(n))
	return n, err
}

// partsFile records which chunks are complete: one byte per chunk, written atomically.
type partsFile struct {
	path string
	n    int
}

func newPartsFile(path string, n int) *partsFile { return &partsFile{path, n} }

func (pf *partsFile) load() []bool {
	b, err := os.ReadFile(pf.path)
	if err != nil || len(b) != pf.n {
		return nil
	}
	done := make([]bool, pf.n)
	for i, c := range b {
		done[i] = c == 1
	}
	return done
}

func (pf *partsFile) save(done []bool) {
	b := make([]byte, len(done))
	for i, d := range done {
		if d {
			b[i] = 1
		}
	}
	tmp := pf.path + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, pf.path)
	}
}

func (pf *partsFile) remove() { os.Remove(pf.path) }
