package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
)

// statusRun is `docker compose ps` with the network and profiles, and the exit status scripts poll:
// 0 = running, 1 = not running (refactor/11 §A.4).
func statusRun(c *Ctx, args []string) (int, error) {
	if len(args) > 0 && args[0] != "--json" {
		return 2, usagef("status: unexpected argument %q", args[0])
	}
	asJSON := c.JSON || len(args) > 0
	cs, err := containers(c.Docker)
	if err != nil {
		return 1, err
	}
	up := false
	for _, ct := range cs {
		if ct.State == "running" {
			up = true
		}
	}
	if asJSON {
		if cs == nil {
			cs = []Container{}
		}
		enc := json.NewEncoder(c.Out)
		enc.SetIndent("", "  ")
		err = enc.Encode(map[string]any{
			"network": c.Value("NETWORK"), "profiles": List(c.Value("COMPOSE_PROFILES")),
			"running": up, "containers": cs,
		})
	} else {
		state := "not running"
		if up {
			state = "running"
		}
		fmt.Fprintf(c.Out, "%s (%s): %s\n", c.Value("NETWORK"), c.Value("COMPOSE_PROFILES"), state)
		if len(cs) > 0 {
			tw := tabwriter.NewWriter(c.Out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "SERVICE\tCONTAINER\tSTATE\tHEALTH\tSTATUS")
			for _, ct := range cs {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", ct.Service, ct.Name, ct.State, orDash(ct.Health), ct.Status)
			}
			tw.Flush()
		}
	}
	if err != nil {
		return 1, err
	}
	if !up {
		return 1, nil
	}
	return 0, nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// Check is one doctor finding.
type Check struct {
	Level  string `json:"level"` // ok, warn, fail
	What   string `json:"what"`
	Detail string `json:"detail,omitempty"`
}

// Compose versions (refactor/14 §5): configs.content and config mount targets need 2.24.0; 2.27.2
// fixed COMPOSE_PROFILES from .env.
var (
	minCompose         = [3]int{2, 24, 0}
	recommendedCompose = [3]int{2, 27, 2}
	versionRe          = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)
)

func parseVersion(s string) ([3]int, bool) {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return [3]int{}, false
	}
	var v [3]int
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, true
}

func less(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// Doctor runs the preflight checks (refactor/14 R6). It never changes anything.
func Doctor(c *Ctx) []Check {
	var checks []Check
	add := func(level, what, detail string) { checks = append(checks, Check{level, what, detail}) }

	out, err := c.Docker.Output([]string{"compose", "version", "--short"})
	switch v, ok := parseVersion(string(out)); {
	case err != nil:
		add("fail", "docker compose", "not found: install Docker with the Compose v2 plugin")
		return checks
	case !ok:
		add("warn", "docker compose", "unrecognised version "+strings.TrimSpace(string(out)))
	case less(v, minCompose):
		add("fail", "docker compose", fmt.Sprintf("%s is too old: 2.24.0 or newer is required", strings.TrimSpace(string(out))))
	case less(v, recommendedCompose):
		add("warn", "docker compose", fmt.Sprintf("%s works, but 2.27.2 or newer is recommended (COMPOSE_PROFILES fixes)", strings.TrimSpace(string(out))))
	default:
		add("ok", "docker compose", strings.TrimSpace(string(out)))
	}
	if out, err := c.Docker.Output([]string{"info", "--format", "{{.ServerVersion}}"}); err != nil {
		add("fail", "docker engine", "not reachable: is the Docker daemon running (or DOCKER_HOST correct)?")
		return checks
	} else {
		add("ok", "docker engine", strings.TrimSpace(string(out)))
	}

	if c.Env == nil {
		add("warn", ".env", "missing: sbd start creates it from sample.env (or cp sample.env .env)")
	} else {
		add("ok", ".env", c.Env.Path)
	}
	net := c.Value("NETWORK")
	if err := checkNetwork(net); err != nil {
		add("fail", "NETWORK", err.Error())
	} else {
		add("ok", "NETWORK", net)
	}
	profiles := List(c.Value("COMPOSE_PROFILES"))
	if err := checkProfiles(profiles); err != nil {
		add("fail", "COMPOSE_PROFILES", err.Error())
	} else {
		add("ok", "COMPOSE_PROFILES", strings.Join(profiles, ","))
	}
	if slices.Contains(profiles, "signer") {
		if key := signerKeyVar(net); strings.TrimSpace(c.Value(key)) == "" {
			add("fail", "signer key", key+" is empty, but the signer is enabled")
		}
	}
	if out, err := c.Docker.Output([]string{"compose", "config", "--quiet"}); err != nil {
		add("fail", "compose project", strings.TrimSpace(string(out))+" "+err.Error())
	} else {
		add("ok", "compose project", "valid")
	}

	// Removing a profile from COMPOSE_PROFILES doesn't stop its containers (refactor/14 §2.1)
	if up, err := running(c.Docker); err == nil && len(up) > 0 {
		if svc, err := Services(c.Docker); err == nil {
			var stray []string
			for _, ct := range up {
				ps := svc[ct.Service]
				if len(ps) > 0 && !anyIn(ps, profiles) {
					stray = append(stray, ct.Name)
				}
			}
			if len(stray) > 0 {
				add("warn", "running services", strings.Join(stray, ", ")+" running but not in COMPOSE_PROFILES: sbd stop stops them")
			} else {
				add("ok", "running services", names(up))
			}
		}
	}
	return checks
}

func doctorRun(c *Ctx, args []string) (int, error) {
	if len(args) > 0 && args[0] != "--json" {
		return 2, usagef("doctor: unexpected argument %q", args[0])
	}
	checks := Doctor(c)
	failed := false
	for _, ch := range checks {
		failed = failed || ch.Level == "fail"
	}
	if c.JSON || len(args) > 0 {
		enc := json.NewEncoder(c.Out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(checks); err != nil {
			return 1, err
		}
	} else {
		for _, ch := range checks {
			fmt.Fprintf(c.Out, "%-4s  %-18s %s\n", strings.ToUpper(ch.Level), ch.What, ch.Detail)
		}
	}
	if failed {
		return 1, nil
	}
	return 0, nil
}

func anyIn(a, b []string) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}
