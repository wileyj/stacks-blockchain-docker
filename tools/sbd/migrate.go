package main

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// migratePlan brings an install from before the compose-first layout up to date without moving any
// data (refactor/14 §8). It edits .env and carries a hand-edited Config.toml's settings over; the rest
// are notes.
//   - COMPOSE_PROFILES: an old .env has none, and then `docker compose up -d` starts nothing but the
//     config job. It is set to node,api, plus signer when the old Config.toml had the signer on.
//   - The API and the node RPC stay on every interface, as the old layout published them (no host
//     address: 0.0.0.0; owner, 2026-11-23): API_BIND_ADDR / RPC_BIND_ADDR are set to 0.0.0.0 unless
//     .env sets them already. A new install gets 127.0.0.1; sbd doctor warns about either.
//   - A hand-edited Config.toml: the config job moves it aside on the next start, and `config suggest
//     --apply` writes the user's own settings to Config.overrides.toml (owner, 2026-11-18: applied, not
//     left for review; the config check stops a start the node would reject). With overrides of their
//     own already, it writes Config.overrides.toml.suggested beside them instead.
//   - A stack still running from the old layout (project compose-files) is stopped first, the node
//     first with its full shutdown time (owner, 2026-11-18): the new stack can't start beside it.
//   - Settings sample.env has and .env doesn't mention at all are appended, commented out with their
//     descriptions (owner, 2026-11-23): an upgraded .env never saw sample.env, so its optional settings
//     (API_BIND_ADDR, STACKS_P2P_ADDRESS, PROXY_TLS, ...) were only in the docs. Nothing changes until
//     one is uncommented, and a second run finds them all mentioned.
//   - Settings and data the new layout doesn't use are reported.
func migratePlan(c *Ctx, args []string) (Plan, error) {
	if len(args) > 0 {
		return nil, usagef("migrate: unexpected argument %q", args[0])
	}
	var p Plan
	note := func(s string) { p = append(p, Step{Note: s}) }
	if out, err := c.Docker.Output(legacyPSArgs); err == nil && len(bytes.TrimSpace(out)) > 0 {
		note("the old layout's stack (project compose-files) is running: stopping it, the node first")
		p = append(p, Step{Docker: []string{"stop", "-t", orDefault(c.Value("STACKS_SHUTDOWN_TIMEOUT"), "1200"), "stacks-blockchain"}},
			compose("-p", "compose-files", "down"))
	}
	if c.Env == nil {
		p = append(p, Step{CreateEnv: true})
	}
	net := c.Value("NETWORK")
	if err := checkNetwork(net); err != nil {
		return nil, err
	}
	confDir := filepath.Join(c.Dir, "conf", net)
	legacy := legacyConfig(confDir)

	if _, set := c.envGet("COMPOSE_PROFILES"); !set {
		profiles := "node,api"
		note("COMPOSE_PROFILES is missing from .env: without it `docker compose up -d` starts no services")
		var keep []string
		for _, k := range []string{"API_BIND_ADDR", "RPC_BIND_ADDR"} {
			if _, set := c.envGet(k); !set {
				keep = append(keep, k)
			}
		}
		if len(keep) > 0 {
			note("the old layout published the API (3999) and the node RPC (20443) on every interface: keeping that. " +
				"To serve them on this host only, set API_BIND_ADDR / RPC_BIND_ADDR to 127.0.0.1 (the default for new installs)")
		}
		if legacy != "" && signerWasOn(legacy) {
			if key := signerKeyVar(net); strings.TrimSpace(c.Value(key)) != "" {
				profiles += ",signer"
			} else {
				note("the old Config.toml had the signer on, but " + key + " is empty: the signer stays off (set the key, then sbd start --with signer)")
			}
		}
		p = append(p, Step{SetKey: "COMPOSE_PROFILES", SetValue: profiles})
		for _, k := range keep {
			p = append(p, Step{SetKey: k, SetValue: "0.0.0.0"})
		}
	}
	if c.Env != nil {
		if lines := newSettings(c, p); len(lines) > 0 {
			p = append(p, Step{AppendEnv: lines})
		}
	}
	for _, k := range sortedKeys(obsoleteKeys) {
		if _, set := c.envGet(k); set {
			note(k + " in .env is no longer used (" + obsoleteKeys[k] + "); you can delete it")
		}
	}

	_, err := os.Stat(filepath.Join(confDir, "Config.overrides.toml"))
	_, errSuggested := os.Stat(filepath.Join(confDir, "Config.overrides.toml.suggested"))
	switch {
	case legacy != "" && errSuggested == nil:
		note("review conf/" + net + "/Config.overrides.toml.suggested, then rename it to Config.overrides.toml (it holds your settings from " + filepath.Base(legacy) + ")")
	case legacy != "" && err == nil:
		note("conf/" + net + "/" + filepath.Base(legacy) + " was edited by hand, and you have a Config.overrides.toml: writing its settings to Config.overrides.toml.suggested to compare with yours")
		p = append(p, compose("run", "--rm", "config", "suggest"))
	case legacy != "":
		note("conf/" + net + "/" + filepath.Base(legacy) + " was edited by hand; Config.toml is generated now. Carrying your settings over to Config.overrides.toml")
		p = append(p, compose("run", "--rm", "config", "suggest", "--apply"))
	}

	dataRoot := c.Value("DATA_ROOT")
	if dataRoot == "" {
		dataRoot = "./persistent-data"
	}
	if !filepath.IsAbs(dataRoot) {
		dataRoot = filepath.Join(c.Dir, dataRoot)
	}
	if fi, err := os.Stat(filepath.Join(dataRoot, net, "event-replay")); err == nil && fi.IsDir() {
		note(filepath.Join(dataRoot, net, "event-replay") + " is no longer used (event replay was removed); delete it to free the space")
	}
	if len(p) == 0 {
		note("nothing to migrate")
	}
	return p, nil
}

// newSettings returns what to append to .env: each sample.env setting it doesn't mention, commented
// out, after the comment lines that describe it in sample.env; nil when there are none. Settings the
// plan already sets are left out.
func newSettings(c *Ctx, p Plan) []string {
	b, err := os.ReadFile(filepath.Join(c.Dir, "sample.env"))
	if err != nil {
		return nil
	}
	planned := map[string]bool{}
	for _, s := range p {
		planned[s.SetKey] = true
	}
	var out, desc []string
	descShown, afterKey := false, false
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		l := strings.TrimRight(sc.Text(), " \t")
		key := ""
		if m := envLineRe.FindStringSubmatch(l); m != nil {
			key, l = m[1], "# "+strings.TrimSpace(l)
		} else if m := commentedKeyRe.FindStringSubmatch(l); m != nil {
			key = m[1]
		}
		switch {
		case strings.TrimSpace(l) == "":
			desc, descShown, afterKey = nil, false, false
		case key == "":
			// A description, unless it's only a rule of #s; a new one starts after a setting
			if afterKey {
				desc, descShown, afterKey = nil, false, false
			}
			if strings.Trim(l, "# ") != "" {
				desc = append(desc, l)
			}
		default:
			afterKey = true
			if planned[key] || c.Env.Mentions(key) {
				continue
			}
			if !descShown {
				if len(out) > 0 {
					out = append(out, "")
				}
				out = append(out, desc...)
				descShown = true
			}
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		return nil
	}
	head := []string{"", "###############################",
		"## Added by sbd migrate (" + now().Format("2006-01-02") + "): settings this .env didn't list, from sample.env.",
		"## All optional and commented out, so nothing changes: uncomment one to use it.", ""}
	return append(head, out...)
}

// legacyPSArgs lists the running containers of the old layout, whose compose files lived in compose-files/.
var legacyPSArgs = []string{"ps", "-q", "--filter", "label=com.docker.compose.project=compose-files"}

// obsoleteKeys are .env settings that nothing reads any more, with what replaced them.
var obsoleteKeys = map[string]string{
	"STACKS_EXPORT_EVENTS_FILE": "event replay was removed",
	"EXPOSE_POSTGRES":           "use POSTGRES_LOCAL=1, which publishes postgres on 127.0.0.1 only",
	"METRICS_BIND_ADDR":         "metrics are published on 127.0.0.1 only; use an SSH tunnel from elsewhere",
	"MONITORING_BIND":           "Prometheus and Grafana are published on 127.0.0.1 only; use an SSH tunnel from elsewhere",
	"NGINX_PROXY_PORT":          "the proxy is Caddy now: set PROXY_HTTP_PORT instead",
	"VERBOSE":                   "manage.sh's output switch; sbd and Compose don't read it",
}

// envGet reads .env only (not the sample.env fallback): whether a setting is really there.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (c *Ctx) envGet(key string) (string, bool) {
	if c.Env == nil {
		return "", false
	}
	return c.Env.Get(key)
}

// legacyConfig returns the hand-edited Config.toml, as the config job finds it: the file itself when
// the job didn't write it, else a Config.toml.legacy* it moved aside.
func legacyConfig(dir string) string {
	path := filepath.Join(dir, "Config.toml")
	if b, err := os.ReadFile(path); err == nil && !bytes.HasPrefix(b, []byte("# Generated by the config job")) {
		return path
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "Config.toml.legacy*")); len(m) > 0 {
		return m[len(m)-1]
	}
	return ""
}

var stackerOn = regexp.MustCompile(`^\s*stacker\s*=\s*true\b`)

// signerWasOn: the old manage.sh turned the signer on by setting `stacker = true` in [node].
func signerWasOn(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		if line := sc.Text(); !strings.HasPrefix(strings.TrimSpace(line), "#") && stackerOn.MatchString(line) {
			return true
		}
	}
	return false
}
