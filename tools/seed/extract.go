package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// StagingName is where a component is rebuilt inside its own data dir, so the final swap is a
// rename on the same filesystem and the data dir (possibly a mount point) is never removed.
const StagingName = ".seed-staging"

// Owner is the uid/gid extracted files get: the owner of the data dir they land in (the archive's
// own uid 1001 means nothing on this host).
type Owner struct{ UID, GID int }

func OwnerOf(dir string) (Owner, error) {
	fi, err := os.Stat(dir)
	if err != nil {
		return Owner{}, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return Owner{}, fmt.Errorf("%s: no owner information", dir)
	}
	return Owner{int(st.Uid), int(st.Gid)}, nil
}

// ArchiveExts are the chainstate archive formats, by file extension: the Hiro archive publishes
// .tar.zst; local archives may also be .tar.gz/.tgz, .tar.xz or plain .tar (refactor/11 Q16/Q21).
var ArchiveExts = []string{".tar.zst", ".tar.gz", ".tgz", ".tar.xz", ".tar"}

// ArchiveBase strips a chainstate archive or dump extension (for the .sha256 sidecar name).
func ArchiveBase(name string) string {
	for _, ext := range append(ArchiveExts, ".dump") {
		if strings.HasSuffix(name, ext) {
			return strings.TrimSuffix(name, ext)
		}
	}
	return name
}

// NodeLocalState are the archive node's own databases, not chain data (tar --exclude patterns, matched
// at any depth). peer.sqlite holds its peer list, which kept a seeded mainnet node at a stale burn
// height until removed; event_observers.sqlite holds its pending event deliveries. The node recreates
// both on start (refactor/15 "Phase 3c").
var NodeLocalState = []string{"peer.sqlite*", "event_observers.sqlite*"}

// ExtractArchive extracts a chainstate archive into dest (existing, empty) with GNU tar, which picks
// the decompressor from the file itself (zstd, xz, gzip or none): a new archive format needs a package
// in the image, not code. The image is Alpine, like the stack's postgres (see the Dockerfile), so this
// is about 40% slower than on glibc, but it runs in parallel with the longer restore (refactor/15
// "Phase 3c"). GNU tar strips leading "/" and refuses ".." members
// and writes through symlinks. Files get the data dir's owner instead of the archive's (uid 1001).
// NodeLocalState is left out.
func ExtractArchive(ctx context.Context, archive, dest string, owner Owner, p *Progress) error {
	var stderr strings.Builder
	args := []string{"-x", "-f", archive, "-C", dest, "--no-same-owner"}
	for _, pat := range NodeLocalState {
		args = append(args, "--exclude="+pat)
	}
	cmd := exec.CommandContext(ctx, "tar", args...)
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go trackReadPosition(done, archive, p)
	err := cmd.Wait()
	close(done)
	if err != nil {
		return fmt.Errorf("tar -x %s: %w: %s", filepath.Base(archive), err, strings.TrimSpace(stderr.String()))
	}
	p.Done.Store(p.Total)
	return chownTree(dest, owner)
}

// trackReadPosition reports how far tar (or the decompressor it runs) has read into the archive, from
// /proc/<pid>/fdinfo: progress without knowing the archive's format.
func trackReadPosition(done <-chan struct{}, archive string, p *Progress) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			if pos := readPosition(archive); pos > p.Done.Load() {
				p.Done.Store(pos)
			}
		}
	}
}

func readPosition(path string) int64 {
	var best int64
	fds, _ := filepath.Glob("/proc/[0-9]*/fd/*")
	for _, fd := range fds {
		if target, err := os.Readlink(fd); err != nil || target != path {
			continue
		}
		info, err := os.ReadFile(strings.Replace(fd, "/fd/", "/fdinfo/", 1))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(info), "\n") {
			if v, ok := strings.CutPrefix(line, "pos:"); ok {
				if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && n > best {
					best = n
				}
			}
		}
	}
	return best
}

// chownTree gives everything under dir the owner of the data dir (tar as root leaves root:root).
func chownTree(dir string, owner Owner) error {
	if owner.UID == os.Geteuid() && owner.GID == os.Getegid() {
		return nil
	}
	return filepath.WalkDir(dir, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(path, owner.UID, owner.GID)
	})
}

// PrepareStaging returns an empty <dir>/.seed-staging, removing one left by an earlier failed run.
func PrepareStaging(dir string, owner Owner, mode os.FileMode) (string, error) {
	staging := filepath.Join(dir, StagingName)
	if err := os.RemoveAll(staging); err != nil {
		return "", err
	}
	if err := os.Mkdir(staging, mode); err != nil {
		return "", err
	}
	return staging, os.Chown(staging, owner.UID, owner.GID)
}

// SwapIn replaces the contents of dir with the contents of <dir>/.seed-staging. The dir itself is
// kept (it may be a mount point, refactor/09 D48); everything else in it is removed first.
func SwapIn(dir string) error {
	staging := filepath.Join(dir, StagingName)
	old, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range old {
		if e.Name() == StagingName {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	staged, err := os.ReadDir(staging)
	if err != nil {
		return err
	}
	for _, e := range staged {
		if err := os.Rename(filepath.Join(staging, e.Name()), filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return os.Remove(staging)
}
