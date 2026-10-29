// sbd is the optional helper CLI for stacks-blockchain-docker. The repo works with plain `docker
// compose`; every sbd command is a documented compose command plus .env edits (refactor/14 §2.1 and
// §11), which `sbd plan <command>` prints instead of running. sbd keeps no state of its own: .env is
// the only settings file, shared with Compose.
//
// Exit codes (refactor/11 Q1): 0 ok, 1 operational failure (or `status`: not running), 2 usage error.
// A failed docker command passes its own exit code through.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type globals struct {
	projectDir, host string
	dryRun, yes      bool
	json, help       bool
}

// passThrough commands hand everything after their name to a job unchanged: `sbd seed --dry-run --yes`
// runs the seed job's own dry run.
var passThrough = map[string]bool{"seed": true}

// splitGlobals takes the global options out of args, wherever they appear before a `--` or the
// arguments of a pass-through command.
func splitGlobals(args []string) (globals, []string, error) {
	var g globals
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" || (len(rest) > 0 && passThrough[rest[len(rest)-1]] && (len(rest) == 1 || rest[0] == "plan")) {
			rest = append(rest, args[i:]...)
			break
		}
		name, val, hasVal := strings.Cut(a, "=")
		takeValue := func() (string, error) {
			if hasVal {
				return val, nil
			}
			if i+1 >= len(args) {
				return "", usagef("%s needs a value", name)
			}
			i++
			return args[i], nil
		}
		var err error
		switch name {
		case "-C", "--project-dir":
			g.projectDir, err = takeValue()
		case "-H", "--host":
			g.host, err = takeValue()
		case "--dry-run":
			g.dryRun = true
		case "-y", "--yes":
			g.yes = true
		case "--json":
			g.json = true
		case "-h", "--help":
			g.help = true
		default:
			rest = append(rest, a)
		}
		if err != nil {
			return g, nil, err
		}
	}
	return g, rest, nil
}

func usage(w io.Writer) {
	fmt.Fprint(w, `sbd: helper for the stacks-blockchain-docker Compose project (optional: plain docker compose works too)

Usage: sbd [global options] <command> [options]

Commands:
`)
	for _, c := range Commands {
		alias := ""
		if len(c.Aliases) > 0 {
			alias = " (" + strings.Join(c.Aliases, ", ") + ")"
		}
		fmt.Fprintf(w, "  %-28s %s%s\n", c.Name, c.Help, alias)
	}
	fmt.Fprint(w, `  plan <command> [options]     Print what <command> would do (.env edits and docker compose commands)

Global options:
  -C, --project-dir DIR   the repo checkout (default: found from the current directory upwards)
  -H, --host URL          Docker host, e.g. ssh://user@host (sets DOCKER_HOST)
      --dry-run           same as plan: print, don't run
  -y, --yes               confirm destructive jobs (reset, seed) without a prompt
      --json              machine-readable output (status, doctor, plan)

Usage of each command: sbd <command> --help
`)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, nil))
}

// run is main without the process exit; docker is injected by tests (nil = the real docker CLI).
func run(args []string, stdout, stderr io.Writer, docker Docker) int {
	code, err := dispatch(args, stdout, docker)
	if err != nil {
		fmt.Fprintln(stderr, "sbd:", err)
		var ue usageError
		if errors.As(err, &ue) {
			return 2
		}
		if code == 0 {
			code = 1
		}
	}
	return code
}

func dispatch(args []string, stdout io.Writer, docker Docker) (int, error) {
	g, rest, err := splitGlobals(args)
	if err != nil {
		return 2, err
	}
	if len(rest) > 0 && rest[0] == "plan" {
		g.dryRun = true
		rest = rest[1:]
	}
	if len(rest) == 0 || rest[0] == "help" {
		usage(stdout)
		if len(rest) == 0 && !g.help {
			return 2, usagef("no command given")
		}
		return 0, nil
	}
	cmd := lookup(rest[0])
	if cmd == nil {
		return 2, usagef("unknown command %q (sbd help lists them)", rest[0])
	}
	args = rest[1:]
	if cmd.Name == "env" && len(args) > 0 && args[0] == "set" {
		cmd, args = envSet, args[1:]
	}
	if g.help {
		fmt.Fprintf(stdout, "Usage: sbd %s\n\n%s\n", cmd.Usage, cmd.Help)
		return 0, nil
	}

	cwd, _ := os.Getwd()
	dir, err := FindProject(g.projectDir, cwd)
	if err != nil {
		return 2, usageError{err.Error()}
	}
	if docker == nil {
		var extra []string
		if g.host != "" {
			extra = append(extra, "DOCKER_HOST="+g.host)
		}
		docker = execDocker{dir: dir, env: extra}
	}
	c := &Ctx{Dir: dir, Docker: docker, Yes: g.yes, JSON: g.json, Out: stdout}
	if env, err := LoadEnv(filepath.Join(dir, ".env")); err == nil {
		c.Env = env
	} else if !os.IsNotExist(err) {
		return 1, err
	}
	if sample, err := LoadEnv(filepath.Join(dir, "sample.env")); err == nil {
		c.Sample = sample
	}

	if cmd.Run != nil {
		if g.dryRun {
			return 2, usagef("%s only reports; there is nothing to plan", cmd.Name)
		}
		return cmd.Run(c, args)
	}
	plan, err := cmd.Plan(c, args)
	if err != nil {
		return 1, err
	}
	if g.dryRun {
		return 0, plan.Print(stdout, g.json)
	}
	if cmd.Guard != nil {
		if err := cmd.Guard(c, args); err != nil {
			return 1, err
		}
	}
	return plan.Execute(dir, docker)
}
