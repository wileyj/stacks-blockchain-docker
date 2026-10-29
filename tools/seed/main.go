// Command seed restores a node + API from a Hiro archive snapshot (or local archive files). It runs
// as the compose `seed` job, so the host needs nothing but Docker:
//
//	docker compose --profile '*' down
//	docker compose run --rm seed [--dry-run] [--yes] [--keep-downloads] [--chainstate F --pgdump F]
//
// Order: refuse while the stack is up -> select the snapshot (refactor/11 Q24) -> confirm -> download
// both files in parallel, hashing while writing (resumable) -> extract the chainstate and restore the
// dump in parallel, each into a staging dir inside its data dir -> only then swap them in. The data
// dirs themselves are never removed (they may be mount points), and a failure leaves the old data.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	chainDir    = "/data/stacks-blockchain"
	pgDir       = "/data/postgres"
	downloadDir = "/downloads"
)

// exitError carries the exit code: 2 for refusals and bad input, 1 for operational failures (Q1/Q12).
type exitError struct {
	code int
	msg  string
}

func (e exitError) Error() string { return e.msg }

func refuse(format string, a ...any) error { return exitError{2, fmt.Sprintf(format, a...)} }

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	cmd, run := "seed", runSeed
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "reset":
			cmd, run = "reset", runReset
			os.Args = os.Args[1:]
		case "build-indexes":
			cmd, run = "build-indexes", runBuildIndexes
			os.Args = os.Args[1:]
		}
	}
	if err := run(ctx, os.Args[1:], os.Getenv); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmd, err)
		var ee exitError
		if errors.As(err, &ee) {
			os.Exit(ee.code)
		}
		os.Exit(1)
	}
}

type options struct {
	yes, dryRun, keep   bool
	chainFile, dumpFile string
	deferIdx            listFlag
}

// listFlag is a repeatable string flag.
type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

func runSeed(ctx context.Context, args []string, getenv func(string) string) error {
	var o options
	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	fs.BoolVar(&o.yes, "yes", false, "don't ask before replacing the existing data (CONFIRM=yes does the same)")
	fs.BoolVar(&o.dryRun, "dry-run", false, "select the snapshot and print the plan; change nothing")
	fs.BoolVar(&o.keep, "keep-downloads", false, "keep the archives this run downloaded")
	fs.StringVar(&o.chainFile, "chainstate", "", "local chainstate archive in the download dir (with --pgdump)")
	fs.StringVar(&o.dumpFile, "pgdump", "", "local API dump in the download dir (with --chainstate)")
	fs.Var(&o.deferIdx, "defer-index", "build this API index after the stack starts instead of during the restore (repeatable; SEED_DEFER_INDEXES=a,b)")
	if err := fs.Parse(args); err != nil {
		return exitError{2, err.Error()}
	}
	if (o.chainFile == "") != (o.dumpFile == "") {
		return refuse("--chainstate and --pgdump go together (a snapshot is always a same-date pair)")
	}
	o.yes = o.yes || strings.EqualFold(getenv("CONFIRM"), "yes")

	network := getenv("NETWORK")
	if network != "mainnet" && network != "testnet" {
		return refuse("NETWORK=%q: set NETWORK in .env (mainnet or testnet)", network)
	}
	pgMajor, err := strconv.Atoi(strings.SplitN(orDefault(getenv("POSTGRES_VERSION"), "17"), ".", 2)[0])
	if err != nil {
		return refuse("POSTGRES_VERSION=%q is not a version", getenv("POSTGRES_VERSION"))
	}
	pins := Pins{Core: getenv("STACKS_BLOCKCHAIN_VERSION"), API: getenv("STACKS_BLOCKCHAIN_API_VERSION"), PG: pgMajor}
	if pins.Core == "" || pins.API == "" {
		return refuse("STACKS_BLOCKCHAIN_VERSION and STACKS_BLOCKCHAIN_API_VERSION must be set in .env")
	}
	jobs, _ := strconv.Atoi(getenv("SEED_RESTORE_JOBS"))
	if jobs <= 0 {
		jobs = min(runtime.NumCPU(), 8)
	}
	pg := PG{User: orDefault(getenv("PG_USER"), "postgres"), Password: orDefault(getenv("PG_PASSWORD"), "postgres"),
		Database: orDefault(getenv("PG_DATABASE"), "postgres"), Schema: orDefault(getenv("PG_SCHEMA"), "public"), Jobs: jobs,
		MaintenanceMem: orDefault(getenv("SEED_PG_MAINTENANCE_MEM"), "1GB"), Defer: o.deferIdx}
	for _, n := range strings.Split(getenv("SEED_DEFER_INDEXES"), ",") {
		if n = strings.TrimSpace(n); n != "" {
			pg.Defer = append(pg.Defer, n)
		}
	}
	archiveURL := strings.TrimRight(orDefault(getenv("ARCHIVE_URL"), "https://archive.hiro.so"), "/")

	fmt.Printf("seed: %s, pinned core %s / API %s / pg%d\n", network, pins.Core, pins.API, pins.PG)
	if err := guardNotRunning("seed"); err != nil {
		return err
	}

	conns, _ := strconv.Atoi(getenv("SEED_CONNECTIONS"))
	if conns <= 0 {
		conns = 4 // saturates a 1 Gbit/s link from the archive; one stream gets about 44 MB/s
	}
	d := &Downloader{Client: &http.Client{Timeout: 0}, Attempts: 10, Backoff: 5 * time.Second, Connections: conns}
	var chain, dump fileJob
	if o.chainFile != "" {
		if chain, dump, err = localPair(network, pins, o); err != nil {
			return err
		}
	} else {
		if chain, dump, err = remotePair(ctx, d, archiveURL, network, pins); err != nil {
			return err
		}
	}

	printPlan(chain, dump)
	if o.dryRun {
		fmt.Println("seed: dry run, nothing was changed")
		return nil
	}
	if err := confirm(o.yes, fmt.Sprintf("replace the %s chainstate and API database", network)); err != nil {
		return err
	}

	// 1. Get and verify both files (in parallel)
	if err := both(ctx, func(ctx context.Context) error { return chain.obtain(ctx, d) }, func(ctx context.Context) error { return dump.obtain(ctx, d) }); err != nil {
		return fmt.Errorf("%w (no data was changed)", err)
	}

	// 2. Build both components in staging (in parallel); the old data is untouched until step 3
	chainOwner, err := OwnerOf(chainDir)
	if err != nil {
		return err
	}
	pgOwner, err := OwnerOf(pgDir)
	if err != nil {
		return err
	}
	chainStaging, err := PrepareStaging(chainDir, chainOwner, 0o755)
	if err != nil {
		return err
	}
	pgStaging, err := PrepareStaging(pgDir, pgOwner, 0o700)
	if err != nil {
		return err
	}
	extractP := &Progress{Name: "extract " + filepath.Base(chain.path), Total: chain.size}
	done := make(chan struct{})
	go Report(done, 30*time.Second, extractP)
	err = both(ctx,
		func(ctx context.Context) error {
			return ExtractArchive(ctx, chain.path, chainStaging, chainOwner, extractP)
		},
		func(ctx context.Context) error { return RestoreDump(ctx, dump.path, pgStaging, pg) },
	)
	close(done)
	if err != nil {
		os.RemoveAll(chainStaging)
		os.RemoveAll(pgStaging)
		return fmt.Errorf("%w (the existing data was left as it was)", err)
	}

	// 3. Swap both in
	fmt.Println("seed: replacing the existing data")
	if err := SwapIn(chainDir); err != nil {
		return err
	}
	if err := SwapIn(pgDir); err != nil {
		return err
	}
	syscall.Sync()

	for _, f := range []fileJob{chain, dump} {
		if f.downloaded && !o.keep {
			os.Remove(f.path)
			os.Remove(f.sumPath())
		}
	}
	fmt.Printf("seed: done: %s snapshot %s. Start the stack with: docker compose up -d\n", network, chain.label)
	return nil
}

// fileJob is one archive file: where it is, where it comes from, and how to verify it.
type fileJob struct {
	name, path, url, sumURL, wantSum, label string
	size                                    int64
	downloaded                              bool // created by this run (deleted afterwards)
	local                                   bool // provided by the user (never deleted)
}

func (f *fileJob) obtain(ctx context.Context, d *Downloader) error {
	p := &Progress{Name: f.name, Total: f.size}
	done := make(chan struct{})
	defer close(done)
	go Report(done, 30*time.Second, p)
	var got string
	var err error
	if f.local {
		fmt.Printf("seed: verifying %s\n", f.name)
		got, err = HashFile(f.path, p)
	} else {
		if _, statErr := os.Stat(f.path); errors.Is(statErr, os.ErrNotExist) {
			f.downloaded = true
		}
		got, err = d.Fetch(ctx, f.url, f.path, p)
	}
	if err != nil {
		return err
	}
	if f.wantSum == "" {
		fmt.Printf("seed: WARNING: no published checksum for %s, not verified\n", f.name)
		return nil
	}
	if got != f.wantSum {
		return fmt.Errorf("%s: sha256 %s does not match the published %s; delete the file and run again", f.name, got, f.wantSum)
	}
	fmt.Printf("seed: %s: sha256 OK\n", f.name)
	if !f.local {
		// Keep the published checksum next to the archive, so a later --chainstate/--pgdump run can verify it
		if err := os.WriteFile(f.sumPath(), []byte(f.wantSum+"  "+f.name+"\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// sumPath is <archive without its extension>.sha256, the archive's own naming.
func (f *fileJob) sumPath() string { return ArchiveBase(f.path) + ".sha256" }

func remotePair(ctx context.Context, d *Downloader, base, network string, pins Pins) (fileJob, fileJob, error) {
	chainList, err := get(ctx, d, fmt.Sprintf("%s/%s/stacks-blockchain/", base, network))
	if err != nil {
		return fileJob{}, fileJob{}, err
	}
	dumpList, err := get(ctx, d, fmt.Sprintf("%s/%s/stacks-blockchain-api-pg/", base, network))
	if err != nil {
		return fileJob{}, fileJob{}, err
	}
	sel, err := Select(network, chainList, dumpList, pins)
	if err != nil {
		if sel.Latest != nil {
			err = fmt.Errorf("%v. Latest snapshot %s. Update the versions in .env to match a snapshot, or sync from genesis", err, sel.Latest)
		}
		return fileJob{}, fileJob{}, exitError{1, err.Error()}
	}
	fmt.Printf("seed: selected snapshot %s\n", sel.Chosen)
	for _, w := range sel.Warnings {
		fmt.Printf("seed: WARNING: %s\n", w)
	}
	mk := func(component, name, ext string) (fileJob, error) {
		url := fmt.Sprintf("%s/%s/%s/%s", base, network, component, name)
		sumURL := strings.TrimSuffix(url, ext) + ".sha256"
		size, ranges, err := d.RemoteSize(ctx, url)
		if err != nil {
			return fileJob{}, err
		}
		if !ranges && d.Connections > 1 {
			fmt.Printf("seed: %s: the server doesn't support Range requests: one stream, no resume\n", name)
			d.Connections = 1
		}
		sum, err := d.Checksum(ctx, sumURL)
		if err != nil {
			return fileJob{}, err
		}
		return fileJob{name: name, path: filepath.Join(downloadDir, name), url: url, sumURL: sumURL, wantSum: sum, size: size, label: sel.Chosen.String()}, nil
	}
	chain, err := mk("stacks-blockchain", sel.Chosen.ChainFile(network), ".tar.zst")
	if err != nil {
		return fileJob{}, fileJob{}, err
	}
	dump, err := mk("stacks-blockchain-api-pg", sel.Chosen.DumpFile(), ".dump")
	return chain, dump, err
}

// localPair checks user-provided archives with the same rules: versions and date from the file names.
func localPair(network string, pins Pins, o options) (fileJob, fileJob, error) {
	cm := localChainRe.FindStringSubmatch(o.chainFile)
	dm := dumpRe.FindStringSubmatch(o.dumpFile)
	if cm == nil || dm == nil || cm[0] != filepath.Base(o.chainFile) || dm[0] != filepath.Base(o.dumpFile) {
		return fileJob{}, fileJob{}, refuse("local archives must keep their archive names (<net>-stacks-blockchain-<ver>-<date>.tar[.zst|.gz|.xz], stacks-blockchain-api-pg-<pg>-<ver>-<date>.dump) so their versions can be checked")
	}
	pgv, _ := strconv.Atoi(dm[1])
	s := Snapshot{Date: cm[3], Core: cm[2], API: dm[2], PG: pgv}
	switch {
	case cm[1] != network:
		return fileJob{}, fileJob{}, refuse("%s is a %s archive, NETWORK is %s", o.chainFile, cm[1], network)
	case cm[3] != dm[3]:
		return fileJob{}, fileJob{}, refuse("the chainstate (%s) and the dump (%s) are from different dates; a snapshot must be a same-date pair", cm[3], dm[3])
	case !compatible(s.Core, pins.Core) || !compatible(s.API, pins.API) || s.PG > pins.PG:
		return fileJob{}, fileJob{}, refuse("local snapshot %s is not compatible with the pinned core %s / API %s / pg%d", s, pins.Core, pins.API, pins.PG)
	}
	fmt.Printf("seed: local snapshot %s\n", s)
	mk := func(name string) (fileJob, error) {
		path := filepath.Join(downloadDir, filepath.Base(name))
		fi, err := os.Stat(path)
		if err != nil {
			return fileJob{}, refuse("%s not found in the download dir (SEED_DOWNLOAD_DIR)", filepath.Base(name))
		}
		f := fileJob{name: filepath.Base(name), path: path, size: fi.Size(), local: true, label: s.String()}
		if b, err := os.ReadFile(f.sumPath()); err == nil {
			if fields := strings.Fields(string(b)); len(fields) > 0 {
				f.wantSum = strings.ToLower(fields[0])
			}
		}
		return f, nil
	}
	chain, err := mk(o.chainFile)
	if err != nil {
		return fileJob{}, fileJob{}, err
	}
	dump, err := mk(o.dumpFile)
	return chain, dump, err
}

// guardNotRunning refuses while any stack service is up: their names resolve on the compose network
// only while their containers run (refactor/14 R5: the guard lives in the job, not in a CLI).
func guardNotRunning(job string) error {
	var up []string
	for _, svc := range []string{"stacks-blockchain", "stacks-blockchain-api", "postgres", "stacks-signer"} {
		if addrs, err := net.LookupHost(svc); err == nil && len(addrs) > 0 {
			up = append(up, svc)
		}
	}
	if len(up) > 0 {
		verb := "are"
		if len(up) == 1 {
			verb = "is"
		}
		return exitError{1, fmt.Sprintf("refusing to %s while %s %s running. Stop the stack first: docker compose --profile '*' down", job, strings.Join(up, ", "), verb)}
	}
	if _, err := os.Stat(filepath.Join(pgDir, "postmaster.pid")); err == nil {
		fmt.Println("seed: WARNING: postgres left a postmaster.pid (unclean shutdown); continuing because postgres isn't running")
	}
	return nil
}

func printPlan(chain, dump fileJob) {
	fmt.Println("seed: plan:")
	for _, f := range []fileJob{chain, dump} {
		src := f.url
		if f.local {
			src = "local file"
		}
		fmt.Printf("seed:   %-58s %10s  %s\n", f.name, human(f.size), src)
	}
	fmt.Printf("seed:   download dir: %s free\n", human(free(downloadDir)))
	fmt.Printf("seed:   then REPLACE the contents of the chainstate dir (%s free) and the postgres dir (%s free)\n", human(free(chainDir)), human(free(pgDir)))
	fmt.Println("seed:   (built in a staging dir first; the old data stays until both are ready)")
}

// confirm asks before a destructive step; without a TTY it needs --yes / CONFIRM=yes (Q12, exit 2).
func confirm(yes bool, action string) error {
	if yes {
		return nil
	}
	if fi, err := os.Stdin.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return refuse("not running interactively: re-run with --yes (or CONFIRM=yes) to %s", action)
	}
	fmt.Printf("%s? [y/N] ", strings.ToUpper(action[:1])+action[1:])
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
		return refuse("aborted, nothing was changed")
	}
	return nil
}

// both runs a and b in parallel. The first failure cancels the other (no point extracting for hours
// after the restore failed) and is the error returned.
func both(ctx context.Context, a, b func(context.Context) error) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var wg sync.WaitGroup
	for _, f := range []func(context.Context) error{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := f(ctx); err != nil {
				cancel(err)
			}
		}()
	}
	wg.Wait()
	return context.Cause(ctx)
}

func get(ctx context.Context, d *Downloader, url string) (string, error) {
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
	var b strings.Builder
	_, err = bufio.NewReader(resp.Body).WriteTo(&b)
	return b.String(), err
}

func free(path string) int64 {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil {
		return 0
	}
	return int64(st.Bavail) * int64(st.Bsize)
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
