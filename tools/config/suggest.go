package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// `config suggest` (run by `sbd migrate`) helps move a hand-edited Config.toml to the rendered model:
// it compares the legacy file with what this job renders now and writes the differences to
// Config.overrides.toml.suggested, for the user to review. It never writes Config.overrides.toml.
//
// Not carried over: keys the upstream network definition sets (bootstrap node, epochs, balances,
// conf/upstream/; refactor/11 Q27), since an old copy of those is what broke testnet (D50), and the
// [[events_observer]] entries this job renders itself. Keys the new config doesn't have at all are
// included but listed in the header: the node rejects unknown keys (config-check), so check each one.
// Keys rendered from .env aren't carried over either (an override would pin them): they are reported
// with the .env variable to set instead.

const suggestedName = "Config.overrides.toml.suggested"

// Suggestion is the result of comparing a legacy Config.toml with the rendered one.
type Suggestion struct {
	Out     []byte   // the suggested overrides file; nil when there is nothing to carry over
	Added   []string // keys the new config doesn't have
	Skipped []string // network definition keys that differ (not carried over)
	FromEnv []string // differing keys that are rendered from .env: "key: set VAR in .env"
}

// envKeys are the keys the renderer sets from .env (RenderNode), with the variable that sets them.
func envKeys(network string) map[string]string {
	btc := "BTC_"
	if network == "testnet" {
		btc = "TBTC_"
	}
	return map[string]string{
		"node.stacker": "COMPOSE_PROFILES (signer)", "node.prometheus_bind": "NODE_METRICS_PORT",
		"node.working_dir": "(fixed by this repo)", "burnchain.peer_host": btc + "HOST",
		"burnchain.rpc_port": btc + "RPC_PORT", "burnchain.peer_port": btc + "P2P_PORT",
		"burnchain.username": btc + "RPC_USER", "burnchain.password": btc + "RPC_PASS",
		"connection_options.auth_token": "AUTH_TOKEN",
	}
}

func runSuggest(confRoot string, getenv func(string) string) error {
	s, err := SettingsFromEnv(getenv)
	if err != nil {
		return err
	}
	dir := filepath.Join(confRoot, s.Network)
	legacyPath, legacy, err := findLegacy(dir)
	if err != nil {
		return err
	}
	if legacyPath == "" {
		fmt.Printf("config: %s/Config.toml is already generated; nothing to migrate\n", s.Network)
		return nil
	}
	tmpl, err := os.ReadFile(filepath.Join(dir, "Config.toml.sample"))
	if err != nil {
		return fmt.Errorf("reading template: %w", err)
	}
	upstream, err := os.ReadFile(filepath.Join(confRoot, "upstream", s.Network+"-follower-conf.toml"))
	if err != nil {
		return fmt.Errorf("reading the upstream network definition: %w", err)
	}
	sg, err := Suggest(legacy, tmpl, upstream, s)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(legacyPath), err)
	}
	rel := filepath.Join(s.Network, filepath.Base(legacyPath))
	if sg.Out == nil {
		fmt.Printf("config: %s has no settings of its own beyond what is rendered now; nothing to carry over\n", rel)
	} else {
		if err := writeAtomic(filepath.Join(dir, suggestedName), sg.Out, 0o644); err != nil {
			return err
		}
		fmt.Printf("config: wrote %s from %s. Review it, then rename it to Config.overrides.toml\n", filepath.Join(s.Network, suggestedName), rel)
	}
	for _, k := range sg.Added {
		fmt.Printf("config:   check %s: not in the new config (the node rejects unknown keys)\n", k)
	}
	for _, k := range sg.FromEnv {
		fmt.Printf("config:   differs: %s\n", k)
	}
	for _, k := range sg.Skipped {
		fmt.Printf("config:   not carried over: %s (network definition, from conf/upstream/)\n", k)
	}
	return nil
}

// findLegacy returns the hand-edited Config.toml: the file itself if the renderer didn't write it,
// else the newest Config.toml.legacy* the renderer moved aside.
func findLegacy(dir string) (string, []byte, error) {
	path := filepath.Join(dir, "Config.toml")
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", nil, err
	}
	if err == nil && !bytes.HasPrefix(b, []byte(Marker)) {
		return path, b, nil
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "Config.toml.legacy*"))
	sort.Slice(matches, func(i, j int) bool {
		a, _ := os.Stat(matches[i])
		b, _ := os.Stat(matches[j])
		return a.ModTime().After(b.ModTime())
	})
	if len(matches) == 0 {
		return "", nil, nil
	}
	b, err = os.ReadFile(matches[0])
	return matches[0], b, err
}

// Suggest returns the overrides that turn today's rendered config into the legacy one, minus the
// network definition, the keys rendered from .env and the rendered observers.
func Suggest(legacy, tmpl, upstream []byte, s Settings) (Suggestion, error) {
	var sg Suggestion
	old, err := parse(legacy, "the legacy Config.toml")
	if err != nil {
		return sg, err
	}
	rendered, err := RenderNode(tmpl, nil, s, "")
	if err != nil {
		return sg, err
	}
	base, err := parse(rendered, "the rendered config")
	if err != nil {
		return sg, err
	}
	network, err := parse(upstream, "the upstream network definition")
	if err != nil {
		return sg, err
	}
	over := map[string]any{}
	d := differ{env: envKeys(s.Network), sg: &sg}
	d.diff(old, base, network, "", over)

	// Observers the user added (a custom indexer, say); the API/signer ones are rendered from COMPOSE_PROFILES
	renderedEndpoints := map[string]bool{}
	for _, o := range s.Observers() {
		renderedEndpoints[o.(map[string]any)["endpoint"].(string)] = true
	}
	if obs, ok := old["events_observer"].([]any); ok {
		var extra []any
		for _, o := range obs {
			if m, ok := o.(map[string]any); ok && !renderedEndpoints[fmt.Sprint(m["endpoint"])] && !isStockObserver(fmt.Sprint(m["endpoint"])) {
				extra = append(extra, m)
			}
		}
		if len(extra) > 0 {
			over["events_observer"] = extra
		}
	}
	if len(over) == 0 {
		return sg, nil
	}
	body, err := toml.Marshal(over)
	if err != nil {
		return sg, err
	}
	var hdr strings.Builder
	hdr.WriteString("# Suggested by `config suggest` (sbd migrate) from your previous Config.toml: the settings\n")
	hdr.WriteString("# that differ from what is rendered now. Review, then rename to Config.overrides.toml.\n")
	if len(sg.Added) > 0 {
		hdr.WriteString("# Not in the new config, check each (the node rejects unknown keys):\n")
		for _, k := range sg.Added {
			hdr.WriteString("#   " + k + "\n")
		}
	}
	sg.Out = append([]byte(hdr.String()+"\n"), body...)
	return sg, nil
}

// isStockObserver: the API and signer observers older versions of this repo shipped in the sample
// (any host, the default ports), which the rendered config replaces.
func isStockObserver(endpoint string) bool {
	for _, suffix := range []string{":3700", ":30000"} {
		if strings.HasSuffix(endpoint, suffix) {
			return true
		}
	}
	return false
}

type differ struct {
	env map[string]string
	sg  *Suggestion
}

// diff adds to over every value in old that base lacks or has differently, recursing into tables.
// Keys the upstream network definition sets are skipped; arrays of tables other than observers are
// network data (epochs, balances) and skipped too. Keys rendered from .env are reported instead.
func (d differ) diff(old, base, network map[string]any, prefix string, over map[string]any) {
	keys := make([]string, 0, len(old))
	for k := range old {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		path := prefix + k
		ov := old[k]
		if path == "events_observer" {
			continue
		}
		if v, ok := d.env[path]; ok {
			if !sameValue(ov, base[k]) {
				d.sg.FromEnv = append(d.sg.FromEnv, fmt.Sprintf("%s = %v (now %v): set %s in .env if you still need it", path, ov, orNone(base[k]), v))
			}
			continue
		}
		if _, isNet := network[k]; isNet && !isTable(ov) {
			if !sameValue(ov, base[k]) {
				d.sg.Skipped = append(d.sg.Skipped, path)
			}
			continue
		}
		if isArrayOfTables(ov) {
			if !reflect.DeepEqual(ov, base[k]) {
				d.sg.Skipped = append(d.sg.Skipped, path)
			}
			continue
		}
		if om, ok := ov.(map[string]any); ok {
			bm, _ := base[k].(map[string]any)
			nm, _ := network[k].(map[string]any)
			sub := map[string]any{}
			d.diff(om, orEmpty(bm), orEmpty(nm), path+".", sub)
			if len(sub) > 0 {
				over[k] = sub
			}
			continue
		}
		bv, inBase := base[k]
		if inBase && sameValue(ov, bv) {
			continue
		}
		if !inBase {
			d.sg.Added = append(d.sg.Added, path)
		}
		over[k] = ov
	}
}

// sameValue compares TOML values, treating the integer types go-toml returns alike.
func sameValue(a, b any) bool {
	if ai, ok := asInt(a); ok {
		bi, ok := asInt(b)
		return ok && ai == bi
	}
	return reflect.DeepEqual(a, b)
}

func asInt(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case uint64:
		return int64(n), true
	}
	return 0, false
}

func orNone(v any) any {
	if v == nil {
		return "unset"
	}
	return v
}

func isTable(v any) bool { _, ok := v.(map[string]any); return ok }

func isArrayOfTables(v any) bool {
	a, ok := v.([]any)
	if !ok || len(a) == 0 {
		return false
	}
	_, ok = a[0].(map[string]any)
	return ok
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
