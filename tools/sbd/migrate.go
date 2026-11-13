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
// data (refactor/14 §8). It only edits .env and writes a suggestion file; everything else is a note.
//   - COMPOSE_PROFILES: an old .env has none, and then `docker compose up -d` starts nothing but the
//     config job. It is set to node,api, plus signer when the old Config.toml had the signer on.
//   - A hand-edited Config.toml: the config job moves it aside on the next start; `config suggest`
//     writes Config.overrides.toml.suggested with the user's own settings, for review.
//   - Settings, containers and data the new layout doesn't use are reported.
func migratePlan(c *Ctx, args []string) (Plan, error) {
	if len(args) > 0 {
		return nil, usagef("migrate: unexpected argument %q", args[0])
	}
	var p Plan
	note := func(s string) { p = append(p, Step{Note: s}) }
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
		note("the API (3999) and the node RPC (20443) now listen on this host only: to serve the API to other machines, add proxy to COMPOSE_PROFILES (or set API_BIND_ADDR / RPC_BIND_ADDR)")
		if legacy != "" && signerWasOn(legacy) {
			if key := signerKeyVar(net); strings.TrimSpace(c.Value(key)) != "" {
				profiles += ",signer"
			} else {
				note("the old Config.toml had the signer on, but " + key + " is empty: the signer stays off (set the key, then sbd start --with signer)")
			}
		}
		p = append(p, Step{SetKey: "COMPOSE_PROFILES", SetValue: profiles})
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
		note("conf/" + net + "/" + filepath.Base(legacy) + " is a hand-edited config, and Config.overrides.toml exists already: compare them by hand")
	case legacy != "":
		note("conf/" + net + "/" + filepath.Base(legacy) + " was edited by hand; Config.toml is generated now. Writing your settings to Config.overrides.toml.suggested for review")
		p = append(p, compose("run", "--rm", "config", "suggest"))
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
	if out, err := c.Docker.Output([]string{"ps", "-q", "--filter", "label=com.docker.compose.project=compose-files"}); err == nil && len(bytes.TrimSpace(out)) > 0 {
		note("containers from the old compose-files layout are running: stop them with ./manage.sh -n " + net + " -a stop before starting this stack")
	}
	if len(p) == 0 {
		note("nothing to migrate")
	}
	return p, nil
}

// obsoleteKeys are .env settings that nothing reads any more, with what replaced them.
var obsoleteKeys = map[string]string{
	"STACKS_EXPORT_EVENTS_FILE": "event replay was removed",
	"EXPOSE_POSTGRES":           "use POSTGRES_LOCAL=1, which publishes postgres on 127.0.0.1 only",
	"METRICS_BIND_ADDR":         "metrics are published on 127.0.0.1 only; use an SSH tunnel from elsewhere",
	"MONITORING_BIND":           "Prometheus and Grafana are published on 127.0.0.1 only; use an SSH tunnel from elsewhere",
	"NGINX_PROXY_PORT":          "the proxy is Caddy now: set PROXY_HTTP_PORT instead",
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
