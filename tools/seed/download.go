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
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Downloader fetches archive files. Each file is hashed while it is written (no second read of a
// multi-hundred-GB archive); a partial file is hashed once and then resumed with a Range request.
type Downloader struct {
	Client      *http.Client
	Attempts    int           // per file (single stream) or per chunk (segmented); each attempt resumes
	Backoff     time.Duration // between attempts, doubled each time
	Connections int           // parallel Range requests per file; 1 = single stream
	ChunkSize   int64         // segmented download chunk (default 64 MiB)
}

// Progress counts bytes for the progress reporter.
type Progress struct {
	Name  string
	Total int64
	Done  atomic.Int64
}

// RemoteSize returns a file's size from a 1-byte ranged GET: archive.hiro.so answers HEAD with 403
// (pre-signed redirects), so Content-Length from HEAD is unusable (refactor/09 D53). ranges reports
// whether the server honours Range requests (needed for resuming and parallel downloads).
func (d *Downloader) RemoteSize(ctx context.Context, url string) (size int64, ranges bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, false, err
	}
	req.Header.Set("Range", "bytes=0-0")
	resp, err := d.Client.Do(req)
	if err != nil {
		return 0, false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusPartialContent:
		cr := resp.Header.Get("Content-Range") // bytes 0-0/<total>
		if i := strings.LastIndex(cr, "/"); i >= 0 {
			size, err = strconv.ParseInt(cr[i+1:], 10, 64)
			return size, err == nil, err
		}
		return 0, false, fmt.Errorf("%s: no total in Content-Range %q", url, cr)
	case http.StatusOK:
		return resp.ContentLength, false, nil
	}
	return 0, false, fmt.Errorf("%s: HTTP %s", url, resp.Status)
}

// Checksum reads a published .sha256 file ("<hex>  <name>").
func (d *Downloader) Checksum(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := d.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: HTTP %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	f := strings.Fields(string(b))
	if len(f) == 0 || len(f[0]) != 64 {
		return "", fmt.Errorf("%s: not a sha256 file", url)
	}
	return strings.ToLower(f[0]), nil
}

// Fetch downloads url to dest (resuming a partial dest) and returns the file's sha256. With a known
// size and Connections > 1 it uses parallel Range requests (fetchSegmented), otherwise one stream.
func (d *Downloader) Fetch(ctx context.Context, url, dest string, p *Progress) (string, error) {
	if d.Connections > 1 && p.Total > 0 {
		return d.fetchSegmented(ctx, url, dest, p)
	}
	backoff := d.Backoff
	var lastErr error
	for attempt := 1; attempt <= d.Attempts; attempt++ {
		sum, err := d.fetchOnce(ctx, url, dest, p)
		if err == nil {
			return sum, nil
		}
		if ctx.Err() != nil || errors.Is(err, errPermanent) {
			return "", err
		}
		lastErr = err
		fmt.Fprintf(os.Stderr, "seed: %s: %v (attempt %d/%d, resuming in %s)\n", p.Name, err, attempt, d.Attempts, backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return "", ctx.Err()
		}
		backoff *= 2
	}
	return "", fmt.Errorf("%s: giving up after %d attempts: %w", p.Name, d.Attempts, lastErr)
}

var errPermanent = errors.New("permanent")

func (d *Downloader) fetchOnce(ctx context.Context, url, dest string, p *Progress) (string, error) {
	f, err := os.OpenFile(dest, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errPermanent, err)
	}
	defer f.Close()
	h := sha256.New()
	// Hash what is already there (a partial download) before resuming
	have, err := io.Copy(h, f)
	if err != nil {
		return "", err
	}
	p.Done.Store(have)
	if p.Total > 0 && have == p.Total {
		return hex.EncodeToString(h.Sum(nil)), nil
	}
	if p.Total > 0 && have > p.Total {
		return "", fmt.Errorf("%w: %s is larger than the remote file; delete it and run again", errPermanent, dest)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	resp, err := d.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusPartialContent && have > 0:
	case resp.StatusCode == http.StatusOK:
		// No resume support (or a fresh download): start over
		if have > 0 {
			if err := f.Truncate(0); err != nil {
				return "", err
			}
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return "", err
			}
			h.Reset()
			p.Done.Store(0)
		}
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden:
		return "", fmt.Errorf("%w: %s: HTTP %s", errPermanent, url, resp.Status)
	default:
		return "", fmt.Errorf("%s: HTTP %s", url, resp.Status)
	}
	if _, err := io.Copy(io.MultiWriter(f, h, counter{p}), resp.Body); err != nil {
		return "", err
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	if p.Total > 0 && p.Done.Load() != p.Total {
		return "", fmt.Errorf("%s: got %d of %d bytes", url, p.Done.Load(), p.Total)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type counter struct{ p *Progress }

func (c counter) Write(b []byte) (int, error) {
	c.p.Done.Add(int64(len(b)))
	return len(b), nil
}

// HashFile hashes a local file (used for archives placed in the download dir).
func HashFile(path string, p *Progress) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(h, counter{p}), f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Report prints progress for the given files every interval until done is closed.
func Report(done <-chan struct{}, interval time.Duration, files ...*Progress) {
	last := make([]int64, len(files))
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			for i, p := range files {
				n := p.Done.Load()
				if p.Total > 0 && n >= p.Total && last[i] >= p.Total {
					continue // finished and already reported at 100%
				}
				rate := float64(n-last[i]) / interval.Seconds()
				last[i] = n
				pct := ""
				if p.Total > 0 {
					pct = fmt.Sprintf(" %5.1f%%", 100*float64(n)/float64(p.Total))
				}
				fmt.Printf("seed:   %-60s %s / %s%s  %s/s\n", p.Name, human(n), human(p.Total), pct, human(int64(rate)))
			}
		}
	}
}

func human(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
