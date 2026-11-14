package main

import (
	"flag"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// `sbd update <service>` replaces one running service with its pinned (or a new) image and leaves the
// rest running (owner, 2026-11-13). Meant for the API and the signer, which get releases more often than
// the node: the node holds its tip while an event observer is away and catches up when it is back
// (refactor/15: 162 s after a 15-min API outage, at the backoff cap). Postgres isn't offered: a new major
// is `sbd db upgrade`.

func init() {
	Commands = append(Commands, &Command{Name: "update", Usage: "update <node|api|signer|proxy> [--version V]",
		Help: "Pull and recreate one service, the others keep running (--version sets its pin in .env first)",
		Plan: updatePlan, Guard: updateGuard})
}

type updatable struct {
	service, pin, note string
}

var updatables = map[string]updatable{
	"node": {"stacks-blockchain", "STACKS_BLOCKCHAIN_VERSION",
		"the node stops (up to STACKS_SHUTDOWN_TIMEOUT) and restarts; the API and signer wait for it"},
	"api": {"stacks-blockchain-api", "STACKS_BLOCKCHAIN_API_VERSION",
		"the node holds its tip while the API restarts (a database migration can take minutes), then catches up"},
	"signer": {"stacks-signer", "STACKS_SIGNER_VERSION",
		"the signer misses signatures while it restarts"},
	"proxy": {"proxy", "CADDY_VERSION",
		"the API is unreachable through the proxy while it restarts"},
}

var imageTagRe = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._-]{0,127}$`)

func updatePlan(c *Ctx, args []string) (Plan, error) {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	version := fs.String("version", "", "")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	names := slices.Sorted(func(yield func(string) bool) {
		for k := range updatables {
			if !yield(k) {
				return
			}
		}
	})
	if len(pos) != 1 {
		return nil, usagef("usage: sbd update <%s> [--version V]", strings.Join(names, "|"))
	}
	u, ok := updatables[pos[0]]
	if !ok {
		if pos[0] == "postgres" {
			return nil, usagef("postgres isn't updated this way: a new major is sbd db upgrade (docs/upgrade.md)")
		}
		return nil, usagef("update: unknown service %q (one of %s)", pos[0], strings.Join(names, ", "))
	}
	var p Plan
	if v := strings.TrimSpace(*version); v != "" {
		if !imageTagRe.MatchString(v) {
			return nil, usagef("update: %q isn't an image tag", v)
		}
		// 4.0.6+ signers rely on node endpoints only 4.0.6+ nodes have: the node goes first (docs/upgrade.md)
		if pos[0] == "signer" && newer(v, c.Value("STACKS_BLOCKCHAIN_VERSION")) {
			return nil, usagef("update: signer %s is newer than the node (%s): update the node first (sbd update node --version %s)",
				v, c.Value("STACKS_BLOCKCHAIN_VERSION"), v)
		}
		if v != c.Value(u.pin) {
			p = append(p, Step{SetKey: u.pin, SetValue: v})
		}
	}
	return append(p,
		Step{Note: u.note},
		compose("pull", u.service),
		compose("run", "--rm", "config"),
		compose("up", "-d", "--no-deps", "--force-recreate", u.service)), nil
}

// updateGuard: on a stopped stack, `up --no-deps` would start that one service alone.
func updateGuard(c *Ctx, args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.String("version", "", "")
	pos, err := parseFlags(fs, args)
	if err != nil || len(pos) != 1 {
		return err // the plan already refused bad arguments
	}
	svc := updatables[pos[0]].service
	up, err := running(c.Docker)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(up, func(ct Container) bool { return ct.Service == svc }) {
		return fmt.Errorf("%s isn't running: set the version (sbd env set) and use sbd start, which starts everything", svc)
	}
	return nil
}

// newer reports whether dotted version a is newer than b; a non-numeric part compares as 0.
func newer(a, b string) bool {
	pa, pb := strings.Split(strings.TrimPrefix(a, "v"), "."), strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}
