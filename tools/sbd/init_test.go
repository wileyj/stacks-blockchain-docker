package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// scripted answers the questions in order and records them; an exhausted script takes the defaults.
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

const initSample = sampleEnv + "AUTH_TOKEN=1234\nBTC_HOST=127.0.0.1\nBTC_P2P_PORT=1\nTBTC_HOST=127.0.0.1\nTBTC_P2P_PORT=1\nPG_PASSWORD=postgres\n"

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

func readEnv(t *testing.T, dir string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, l := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok && !strings.HasPrefix(k, "#") {
			m[k] = unquote(v)
		}
	}
	return m
}

func TestInitFreshInstall(t *testing.T) {
	dir, c := initProject(t, "-")
	host, port, _ := net.SplitHostPort(fakePeer(t, btcMagic["testnet"], "/Satoshi:25.1.0/", 35619, false))
	// network, signer, proxy, monitoring, BTC host, port, data root, write, bootstrap, start now
	p := &scripted{answers: []string{"testnet", "n", "y", "y", host, port, "", "y", "genesis", "n"}}
	if code, err := runInit(c, p, map[string]string{}, false); code != 0 || err != nil {
		t.Fatalf("exit %d: %v\n%s", code, err, p.out.String())
	}
	e := readEnv(t, dir)
	if e["NETWORK"] != "testnet" || e["COMPOSE_PROFILES"] != "node,api,proxy,monitoring" {
		t.Errorf("network/profiles: %s / %s", e["NETWORK"], e["COMPOSE_PROFILES"])
	}
	out := p.out.String()
	for _, k := range []string{"AUTH_TOKEN", "PG_PASSWORD", "GRAFANA_ADMIN_PASSWORD"} {
		if v := e[k]; !regexp.MustCompile(`^[0-9a-f]{24,64}$`).MatchString(v) || strings.Contains(out, v) {
			t.Errorf("%s must be generated and never printed (%q)", k, v)
		}
		if !strings.Contains(out, "sbd env get "+k) {
			t.Errorf("the notes must say how to read %s", k)
		}
	}
	for _, want := range []string{"answers: /Satoshi:25.1.0/ at height 35619", "Proxy:", "Grafana:", "Next:       sbd doctor, then sbd start"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	for _, q := range p.asked {
		if strings.Contains(q, "Run the Stacks Blockchain API") || strings.Contains(q, "HTTPS") {
			t.Errorf("the API always runs and HTTPS isn't asked: %q", q)
		}
	}
}

func TestInitKeepsAnExistingInstall(t *testing.T) {
	dir, c := initProject(t, initSample+"GRAFANA_ADMIN_PASSWORD=keep\n")
	for _, d := range []string{"postgres/base", "stacks-blockchain/mainnet"} {
		os.MkdirAll(filepath.Join(dir, "persistent-data", "mainnet", d), 0o755)
	}
	p := defaultsPrompter{out: &bytes.Buffer{}}
	if code, err := runInit(c, p, map[string]string{}, false); code != 0 || err != nil {
		t.Fatalf("exit %d: %v", code, err)
	}
	e := readEnv(t, dir)
	if e["PG_PASSWORD"] != "postgres" || e["AUTH_TOKEN"] == "1234" || e["GRAFANA_ADMIN_PASSWORD"] != "keep" || e["NETWORK"] != "mainnet" {
		t.Errorf("an existing database keeps its password, the placeholder token is replaced, settings are kept: %v", e)
	}
	if d := c.Docker.(*fakeDocker); len(d.ran) > 0 {
		t.Errorf("an existing chainstate is kept by default, nothing runs: %v", d.ran)
	}
}

func TestInitSigner(t *testing.T) {
	dir, c := initProject(t, initSample)
	host, port, _ := net.SplitHostPort(fakePeer(t, btcMagic["mainnet"], "/Satoshi:31.1.0/", 974829, false))
	key := strings.Repeat("ab", 32) + "01"
	// network, signer yes, proxy, monitoring, continue, a bad key, a good key, host, port, data, write, bootstrap, start
	p := &scripted{answers: []string{"", "y", "n", "n", "y", "nothex", key, host, port, "", "y", "genesis", "n"}}
	if code, err := runInit(c, p, map[string]string{}, false); code != 0 || err != nil {
		t.Fatalf("exit %d: %v\n%s", code, err, p.out.String())
	}
	e := readEnv(t, dir)
	if e["SIGNER_PRIVATE_KEY"] != key || e["COMPOSE_PROFILES"] != "node,api,signer" {
		t.Errorf("signer: %v", e)
	}
	out := p.out.String()
	for _, want := range []string{"liveness dependency", "registered on chain", "not a 64-character hex key", "Signer:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, key) {
		t.Errorf("the key must never be printed")
	}

	_, c = initProject(t, initSample)
	code, err := runInit(c, defaultsPrompter{out: &bytes.Buffer{}}, map[string]string{
		"NETWORK": "testnet", "COMPOSE_PROFILES": "node,api,signer", "TESTNET_SIGNER_PRIVATE_KEY": "bad"}, false)
	if code != 2 || err == nil || !strings.Contains(err.Error(), "TESTNET_SIGNER_PRIVATE_KEY") {
		t.Errorf("a bad key in the answers file must fail: %d %v", code, err)
	}
}

// Choosing to seed doesn't start a download: that needs its own yes
func TestInitSeedNeedsItsOwnYes(t *testing.T) {
	_, c := initProject(t, initSample)
	host, port, _ := net.SplitHostPort(fakePeer(t, btcMagic["mainnet"], "/Satoshi:31.1.0/", 1, false))
	// network, signer, proxy, monitoring, host, port, data, write, bootstrap=seed, run now? no
	p := &scripted{answers: []string{"", "n", "n", "n", host, port, "", "y", "seed", "n"}}
	if code, err := runInit(c, p, map[string]string{}, false); code != 0 || err != nil {
		t.Fatalf("exit %d: %v\n%s", code, err, p.out.String())
	}
	if d := c.Docker.(*fakeDocker); len(d.ran) > 0 {
		t.Errorf("nothing may run without its own yes: %v", d.ran)
	}
	if !strings.Contains(p.out.String(), "Next:       sbd doctor, then sbd seed, then sbd start") {
		t.Errorf("next steps:\n%s", p.out.String())
	}

	_, c = initProject(t, initSample)
	host, port, _ = net.SplitHostPort(fakePeer(t, btcMagic["mainnet"], "/Satoshi:31.1.0/", 1, false))
	p = &scripted{answers: []string{"", "n", "n", "n", host, port, "", "y", "seed", "y", "y"}}
	if code, err := runInit(c, p, map[string]string{}, false); code != 0 || err != nil {
		t.Fatalf("exit %d: %v", code, err)
	}
	if d := c.Docker.(*fakeDocker); strings.Join(d.ran, ";") != "compose run --rm seed;compose up -d" {
		t.Errorf("seed, then start: %v", d.ran)
	}
}

func TestInitBadBitcoinPeer(t *testing.T) {
	_, c := initProject(t, initSample)
	// a closed port three times: init warns each time and keeps the last answer
	p := &scripted{answers: []string{"", "n", "n", "n", "127.0.0.1", "1", "n", "127.0.0.1", "1", "n", "127.0.0.1", "1", "", "y", "genesis", "n"}}
	if code, err := runInit(c, p, map[string]string{}, false); code != 0 || err != nil {
		t.Fatalf("exit %d: %v\n%s", code, err, p.out.String())
	}
	if n := strings.Count(p.out.String(), "WARNING: 127.0.0.1:1"); n != 3 || !strings.Contains(p.out.String(), "keeping 127.0.0.1:1") {
		t.Errorf("three warnings, then kept:\n%s", p.out.String())
	}
}

func TestInitNeedsATerminalOrAnswers(t *testing.T) {
	dir := project(t, sampleEnv)
	code, _, errOut := sbd(t, dir, &fakeDocker{version: "2.39.1"}, "init")
	if code != 2 || !strings.Contains(errOut, "run it in a terminal") {
		t.Errorf("exit %d: %s", code, errOut)
	}
}
