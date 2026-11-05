package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// scripted answers the questions in order and records them.
type scripted struct {
	answers []string
	asked   []string
	out     bytes.Buffer
}

func (s *scripted) next(q string) string {
	s.asked = append(s.asked, q)
	if len(s.answers) == 0 {
		return ""
	}
	a := s.answers[0]
	s.answers = s.answers[1:]
	return a
}
func (s *scripted) Ask(q, def string) string {
	if a := s.next(q); a != "" {
		return a
	}
	return def
}
func (s *scripted) Secret(q string) string { return s.next(q) }
func (s *scripted) Confirm(q string, def bool) bool {
	switch s.next(q) {
	case "y":
		return true
	case "n":
		return false
	}
	return def
}
func (s *scripted) Say(f string, a ...any) { fmt.Fprintf(&s.out, f+"\n", a...) }

const initSample = sampleEnv + "AUTH_TOKEN=1234\nBTC_HOST=127.0.0.1\nBTC_P2P_PORT=1\nPG_PASSWORD=postgres\n"

func initProject(t *testing.T, env string) (string, *Ctx) {
	t.Helper()
	dir := project(t, env)
	os.WriteFile(filepath.Join(dir, "sample.env"), []byte(initSample), 0o644)
	c := &Ctx{Dir: dir, Docker: &fakeDocker{version: "2.39.1"}, Out: &bytes.Buffer{}}
	c.Sample, _ = LoadEnv(filepath.Join(dir, "sample.env"))
	if e, err := LoadEnv(filepath.Join(dir, ".env")); err == nil {
		c.Env = e
	}
	return dir, c
}

func readEnv(t *testing.T, dir string) *Env {
	t.Helper()
	e, err := LoadEnv(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestInitFreshInstall(t *testing.T) {
	dir, c := initProject(t, "-")
	// network, api, signer, proxy, monitoring, BTC host, port, data root, TLS, write, bootstrap
	p := &scripted{answers: []string{"testnet", "y", "n", "y", "y", "", "", "", "n", "y", "later"}}
	if code, err := runInit(c, p, map[string]string{}, false); code != 0 || err != nil {
		t.Fatalf("exit %d: %v\n%s", code, err, p.out.String())
	}
	e := readEnv(t, dir)
	get := func(k string) string { v, _ := e.Get(k); return v }
	if get("NETWORK") != "testnet" || get("COMPOSE_PROFILES") != "node,api,proxy,monitoring" {
		t.Errorf("network/profiles: %s / %s", get("NETWORK"), get("COMPOSE_PROFILES"))
	}
	for _, k := range []string{"AUTH_TOKEN", "PG_PASSWORD", "GRAFANA_ADMIN_PASSWORD"} {
		v := get(k)
		if !regexp.MustCompile(`^[0-9a-f]{24,64}$`).MatchString(v) || v == "1234" || v == "postgres" {
			t.Errorf("%s must be generated, got %q", k, v)
		}
		if strings.Contains(p.out.String(), v) {
			t.Errorf("%s's value must never be printed", k)
		}
	}
	if !strings.Contains(p.out.String(), "AUTH_TOKEN=<hidden>") || !strings.Contains(p.out.String(), "Next: sbd doctor, then sbd start") {
		t.Errorf("review/next steps:\n%s", p.out.String())
	}
}

func TestInitDefaultsKeepAnExistingEnv(t *testing.T) {
	env := initSample + "GRAFANA_ADMIN_PASSWORD=keep\n"
	dir, c := initProject(t, env)
	os.MkdirAll(filepath.Join(dir, "persistent-data", "mainnet", "postgres", "base"), 0o755) // a database exists
	if code, err := runInit(c, defaultsPrompter{out: &bytes.Buffer{}}, map[string]string{}, false); code != 0 || err != nil {
		t.Fatalf("exit %d: %v", code, err)
	}
	e := readEnv(t, dir)
	if v, _ := e.Get("PG_PASSWORD"); v != "postgres" {
		t.Errorf("an existing database keeps its password, got %q", v)
	}
	if v, _ := e.Get("AUTH_TOKEN"); v == "1234" {
		t.Errorf("the placeholder auth token must be replaced")
	}
	if v, _ := e.Get("COMPOSE_PROFILES"); v != "node,api" {
		t.Errorf("profiles kept: %q", v)
	}
	if v, _ := e.Get("GRAFANA_ADMIN_PASSWORD"); v != "keep" {
		t.Errorf("an existing setting is kept: %q", v)
	}
}

func TestInitSignerKey(t *testing.T) {
	dir, c := initProject(t, initSample)
	key := strings.Repeat("ab", 32) + "01"
	// network, api, signer, proxy, monitoring, then a bad key and a good one, then the rest
	p := &scripted{answers: []string{"mainnet", "y", "y", "n", "n", "nothex", key, "", "", "", "y", "later"}}
	if code, err := runInit(c, p, map[string]string{}, false); code != 0 || err != nil {
		t.Fatalf("exit %d: %v\n%s", code, err, p.out.String())
	}
	if v, _ := readEnv(t, dir).Get("SIGNER_PRIVATE_KEY"); v != key {
		t.Errorf("signer key %q", v)
	}
	if !strings.Contains(p.out.String(), "not a 64-character hex key") {
		t.Errorf("a bad key must be refused and asked again:\n%s", p.out.String())
	}

	_, c = initProject(t, initSample)
	code, err := runInit(c, defaultsPrompter{out: &bytes.Buffer{}}, map[string]string{
		"NETWORK": "testnet", "COMPOSE_PROFILES": "node,api,signer", "TESTNET_SIGNER_PRIVATE_KEY": "bad"}, false)
	if code != 2 || err == nil || !strings.Contains(err.Error(), "TESTNET_SIGNER_PRIVATE_KEY") {
		t.Errorf("a bad key in the answers file must fail: %d %v", code, err)
	}
}

func TestInitNeedsATerminalOrAnswers(t *testing.T) {
	dir := project(t, sampleEnv)
	code, _, errOut := sbd(t, dir, &fakeDocker{version: "2.39.1"}, "init")
	if code != 2 || !strings.Contains(errOut, "run it in a terminal") {
		t.Errorf("exit %d: %s", code, errOut)
	}
}
