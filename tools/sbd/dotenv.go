package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Env is the project's .env: the only place settings live (refactor/14 R2). Compose reads the same
// file, so sbd parses the subset of the dotenv format Compose accepts and edits it line by line,
// keeping comments, order and unrelated lines exactly as they were.
type Env struct {
	Path  string
	lines []string
}

var envLineRe = regexp.MustCompile(`^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=(.*)$`)

func LoadEnv(path string) (*Env, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	if s == "" {
		lines = nil
	}
	return &Env{Path: path, lines: lines}, nil
}

// Get returns the value of the last assignment of key (as Compose does), and whether there is one.
func (e *Env) Get(key string) (string, bool) {
	val, ok := "", false
	for _, l := range e.lines {
		if m := envLineRe.FindStringSubmatch(l); m != nil && m[1] == key {
			val, ok = unquote(m[2]), true
		}
	}
	return val, ok
}

// Set changes the last assignment of key in place, or appends one. It reports whether anything changed.
func (e *Env) Set(key, value string) bool {
	line := key + "=" + quote(value)
	for i := len(e.lines) - 1; i >= 0; i-- {
		if m := envLineRe.FindStringSubmatch(e.lines[i]); m != nil && m[1] == key {
			if unquote(m[2]) == value {
				return false
			}
			e.lines[i] = line
			return true
		}
	}
	e.lines = append(e.lines, line)
	return true
}

// Save writes the file. With backup, it first copies the current file to .env.save (refactor/11
// §A.11); a command that makes several edits backs up once, so .env.save is the state before it.
func (e *Env) Save(backup bool) error {
	if old, err := os.ReadFile(e.Path); err == nil && backup {
		if err := os.WriteFile(e.Path+".save", old, 0o600); err != nil {
			return err
		}
	}
	return os.WriteFile(e.Path, []byte(strings.Join(e.lines, "\n")+"\n"), 0o600)
}

// unquote follows Compose: single quotes are literal, double quotes allow escapes, unquoted values
// end at " #" (an inline comment) and are trimmed.
func unquote(raw string) string {
	v := strings.TrimSpace(raw)
	switch {
	case len(v) >= 2 && v[0] == '\'' && strings.LastIndexByte(v, '\'') > 0:
		return v[1:strings.LastIndexByte(v, '\'')]
	case len(v) >= 2 && v[0] == '"' && strings.LastIndexByte(v, '"') > 0:
		inner := v[1:strings.LastIndexByte(v, '"')]
		return strings.NewReplacer(`\n`, "\n", `\"`, `"`, `\\`, `\`).Replace(inner)
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	return v
}

func quote(v string) string {
	if v == "" || strings.IndexFunc(v, func(r rune) bool { return strings.ContainsRune(" #'\"\\$\t\n", r) }) < 0 {
		return v
	}
	if !strings.Contains(v, `'`) {
		return `'` + v + `'` // literal in Compose, no interpolation
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
}

// List splits a comma-separated value (COMPOSE_PROFILES) into lower-case, de-duplicated items.
func List(v string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range strings.Split(v, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

func mustEnv(dir string) (*Env, error) {
	env, err := LoadEnv(dir + "/.env")
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("no .env in %s: run `sbd start` (it creates one from sample.env) or `cp sample.env .env`", dir)
	}
	return env, err
}
