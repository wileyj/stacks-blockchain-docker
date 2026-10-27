// Command config renders conf/<network>/Config.toml (and Signer.toml when the signer profile is
// enabled) from the network template (*.sample), the .env values and the user's *.overrides.toml.
// It runs as the one-shot `config` service on every `docker compose up` (refactor/14 §2.1).
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

func main() {
	confRoot := flag.String("conf", "/conf", "directory holding conf/<network>/")
	flag.Parse()
	if err := run(*confRoot, os.Getenv); err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}
}

func run(confRoot string, getenv func(string) string) error {
	s, err := SettingsFromEnv(getenv)
	if err != nil {
		return err
	}
	dir := filepath.Join(confRoot, s.Network)
	profiles := make([]string, 0, len(s.Profiles))
	for p := range s.Profiles {
		profiles = append(profiles, p)
	}
	sort.Strings(profiles)
	fmt.Printf("config: network=%s profiles=%s\n", s.Network, strings.Join(profiles, ","))

	if err := renderFile(dir, "Config.toml", s, RenderNode); err != nil {
		return err
	}
	if s.Profiles["signer"] {
		if err := renderFile(dir, "Signer.toml", s, RenderSigner); err != nil {
			return err
		}
	}
	for _, o := range s.Observers() {
		fmt.Printf("config: events_observer %s\n", o.(map[string]any)["endpoint"])
	}
	return nil
}

type renderer func(tmpl, overrides []byte, s Settings, source string) ([]byte, error)

func renderFile(dir, name string, s Settings, render renderer) error {
	base := strings.TrimSuffix(name, ".toml")
	tmplPath := filepath.Join(dir, name+".sample")
	tmpl, err := os.ReadFile(tmplPath)
	if err != nil {
		return fmt.Errorf("reading template: %w", err)
	}
	overridesName := base + ".overrides.toml"
	overrides, err := os.ReadFile(filepath.Join(dir, overridesName))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	source := name + ".sample"
	if len(overrides) > 0 {
		source += " + " + overridesName
	}
	out, err := render(tmpl, overrides, s, source)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}

	dest := filepath.Join(dir, name)
	if err := moveLegacy(dest, overridesName); err != nil {
		return err
	}
	// Signer.toml holds the private key
	mode := os.FileMode(0o644)
	if name == "Signer.toml" {
		mode = 0o600
	}
	if err := writeAtomic(dest, out, mode); err != nil {
		return err
	}
	fmt.Printf("config: wrote %s\n", filepath.Join(s.Network, name))
	return nil
}

// moveLegacy moves a file that wasn't written by this job (a hand-edited or manage.sh-rendered
// Config.toml) to <name>.legacy once, so no edit is silently overwritten.
func moveLegacy(dest, overridesName string) error {
	b, err := os.ReadFile(dest)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if bytes.HasPrefix(b, []byte(Marker)) {
		return nil
	}
	legacy := dest + ".legacy"
	if _, err := os.Stat(legacy); err == nil {
		legacy = fmt.Sprintf("%s.legacy.%d", dest, time.Now().Unix())
	}
	if err := os.Rename(dest, legacy); err != nil {
		return err
	}
	fmt.Printf("config: moved the existing %s to %s. It is generated from now on;\n"+
		"config: copy any custom settings from it into %s\n",
		filepath.Base(dest), filepath.Base(legacy), overridesName)
	return nil
}

// writeAtomic writes via a temp file and rename, owned like the directory it lands in, so the
// files on the host belong to the repo owner rather than root.
func writeAtomic(dest string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dest)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	if fi, err := os.Stat(dir); err == nil {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			_ = os.Chown(tmp.Name(), int(st.Uid), int(st.Gid))
		}
	}
	return os.Rename(tmp.Name(), dest)
}
