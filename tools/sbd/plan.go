package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// A Plan is what a command does, as steps a user could perform by hand: .env edits and `docker
// compose` commands (refactor/14 R4). `sbd plan <cmd>` / `--dry-run` prints it, and running the
// command executes exactly these steps.
type Plan []Step

type Step struct {
	CreateEnv bool     `json:"create_env,omitempty"` // cp sample.env .env
	SetKey    string   `json:"set_key,omitempty"`    // edit .env: KEY=VALUE
	SetValue  string   `json:"set_value,omitempty"`  //
	Docker    []string `json:"docker,omitempty"`     // docker <args>
	Stdout    string   `json:"stdout,omitempty"`     // redirect docker's stdout to this file (relative to the project)
}

func compose(args ...string) Step { return Step{Docker: append([]string{"compose"}, args...)} }

// String renders a step as the shell command (or edit) a user would type.
func (s Step) String() string {
	switch {
	case s.CreateEnv:
		return "cp sample.env .env"
	case s.SetKey != "":
		return fmt.Sprintf("edit .env: %s=%s", s.SetKey, quote(s.SetValue))
	}
	parts := []string{"docker"}
	for _, a := range s.Docker {
		parts = append(parts, shellQuote(a))
	}
	if s.Stdout != "" {
		parts = append(parts, ">", shellQuote(s.Stdout))
	}
	return strings.Join(parts, " ")
}

func shellQuote(a string) string {
	if a != "" && strings.IndexFunc(a, func(r rune) bool { return strings.ContainsRune(" *'\"$\\|&;<>(){}[]?!#~`", r) }) < 0 {
		return a
	}
	return `'` + strings.ReplaceAll(a, `'`, `'\''`) + `'`
}

func (p Plan) Print(w io.Writer, asJSON bool) error {
	if asJSON {
		steps := p
		if steps == nil {
			steps = Plan{}
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"steps": steps})
	}
	for _, s := range p {
		fmt.Fprintln(w, s.String())
	}
	return nil
}

// Execute runs the steps in order and stops at the first failure. It returns the exit code of a
// failed docker command (or 1 for any other failure).
func (p Plan) Execute(dir string, d Docker) (int, error) {
	backedUp := false
	for _, s := range p {
		switch {
		case s.CreateEnv:
			b, err := os.ReadFile(filepath.Join(dir, "sample.env"))
			if err != nil {
				return 1, err
			}
			if err := os.WriteFile(filepath.Join(dir, ".env"), b, 0o600); err != nil {
				return 1, err
			}
			fmt.Fprintln(os.Stderr, "sbd: created .env from sample.env")
		case s.SetKey != "":
			env, err := LoadEnv(filepath.Join(dir, ".env"))
			if err != nil {
				return 1, err
			}
			if env.Set(s.SetKey, s.SetValue) {
				if err := env.Save(!backedUp); err != nil {
					return 1, err
				}
				backedUp = true
				fmt.Fprintf(os.Stderr, "sbd: .env: %s=%s (previous .env in .env.save)\n", s.SetKey, quote(s.SetValue))
			}
		default:
			out := io.Writer(os.Stdout)
			if s.Stdout != "" {
				path := filepath.Join(dir, s.Stdout)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					return 1, err
				}
				f, err := os.Create(path)
				if err != nil {
					return 1, err
				}
				defer f.Close()
				out = f
			}
			code, err := d.Run(s.Docker, out)
			if err != nil {
				return 1, err
			}
			if code != 0 {
				return code, fmt.Errorf("`%s` exited %d", s, code)
			}
		}
	}
	return 0, nil
}
