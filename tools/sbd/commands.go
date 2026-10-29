package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
)

// Ctx is what a command sees: the project, its .env (nil when there is none yet; sample.env then
// supplies the defaults), docker, and the global options.
type Ctx struct {
	Dir    string
	Env    *Env
	Sample *Env
	Docker Docker
	Yes    bool
	JSON   bool
	Out    io.Writer
}

// Value reads a setting from .env, falling back to sample.env (what `sbd start` would create).
func (c *Ctx) Value(key string) string {
	if c.Env != nil {
		v, _ := c.Env.Get(key)
		return v
	}
	if c.Sample != nil {
		v, _ := c.Sample.Get(key)
		return v
	}
	return ""
}

type Command struct {
	Name    string
	Aliases []string
	Usage   string
	Help    string
	// Plan builds the steps from the arguments and .env (only `logs export` also lists containers), so
	// `sbd plan` shows exactly what will run; Guard checks the live state just before the steps run.
	Plan  func(c *Ctx, args []string) (Plan, error)
	Guard func(c *Ctx, args []string) error
	// Run is for commands that report instead of change (status, doctor, env get).
	Run func(c *Ctx, args []string) (int, error)
}

// usageError exits 2 (refactor/11 Q1); everything else that fails exits 1.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usagef(format string, a ...any) error { return usageError{fmt.Sprintf(format, a...)} }

var (
	Networks = []string{"mainnet", "testnet"}
	// Profiles are the long-running services a user enables in COMPOSE_PROFILES; job profiles (seed,
	// reset, bns) are launched with `docker compose run` instead.
	Profiles = []string{"node", "api", "signer", "proxy", "monitoring"}
)

func signerKeyVar(network string) string {
	if network == "testnet" {
		return "TESTNET_SIGNER_PRIVATE_KEY"
	}
	return "SIGNER_PRIVATE_KEY"
}

func checkNetwork(n string) error {
	if !slices.Contains(Networks, n) {
		return usagef("unknown network %q (supported: %s)", n, strings.Join(Networks, ", "))
	}
	return nil
}

func checkProfiles(ps []string) error {
	if len(ps) == 0 {
		return usagef("no services selected: COMPOSE_PROFILES must list at least one of %s", strings.Join(Profiles, ", "))
	}
	for _, p := range ps {
		if !slices.Contains(Profiles, p) {
			return usagef("unknown service set %q (supported: %s)", p, strings.Join(Profiles, ", "))
		}
	}
	return nil
}

// parseFlags parses fs from args, allowing flags and positional arguments in any order.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, usagef("%s: %v", fs.Name(), err)
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		if args[0] == "--" {
			return append(pos, args[1:]...), nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, List(v)...); return nil }

// refuseIfRunning is the CLI's friendly form of the guard the jobs carry themselves (refactor/14 R5).
func refuseIfRunning(what string) func(c *Ctx, args []string) error {
	return func(c *Ctx, _ []string) error {
		up, err := running(c.Docker)
		if err != nil {
			return err
		}
		if len(up) > 0 {
			return fmt.Errorf("%s needs the stack stopped (%s network; running: %s). Stop it first: sbd stop",
				what, c.Value("NETWORK"), names(up))
		}
		return nil
	}
}

func names(cs []Container) string {
	var n []string
	for _, c := range cs {
		n = append(n, c.Name)
	}
	sort.Strings(n)
	return strings.Join(n, ", ")
}

// startPlan selects the network and services (persisted in .env, refactor/14 R2) and starts them.
func startPlan(c *Ctx, args []string) (Plan, error) {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	network := fs.String("network", "", "")
	var with, without, profiles listFlag
	fs.Var(&with, "with", "")
	fs.Var(&without, "without", "")
	fs.Var(&profiles, "profiles", "")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) > 0 {
		return nil, usagef("start: unexpected argument %q", pos[0])
	}
	var p Plan
	if c.Env == nil {
		p = append(p, Step{CreateEnv: true})
	}
	net := c.Value("NETWORK")
	if *network != "" {
		net = strings.ToLower(*network)
	}
	if err := checkNetwork(net); err != nil {
		return nil, err
	}
	current := c.Value("COMPOSE_PROFILES")
	sel := List(current)
	if len(profiles) > 0 {
		sel = profiles
	}
	for _, w := range with {
		if !slices.Contains(sel, w) {
			sel = append(sel, w)
		}
	}
	sel = slices.DeleteFunc(sel, func(s string) bool { return slices.Contains(without, s) })
	if err := checkProfiles(sel); err != nil {
		return nil, err
	}
	if slices.Contains(sel, "signer") {
		key := signerKeyVar(net)
		if strings.TrimSpace(c.Value(key)) == "" {
			return nil, usagef("the signer needs %s set in .env (sbd env set %s=<hex key>)", key, key)
		}
	}
	if net != c.Value("NETWORK") {
		p = append(p, Step{SetKey: "NETWORK", SetValue: net})
	}
	if joined := strings.Join(sel, ","); joined != current {
		p = append(p, Step{SetKey: "COMPOSE_PROFILES", SetValue: joined})
	}
	return append(p, compose("up", "-d")), nil
}

// stopStep stops every service, also ones dropped from COMPOSE_PROFILES since they started. No -t:
// it would override the node's stop_grace_period (STACKS_SHUTDOWN_TIMEOUT).
var stopStep = compose("--profile", "*", "down")

func jobPlan(service string, passYes bool) func(c *Ctx, args []string) (Plan, error) {
	return func(c *Ctx, args []string) (Plan, error) {
		a := append([]string{"run", "--rm", service}, args...)
		if passYes && c.Yes && !slices.Contains(args, "--yes") {
			a = append(a, "--yes")
		}
		return Plan{compose(a...)}, nil
	}
}

func noArgs(name string, p Plan) func(c *Ctx, args []string) (Plan, error) {
	return func(c *Ctx, args []string) (Plan, error) {
		if len(args) > 0 {
			return nil, usagef("%s: unexpected argument %q", name, args[0])
		}
		return p, nil
	}
}

func logsPlan(c *Ctx, args []string) (Plan, error) {
	if len(args) > 0 && args[0] == "export" {
		return logsExportPlan(c, args[1:])
	}
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	tail := fs.String("tail", "100", "")
	noFollow := fs.Bool("no-follow", false, "")
	services, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	a := []string{"logs"}
	if !*noFollow {
		a = append(a, "-f")
	}
	a = append(a, "--tail", *tail)
	return Plan{compose(append(a, services...)...)}, nil
}

// logsExportPlan writes each container's full log to exported-logs/<container>.log (refactor/11
// §A.17). It lists the containers, so unlike the other plans it reads docker state.
func logsExportPlan(c *Ctx, args []string) (Plan, error) {
	if len(args) > 0 {
		return nil, usagef("logs export: unexpected argument %q", args[0])
	}
	cs, err := containers(c.Docker)
	if err != nil {
		return nil, err
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].Name < cs[j].Name })
	var p Plan
	seen := map[string]bool{}
	for _, ct := range cs {
		if seen[ct.Service] {
			continue
		}
		seen[ct.Service] = true
		s := compose("--profile", "*", "logs", "--no-color", "--timestamps", ct.Service)
		s.Stdout = "exported-logs/" + ct.Name + ".log"
		p = append(p, s)
	}
	if len(p) == 0 {
		return nil, errors.New("logs export: the project has no containers")
	}
	return p, nil
}

func configPlan(c *Ctx, args []string) (Plan, error) {
	if len(args) != 1 || args[0] != "render" {
		return nil, usagef("usage: sbd config render")
	}
	return Plan{compose("run", "--rm", "config")}, nil
}

func envRun(c *Ctx, args []string) (int, error) {
	if len(args) == 0 || args[0] != "get" {
		return 2, usagef("usage: sbd env get [KEY...] | sbd env set KEY=VALUE...")
	}
	if c.Env == nil {
		_, err := mustEnv(c.Dir)
		return 1, err
	}
	keys := args[1:]
	if len(keys) == 0 {
		keys = []string{"NETWORK", "COMPOSE_PROFILES"}
	}
	for _, k := range keys {
		v, _ := c.Env.Get(k)
		if len(args) == 2 {
			fmt.Fprintln(c.Out, v)
		} else {
			fmt.Fprintf(c.Out, "%s=%s\n", k, v)
		}
	}
	return 0, nil
}

func envSetPlan(c *Ctx, args []string) (Plan, error) {
	if len(args) == 0 {
		return nil, usagef("usage: sbd env set KEY=VALUE...")
	}
	var p Plan
	if c.Env == nil {
		p = append(p, Step{CreateEnv: true})
	}
	for _, a := range args {
		k, v, ok := strings.Cut(a, "=")
		if !ok || !envLineRe.MatchString(k+"=") {
			return nil, usagef("env set: %q is not KEY=VALUE", a)
		}
		switch k {
		case "NETWORK":
			v = strings.ToLower(v)
			if err := checkNetwork(v); err != nil {
				return nil, err
			}
		case "COMPOSE_PROFILES":
			ps := List(v)
			if err := checkProfiles(ps); err != nil {
				return nil, err
			}
			v = strings.Join(ps, ",")
		}
		p = append(p, Step{SetKey: k, SetValue: v})
	}
	return p, nil
}

// Commands is the CLI. Every one that changes something is a Plan of documented compose commands
// (docs/usage.md, checked by TestPlansAreDocumented).
var Commands = []*Command{
	{Name: "start", Aliases: []string{"up"}, Usage: "start [--network mainnet|testnet] [--with svc,...] [--without svc,...] [--profiles svc,...]",
		Help: "Start the services in COMPOSE_PROFILES (the options update NETWORK/COMPOSE_PROFILES in .env first)",
		Plan: startPlan, Guard: func(c *Ctx, _ []string) error {
			up, err := running(c.Docker)
			if err != nil {
				return err
			}
			if len(up) > 0 {
				return fmt.Errorf("already running (%s). To change networks or services: sbd stop, then sbd start with the new options", names(up))
			}
			return nil
		}},
	{Name: "stop", Aliases: []string{"down"}, Usage: "stop", Help: "Stop everything gracefully (node first, up to STACKS_SHUTDOWN_TIMEOUT)",
		Plan: noArgs("stop", Plan{stopStep})},
	{Name: "restart", Usage: "restart [start options]", Help: "Stop, then start (accepts the start options)",
		Plan: func(c *Ctx, args []string) (Plan, error) {
			p, err := startPlan(c, args)
			if err != nil {
				return nil, err
			}
			return append(Plan{stopStep}, p...), nil
		}},
	{Name: "status", Usage: "status [--json]", Help: "Show the containers; exit 0 if running, 1 if not", Run: statusRun},
	{Name: "logs", Aliases: []string{"log"}, Usage: "logs [service...] [--tail N] [--no-follow] | logs export",
		Help: "Follow the logs, or write every container's log to exported-logs/", Plan: logsPlan},
	{Name: "pull", Aliases: []string{"upgrade"}, Usage: "pull", Help: "Pull the images pinned in .env (then sbd restart to use them)",
		Plan: noArgs("pull", Plan{compose("pull", "--ignore-buildable")})},
	{Name: "seed", Usage: "seed [seed job options, e.g. --dry-run, --chainstate F --pgdump F]",
		Help: "Restore chainstate and API database from the Hiro archive (stack must be stopped)",
		Plan: jobPlan("seed", true), Guard: refuseIfRunning("seed")},
	{Name: "reset", Usage: "reset", Help: "Delete this network's chainstate and API database (stack must be stopped)",
		Plan: func(c *Ctx, args []string) (Plan, error) {
			if len(args) > 0 {
				return nil, usagef("reset: unexpected argument %q (use the global --yes)", args[0])
			}
			return jobPlan("reset", true)(c, nil)
		}, Guard: refuseIfRunning("reset")},
	{Name: "bns", Usage: "bns", Help: "Download the legacy BNS data (stack must be stopped)",
		Plan: noArgs("bns", Plan{compose("run", "--rm", "bns")}), Guard: refuseIfRunning("bns")},
	{Name: "config", Usage: "config render", Help: "Render Config.toml/Signer.toml now (start does this too)", Plan: configPlan},
	{Name: "env", Usage: "env get [KEY...] | env set KEY=VALUE...", Help: "Read or change settings in .env",
		Run: envRun},
	{Name: "doctor", Usage: "doctor [--json]", Help: "Check Docker, Compose, .env and the running services", Run: doctorRun},
}

// envSet is `env set`, dispatched from `env` because it is a plan while `env get` is a report.
var envSet = &Command{Name: "env set", Plan: envSetPlan}

func lookup(name string) *Command {
	for _, c := range Commands {
		if c.Name == name || slices.Contains(c.Aliases, name) {
			return c
		}
	}
	return nil
}
