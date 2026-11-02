package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// `pg-upgrade` upgrades the API database to the stack's PostgreSQL major (refactor/11 Q9), replacing
// scripts/postgres-upgrade.sh. Run as `docker compose run --rm pg-upgrade [--yes] [--mode M]` with
// the stack stopped, after raising POSTGRES_VERSION in .env.
//
// It runs pg_upgrade: the new cluster is built with this image's own binaries, the same official
// postgres:<ver>-alpine image the stack runs, and the old server comes from Alpine's postgresql<old>
// package (musl on both sides, so text indexes stay valid; refactor/15 "Run 3"). The new cluster is
// built in <data>/.pg-upgrade-staging and swapped in only after pg_upgrade and the statistics rebuild
// succeed; the data dir itself is kept (it may be a mount point).
//
// Modes: clone (copy-on-write, instant, where the filesystem supports it), copy (needs the database's
// size free again), link (hard links: minutes and no extra space, but once the new cluster has run the
// old one can't be started again). auto picks clone, else copy if there is room, else refuses and
// suggests --mode link.

const upgradeStaging = ".pg-upgrade-staging"

func runPGUpgrade(ctx context.Context, args []string, getenv func(string) string) error {
	fs := flag.NewFlagSet("pg-upgrade", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "don't ask before upgrading (CONFIRM=yes does the same)")
	mode := fs.String("mode", "auto", "auto, clone, copy or link")
	dryRun := fs.Bool("dry-run", false, "only report what would be done")
	skipAnalyze := fs.Bool("skip-analyze", false, "don't rebuild the planner statistics (do it later with vacuumdb --all --analyze-in-stages)")
	if err := fs.Parse(args); err != nil {
		return exitError{2, err.Error()}
	}
	switch *mode {
	case "auto", "clone", "copy", "link":
	default:
		return refuse("--mode %q: use auto, clone, copy or link", *mode)
	}
	if err := guardNotRunning("upgrade postgres"); err != nil {
		return err
	}

	from, err := dataMajor(pgDir)
	if err != nil {
		return err
	}
	to, err := serverMajor()
	if err != nil {
		return err
	}
	switch {
	case from == to:
		fmt.Printf("pg-upgrade: the database is already PostgreSQL %d; nothing to do\n", to)
		return nil
	case from > to:
		return refuse("the database is PostgreSQL %d, newer than POSTGRES_VERSION=%d: set POSTGRES_VERSION=%d in .env", from, to, from)
	}
	oldBin := fmt.Sprintf("/usr/libexec/postgresql%d", from) // Alpine: the binaries sit directly in it
	if _, err := os.Stat(filepath.Join(oldBin, "postgres")); err != nil {
		return refuse("upgrading from PostgreSQL %d isn't supported by this image (it has %s). "+
			"Re-seed instead (docker compose run --rm seed restores a fresh database), or upgrade in steps",
			from, strings.Join(oldMajors(), ", "))
	}
	if _, err := os.Stat(filepath.Join(pgDir, "postmaster.pid")); err == nil {
		return refuse("postgres wasn't shut down cleanly (postmaster.pid exists): start the stack once and stop it with docker compose --profile '*' down, then retry")
	}

	size, err := dirSize(pgDir)
	if err != nil {
		return err
	}
	pg, err := postgresUser()
	if err != nil {
		return err
	}
	if o, err := OwnerOf(pgDir); err != nil || o.UID != pg.uid {
		return refuse("the postgres data dir isn't owned by the postgres user (uid %d): start the stack once (its postgres fixes the ownership), stop it, and retry", pg.uid)
	}
	chosen, err := pickMode(*mode, size, free(pgDir), func() bool { return canClone(pgDir) })
	if err != nil {
		return err
	}
	fmt.Printf("pg-upgrade: plan: PostgreSQL %d -> %d, %s of data, mode %s (%s free)\n", from, to, human(size), chosen, human(free(pgDir)))
	if chosen == "link" {
		fmt.Println("pg-upgrade:   link mode: the old cluster can't be used again once the new one has started")
	}
	if *dryRun {
		return nil
	}
	if err := confirm(*yes || strings.EqualFold(getenv("CONFIRM"), "yes"), fmt.Sprintf("upgrade the database from PostgreSQL %d to %d", from, to)); err != nil {
		return err
	}

	work, err := os.MkdirTemp("", "pg-upgrade")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if err := os.Chown(work, pg.uid, pg.gid); err != nil {
		return err
	}
	start := time.Now()
	info, err := clusterInfo(ctx, oldBin, pgDir, work, getenv("PG_USER"))
	if err != nil {
		return fmt.Errorf("reading the old cluster: %w", err)
	}
	fmt.Printf("pg-upgrade: old cluster: install user %s, encoding %s, locale %s/%s (%s), data checksums %v\n", info.user, info.encoding, info.collate, info.ctype, info.provider, info.checksums)

	staging := filepath.Join(pgDir, upgradeStaging)
	if err := os.RemoveAll(staging); err != nil {
		return err
	}
	if err := os.Mkdir(staging, 0o700); err != nil {
		return err
	}
	if err := os.Chown(staging, pg.uid, pg.gid); err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(staging)
		}
	}()
	if err := run(ctx, "gosu", append([]string{"postgres", "initdb", "-D", staging}, info.initdbArgs(to)...)...); err != nil {
		return fmt.Errorf("initdb: %w", err)
	}
	upgradeArgs := []string{"postgres", "pg_upgrade", "-b", oldBin, "-B", "/usr/local/bin", "-d", pgDir, "-D", staging,
		"-U", info.user, "--socketdir", work, "-j", strconv.Itoa(runtime.NumCPU()), "--" + chosen}
	cmd := exec.CommandContext(ctx, "gosu", upgradeArgs...)
	cmd.Dir = work // pg_upgrade writes its logs into the current directory on older releases
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if chosen == "link" {
			return fmt.Errorf("pg_upgrade: %w. The old cluster wasn't started by the new server; if pg_upgrade renamed its global/pg_control to pg_control.old, rename it back to use it", err)
		}
		return fmt.Errorf("pg_upgrade: %w (the old data is unchanged)", err)
	}
	// Access rules are the user's; the new release's postgresql.conf defaults are kept
	for _, f := range []string{"pg_hba.conf", "pg_ident.conf"} {
		if err := copyFile(filepath.Join(pgDir, f), filepath.Join(staging, f), pg); err != nil {
			return err
		}
	}
	if !*skipAnalyze {
		if err := analyze(ctx, staging, work, info.user); err != nil {
			return fmt.Errorf("rebuilding statistics: %w (re-run, or use --skip-analyze and run vacuumdb --all --analyze-in-stages later)", err)
		}
	}
	syscall.Sync()

	fmt.Println("pg-upgrade: replacing the old cluster with the upgraded one")
	if err := SwapInFrom(pgDir, upgradeStaging); err != nil {
		return err
	}
	ok = true
	syscall.Sync()
	fmt.Printf("pg-upgrade: done in %s: the database is PostgreSQL %d. Start the stack: docker compose up -d\n", time.Since(start).Truncate(time.Second), to)
	if old, err := os.ReadFile(filepath.Join(pgDir, "postgresql.auto.conf")); err == nil && len(bytes.TrimSpace(stripComments(old))) > 0 {
		fmt.Println("pg-upgrade: note: settings made with ALTER SYSTEM were not carried over; set them again if needed")
	}
	return nil
}

// dataMajor reads the major version a data dir was created with.
func dataMajor(dir string) (int, error) {
	b, err := os.ReadFile(filepath.Join(dir, "PG_VERSION"))
	if err != nil {
		return 0, refuse("no PostgreSQL data in the postgres data dir (%v): nothing to upgrade", err)
	}
	return parseMajor(string(b))
}

func parseMajor(s string) (int, error) {
	m := regexp.MustCompile(`(\d+)`).FindString(strings.TrimSpace(s))
	n, err := strconv.Atoi(m)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("can't read a PostgreSQL major version from %q", strings.TrimSpace(s))
	}
	return n, nil
}

// serverMajor is the major of this image's postgres (the stack's POSTGRES_VERSION).
func serverMajor() (int, error) {
	out, err := exec.Command("postgres", "--version").Output()
	if err != nil {
		return 0, err
	}
	// "postgres (PostgreSQL) 17.6"
	f := strings.Fields(string(out))
	return parseMajor(f[len(f)-1])
}

func oldMajors() []string {
	m, _ := filepath.Glob("/usr/libexec/postgresql*/postgres")
	var out []string
	for _, p := range m {
		out = append(out, "PostgreSQL "+strings.TrimPrefix(filepath.Base(filepath.Dir(p)), "postgresql"))
	}
	sort.Strings(out)
	if len(out) == 0 {
		return []string{"no older releases"}
	}
	return out
}

// pickMode resolves auto: clone where supported, copy if the data fits again, else refuse.
func pickMode(mode string, size, free int64, cloneOK func() bool) (string, error) {
	need := size + size/10
	switch mode {
	case "clone":
		if !cloneOK() {
			return "", refuse("--mode clone: the filesystem can't clone files (use --mode copy or --mode link)")
		}
	case "copy":
		if free < need {
			return "", refuse("--mode copy needs about %s free, %s is: use --mode link, or make room", human(need), human(free))
		}
	case "auto":
		switch {
		case cloneOK():
			return "clone", nil
		case free >= need:
			return "copy", nil
		default:
			return "", refuse("not enough space to copy the database (%s needed, %s free) and the filesystem can't clone files: "+
				"re-run with --mode link (fast, no extra space; the old cluster can't be used again once the new one has run)", human(need), human(free))
		}
	}
	return mode, nil
}

// canClone tries a reflink copy (FICLONE) of a scratch file inside dir.
func canClone(dir string) bool {
	src, err := os.CreateTemp(dir, ".clone-test")
	if err != nil {
		return false
	}
	defer os.Remove(src.Name())
	defer src.Close()
	src.WriteString("x")
	dst, err := os.CreateTemp(dir, ".clone-test")
	if err != nil {
		return false
	}
	defer os.Remove(dst.Name())
	defer dst.Close()
	const ficlone = 0x40049409
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, dst.Fd(), ficlone, src.Fd())
	return errno == 0
}

func dirSize(dir string) (int64, error) {
	var n int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n, err
}

type pgUser struct{ uid, gid int }

func postgresUser() (pgUser, error) {
	u, err := user.Lookup("postgres")
	if err != nil {
		return pgUser{}, fmt.Errorf("no postgres user in the image: %w", err)
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	return pgUser{uid, gid}, nil
}

// cluster is what initdb needs to match: pg_upgrade requires the same install user, encoding,
// template0 locale and data checksums setting.
type cluster struct {
	user, encoding, provider, collate, ctype, locale string
	checksums                                        bool
}

// initdbArgs for a new cluster of major `to` that pg_upgrade accepts for this old one.
func (c cluster) initdbArgs(to int) []string {
	a := []string{"--username=" + c.user, "--encoding=" + c.encoding, "--lc-collate=" + c.collate, "--lc-ctype=" + c.ctype, "--auth=trust"}
	switch {
	case c.checksums:
		a = append(a, "--data-checksums")
	case to >= 18: // checksums became initdb's default in 18
		a = append(a, "--no-data-checksums")
	}
	switch c.provider {
	case "i":
		a = append(a, "--locale-provider=icu", "--icu-locale="+c.locale)
	case "b":
		a = append(a, "--locale-provider=builtin", "--builtin-locale="+c.locale)
	}
	return a
}

// clusterInfo starts the old server on a private socket (trust for local connections only, nothing
// listens on TCP), reads the install user and template0's encoding and locale, and stops it.
func clusterInfo(ctx context.Context, bin, data, work, pgUserEnv string) (cluster, error) {
	var c cluster
	hba := filepath.Join(work, "hba.conf")
	if err := os.WriteFile(hba, []byte("local all all trust\n"), 0o644); err != nil {
		return c, err
	}
	pg, _ := postgresUser()
	os.Chown(hba, pg.uid, pg.gid)
	if err := run(ctx, "gosu", "postgres", filepath.Join(bin, "pg_ctl"), "-D", data, "-w", "-l", filepath.Join(work, "old.log"), "start",
		"-o", "-c listen_addresses='' -c unix_socket_directories="+work+" -c hba_file="+hba); err != nil {
		log, _ := os.ReadFile(filepath.Join(work, "old.log"))
		return c, fmt.Errorf("starting the old server: %w\n%s", err, log)
	}
	defer run(context.Background(), "gosu", "postgres", filepath.Join(bin, "pg_ctl"), "-D", data, "-w", "-m", "fast", "stop")

	var out []byte
	var err error
	for _, u := range uniq([]string{pgUserEnv, "postgres"}) {
		q := exec.CommandContext(ctx, filepath.Join(bin, "psql"), "-h", work, "-U", u, "-d", "template1", "-AtX", "-F", "|", "-c",
			"select (select rolname from pg_authid where oid = 10), pg_encoding_to_char(encoding), datlocprovider, datcollate, datctype, "+
				localeColumn(bin)+", current_setting('data_checksums') from pg_database where datname = 'template0'")
		if out, err = q.Output(); err == nil {
			break
		}
	}
	if err != nil {
		return c, fmt.Errorf("querying the old server: %w", err)
	}
	f := strings.Split(strings.TrimSpace(string(out)), "|")
	if len(f) != 7 {
		return c, fmt.Errorf("unexpected answer %q", out)
	}
	return cluster{user: f[0], encoding: f[1], provider: f[2], collate: f[3], ctype: f[4], locale: f[5], checksums: f[6] == "on"}, nil
}

// localeColumn: template0's ICU/builtin locale is datlocale from PostgreSQL 17, daticulocale before.
func localeColumn(bin string) string {
	out, err := exec.Command(filepath.Join(bin, "postgres"), "--version").Output()
	f := strings.Fields(string(out))
	if err == nil && len(f) > 0 {
		if n, err := parseMajor(f[len(f)-1]); err == nil && n < 17 {
			return "coalesce(daticulocale, '')"
		}
	}
	return "coalesce(datlocale, '')"
}

// analyze rebuilds the planner statistics on the new cluster, which pg_upgrade doesn't carry over.
func analyze(ctx context.Context, data, work, user string) error {
	if err := run(ctx, "gosu", "postgres", "pg_ctl", "-D", data, "-w", "-l", filepath.Join(work, "new.log"), "start",
		"-o", "-c listen_addresses='' -c unix_socket_directories="+work); err != nil {
		return err
	}
	defer run(context.Background(), "gosu", "postgres", "pg_ctl", "-D", data, "-w", "-m", "fast", "stop")
	return run(ctx, "vacuumdb", "-h", work, "-U", user, "--all", "--analyze-in-stages", "-j", strconv.Itoa(runtime.NumCPU()))
}

func copyFile(src, dst string, owner pgUser) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		return err
	}
	return os.Chown(dst, owner.uid, owner.gid)
}

func stripComments(b []byte) []byte {
	var out [][]byte
	for _, l := range bytes.Split(b, []byte("\n")) {
		if t := bytes.TrimSpace(l); len(t) > 0 && t[0] != '#' {
			out = append(out, l)
		}
	}
	return bytes.Join(out, []byte("\n"))
}

func uniq(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
