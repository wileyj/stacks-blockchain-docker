package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/term"
)

// `sbd init`: first-time setup (refactor/13 §1; owner, 2026-11-05: "init", still needed). It asks for
// the network, the services and their settings, shows every change, and writes .env (and nothing
// else: everything it does can be done by editing .env by hand). Secrets it can choose itself are
// generated instead of left at their sample defaults. Non-interactive: --from answers.env (KEY=VALUE,
// the .env format) and/or --yes (take the defaults).

// Registered here rather than in Commands: init runs other commands (seed, start) through lookup,
// which reads Commands, so a static entry would be an initialization cycle.
func init() {
	Commands = append([]*Command{{Name: "init", Usage: "init [--from answers.env] [--start]",
		Help: "Set up .env step by step (network, services, keys, data location); writes only .env", Run: initRun}}, Commands...)
}

// Prompter asks the questions. The terminal one reads stdin; the defaults one (--yes) answers each
// question with its default.
type Prompter interface {
	Ask(question, def string) string
	Secret(question string) string
	Confirm(question string, def bool) bool
	Say(format string, a ...any)
}

type termPrompter struct {
	in  *bufio.Reader
	out io.Writer
	fd  int
	tty bool
}

func (p *termPrompter) line() string {
	s, _ := p.in.ReadString('\n')
	return strings.TrimSpace(s)
}

func (p *termPrompter) Ask(q, def string) string {
	if def != "" {
		fmt.Fprintf(p.out, "%s [%s]: ", q, def)
	} else {
		fmt.Fprintf(p.out, "%s: ", q)
	}
	if v := p.line(); v != "" {
		return v
	}
	return def
}

func (p *termPrompter) Secret(q string) string {
	fmt.Fprintf(p.out, "%s: ", q)
	if p.tty {
		b, err := term.ReadPassword(p.fd)
		fmt.Fprintln(p.out)
		if err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return p.line()
}

func (p *termPrompter) Confirm(q string, def bool) bool {
	d := "y/N"
	if def {
		d = "Y/n"
	}
	for {
		fmt.Fprintf(p.out, "%s [%s]: ", q, d)
		switch strings.ToLower(p.line()) {
		case "":
			return def
		case "y", "yes":
			return true
		case "n", "no":
			return false
		}
	}
}

func (p *termPrompter) Say(format string, a ...any) { fmt.Fprintf(p.out, format+"\n", a...) }

type defaultsPrompter struct{ out io.Writer }

func (p defaultsPrompter) Ask(q, def string) string        { return def }
func (p defaultsPrompter) Secret(string) string            { return "" }
func (p defaultsPrompter) Confirm(q string, def bool) bool { return def }
func (p defaultsPrompter) Say(format string, a ...any)     { fmt.Fprintf(p.out, format+"\n", a...) }

var signerKeyRe = regexp.MustCompile(`^[0-9a-fA-F]{64}(01)?$`)

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// initState collects the edits: the values .env will have, starting from .env or sample.env.
type initState struct {
	c       *Ctx
	answers map[string]string // --from
	edits   []Step
	values  map[string]string
}

func (st *initState) get(key string) string {
	if v, ok := st.values[key]; ok {
		return v
	}
	return st.c.Value(key)
}

func (st *initState) set(key, value string) {
	if st.get(key) == value {
		return
	}
	st.values[key] = value
	st.edits = slices.DeleteFunc(st.edits, func(s Step) bool { return s.SetKey == key })
	st.edits = append(st.edits, Step{SetKey: key, SetValue: value})
}

// answer returns the answers file's value for key, or asks.
func (st *initState) answer(p Prompter, key, question, def string) string {
	if v, ok := st.answers[key]; ok {
		return v
	}
	return p.Ask(question, def)
}

func initRun(c *Ctx, args []string) (int, error) {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	from := fs.String("from", "", "")
	start := fs.Bool("start", false, "")
	if _, err := parseFlags(fs, args); err != nil {
		return 2, err
	}
	answers := map[string]string{}
	if *from != "" {
		env, err := LoadEnv(*from)
		if err != nil {
			return 2, usagef("init --from: %v", err)
		}
		for _, l := range env.lines {
			if m := envLineRe.FindStringSubmatch(l); m != nil {
				answers[m[1]] = unquote(m[2])
			}
		}
	}
	var p Prompter
	fd := int(os.Stdin.Fd())
	tty := term.IsTerminal(fd)
	switch {
	case c.Yes || (*from != "" && !tty):
		p = defaultsPrompter{out: c.Out}
	case tty:
		p = &termPrompter{in: bufio.NewReader(os.Stdin), out: c.Out, fd: fd, tty: true}
	default:
		return 2, usagef("init asks questions: run it in a terminal, or pass --yes (defaults) and/or --from answers.env")
	}
	return runInit(c, p, answers, *start)
}

// runInit is init with its prompter and answers injected (tests drive it directly).
func runInit(c *Ctx, p Prompter, answers map[string]string, start bool) (int, error) {
	st := &initState{c: c, answers: answers, values: map[string]string{}}
	p.Say("sbd init: sets up .env for this checkout (%s). Nothing is written until you confirm.", c.Dir)
	if c.Env != nil {
		p.Say("An .env exists: its values are the defaults, and only what you change is rewritten.")
	}

	// 1. Preflight
	failed := false
	for _, ch := range Doctor(c) {
		if ch.Level != "ok" && ch.What != ".env" && ch.What != "disk space" {
			p.Say("  %s %s: %s", strings.ToUpper(ch.Level), ch.What, ch.Detail)
		}
		failed = failed || (ch.Level == "fail" && ch.What != "NETWORK" && ch.What != "COMPOSE_PROFILES" && ch.What != "signer key")
	}
	if failed {
		return 1, fmt.Errorf("fix the failed checks above first (sbd doctor shows them all)")
	}

	// 2. Network
	for {
		n := strings.ToLower(st.answer(p, "NETWORK", "Network (mainnet, testnet)", orDefault(st.get("NETWORK"), "mainnet")))
		if checkNetwork(n) == nil {
			st.set("NETWORK", n)
			break
		}
		if _, ok := answers["NETWORK"]; ok {
			return 2, checkNetwork(n)
		}
		p.Say("  mainnet or testnet")
	}
	net := st.get("NETWORK")

	// 3. Services
	current := List(st.get("COMPOSE_PROFILES"))
	profiles := []string{"node"}
	if v, ok := answers["COMPOSE_PROFILES"]; ok {
		profiles = List(v)
		if err := checkProfiles(profiles); err != nil {
			return 2, err
		}
	} else {
		for _, svc := range []struct{ name, question string }{
			{"api", "Run the Stacks Blockchain API (and its postgres)?"},
			{"signer", "Run a signer?"},
			{"proxy", "Serve the API to other machines through the nginx proxy (ports 80/443)?"},
			{"monitoring", "Run monitoring (Prometheus and Grafana, on this host only)?"},
		} {
			def := slices.Contains(current, svc.name) || (svc.name == "api" && c.Env == nil)
			if svc.name == "proxy" && !slices.Contains(profiles, "api") {
				continue
			}
			if p.Confirm(svc.question, def) {
				profiles = append(profiles, svc.name)
			}
		}
	}
	st.set("COMPOSE_PROFILES", strings.Join(profiles, ","))

	// 4. Signer key and the node/signer auth token
	if slices.Contains(profiles, "signer") {
		keyVar := signerKeyVar(net)
		key := st.get(keyVar)
		if v, ok := answers[keyVar]; ok {
			key = v
		}
		for !signerKeyRe.MatchString(key) {
			if _, ok := answers[keyVar]; ok || isDefaults(p) {
				return 2, usagef("%s must be a 64-character hex private key (optionally ending in 01)", keyVar)
			}
			if key != "" {
				p.Say("  not a 64-character hex key (optionally ending in 01)")
			}
			key = p.Secret("Signer private key (hex, not shown)")
		}
		st.set(keyVar, key)
	}
	if tok := st.get("AUTH_TOKEN"); tok == "" || tok == "1234" {
		st.set("AUTH_TOKEN", randomHex(32))
		p.Say("  AUTH_TOKEN: generated (the sample's 1234 is a placeholder)")
	}

	// 5. Bitcoin peer
	btc := "BTC_"
	if net == "testnet" {
		btc = "TBTC_"
	}
	host := st.answer(p, btc+"HOST", "Bitcoin node to follow (host)", st.get(btc+"HOST"))
	st.set(btc+"HOST", host)
	port := st.answer(p, btc+"P2P_PORT", "Its P2P port", st.get(btc+"P2P_PORT"))
	st.set(btc+"P2P_PORT", port)
	if _, scripted := answers[btc+"HOST"]; !scripted && !isDefaults(p) {
		if conn, err := (netDialer{}).dial(host, port); err != nil {
			p.Say("  WARNING: can't reach %s:%s (%v); the node needs it", host, port, err)
		} else {
			conn.Close()
			p.Say("  %s:%s is reachable", host, port)
		}
	}

	// 6. Data location
	root := st.answer(p, "DATA_ROOT", "Where to keep the data", orDefault(st.get("DATA_ROOT"), "./persistent-data"))
	if root != orDefault(st.get("DATA_ROOT"), "./persistent-data") {
		st.set("DATA_ROOT", root)
	}
	view := &Ctx{Dir: c.Dir, Env: c.Env, Sample: c.Sample, Docker: c.Docker, Remote: c.Remote}
	view.Env = overlay(c, st.values)
	for _, ch := range diskChecks(view, profiles, c.Remote) {
		p.Say("  %s: %s", ch.What, ch.Detail)
	}

	// 7. Proxy TLS, Grafana, the database password
	if slices.Contains(profiles, "proxy") {
		tlsDef := st.get("PROXY_TLS") == "1"
		tls := tlsDef
		if v, ok := answers["PROXY_TLS"]; ok {
			tls = v == "1"
		} else {
			tls = p.Confirm("Serve HTTPS (needs fullchain.pem and privkey.pem)?", tlsDef)
		}
		if tls {
			st.set("PROXY_TLS", "1")
			dir := st.answer(p, "PROXY_TLS_DIR", "Directory with fullchain.pem and privkey.pem", orDefault(st.get("PROXY_TLS_DIR"), "./conf/nginx/tls"))
			if dir != orDefault(st.get("PROXY_TLS_DIR"), "./conf/nginx/tls") {
				st.set("PROXY_TLS_DIR", dir)
			}
			abs := dir
			if !filepath.IsAbs(abs) {
				abs = filepath.Join(c.Dir, dir)
			}
			for _, f := range []string{"fullchain.pem", "privkey.pem"} {
				if _, err := os.Stat(filepath.Join(abs, f)); err != nil {
					p.Say("  WARNING: %s is missing in %s: nginx won't start until it's there", f, dir)
				}
			}
		} else if st.get("PROXY_TLS") == "1" {
			st.set("PROXY_TLS", "0")
		}
	}
	if slices.Contains(profiles, "monitoring") && st.get("GRAFANA_ADMIN_PASSWORD") == "" {
		st.set("GRAFANA_ADMIN_PASSWORD", randomHex(12))
		p.Say("  GRAFANA_ADMIN_PASSWORD: generated (sbd env get GRAFANA_ADMIN_PASSWORD shows it)")
	}
	// Only for a database that doesn't exist yet: postgres keeps the password it was created with
	if slices.Contains(profiles, "api") && orDefault(st.get("PG_PASSWORD"), "postgres") == "postgres" && !hasData(dataDir(view, "api")) {
		st.set("PG_PASSWORD", randomHex(16))
		p.Say("  PG_PASSWORD: generated for the new database")
	}

	// 8. Review and write
	if len(st.edits) == 0 && c.Env != nil {
		p.Say("Nothing to change: .env already has these settings.")
	} else {
		p.Say("\nChanges to .env:")
		if c.Env == nil {
			p.Say("  (created from sample.env)")
		}
		for _, s := range st.edits {
			p.Say("  %s=%s", s.SetKey, shown(s.SetKey, s.SetValue))
		}
		if !p.Confirm("Write .env?", true) {
			return 1, fmt.Errorf("nothing written")
		}
		var plan Plan
		if c.Env == nil {
			plan = append(plan, Step{CreateEnv: true})
		}
		if code, err := append(plan, st.edits...).Execute(c.Dir, c.Docker); err != nil {
			return code, err
		}
		p.Say(".env written (the previous one is in .env.save).")
	}

	// 9. Bootstrap and start
	bootstrap := strings.ToLower(st.answer(p, "SBD_INIT_BOOTSTRAP", "Bootstrap: seed from the Hiro archive now, or sync from genesis (seed, genesis, later)", "later"))
	next := []string{"sbd doctor"}
	switch bootstrap {
	case "seed":
		next = append(next, "sbd seed")
	case "genesis":
		if net == "mainnet" {
			p.Say("  A mainnet sync from genesis takes weeks; seeding takes hours.")
		}
	}
	if start || (bootstrap != "later" && !isDefaults(p) && p.Confirm("Start the stack when that's done?", true)) {
		next = append(next, "sbd start")
	}
	if bootstrap == "seed" || slices.Contains(next, "sbd start") {
		fresh, err := LoadEnv(filepath.Join(c.Dir, ".env"))
		if err != nil {
			return 1, err
		}
		run := &Ctx{Dir: c.Dir, Env: fresh, Sample: c.Sample, Docker: c.Docker, Yes: c.Yes, Out: c.Out, Remote: c.Remote}
		if bootstrap == "seed" {
			if code, err := runCommand(run, "seed", nil); err != nil || code != 0 {
				return code, err
			}
		}
		if slices.Contains(next, "sbd start") {
			return runCommand(run, "start", nil)
		}
	}
	p.Say("Next: %s", strings.Join(append(next, "sbd start"), ", then "))
	return 0, nil
}

func isDefaults(p Prompter) bool { _, ok := p.(defaultsPrompter); return ok }

// overlay is .env (or sample.env) with the edits applied, for checks that read settings.
func overlay(c *Ctx, values map[string]string) *Env {
	base := c.Env
	if base == nil {
		base = c.Sample
	}
	e := &Env{}
	if base != nil {
		e.lines = slices.Clone(base.lines)
	}
	for k, v := range values {
		e.Set(k, v)
	}
	return e
}

// runCommand runs another sbd command (with its guard) on behalf of init.
func runCommand(c *Ctx, name string, args []string) (int, error) {
	cmd := lookup(name)
	plan, err := cmd.Plan(c, args)
	if err != nil {
		return 1, err
	}
	if cmd.Guard != nil {
		if err := cmd.Guard(c, args); err != nil {
			return 1, err
		}
	}
	return plan.Execute(c.Dir, c.Docker)
}

// netDialer is a seam for the connectivity test.
type netDialer struct{}

func (netDialer) dial(host, port string) (net.Conn, error) {
	return net.DialTimeout("tcp", net.JoinHostPort(host, port), 3*time.Second)
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
