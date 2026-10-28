package main

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/klauspost/compress/zstd"
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

// ExtractTarZst streams a .tar.zst archive into dest, which must exist and be empty.
func ExtractTarZst(archive, dest string, owner Owner, p *Progress) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := zstd.NewReader(io.TeeReader(f, counter{p}), zstd.WithDecoderMaxWindow(1<<31))
	if err != nil {
		return err
	}
	defer zr.Close()
	return extractTar(tar.NewReader(zr), dest, owner)
}

func extractTar(tr *tar.Reader, dest string, owner Owner) error {
	root, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(root, hdr.Name)
		if err != nil {
			return err
		}
		mode := os.FileMode(hdr.Mode).Perm()
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, mode|0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// The link target is stored as-is; only links that stay inside dest are allowed
			if _, err := safeJoin(root, filepath.Join(filepath.Dir(hdr.Name), hdr.Linkname)); err != nil || filepath.IsAbs(hdr.Linkname) {
				return fmt.Errorf("refusing symlink %s -> %s outside the archive root", hdr.Name, hdr.Linkname)
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		case tar.TypeLink:
			src, err := safeJoin(root, hdr.Linkname)
			if err != nil {
				return err
			}
			if err := os.Link(src, target); err != nil {
				return err
			}
		default:
			fmt.Fprintf(os.Stderr, "seed: skipping %s (tar type %q)\n", hdr.Name, hdr.Typeflag)
			continue
		}
		if err := os.Lchown(target, owner.UID, owner.GID); err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeSymlink {
			_ = os.Chtimes(target, hdr.ModTime, hdr.ModTime)
		}
	}
}

// safeJoin resolves name under root and refuses anything that would land outside it.
func safeJoin(root, name string) (string, error) {
	p := filepath.Join(root, name) // Join cleans "..", so check the result is still under root
	if p != root && !strings.HasPrefix(p, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("refusing archive entry %q outside the archive root", name)
	}
	return p, nil
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
