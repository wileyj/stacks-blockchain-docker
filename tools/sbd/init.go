package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
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
	c         *Ctx
	answers   map[string]string // --from
	edits     []Step
	values    map[string]string
	generated []string // keys whose values init chose (listed at the end, never shown)
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
//
// Decisions (owner, 2026-11-06): preflight only warns; the network defaults to the existing one, else
// mainnet; the node and the API always run, the signer (off by default), the proxy and monitoring are
// optional; the Bitcoin peer is checked with a real P2P handshake; generated secrets are listed at the
// end, never shown on the way; an existing chainstate is kept unless the user asks to seed or resync.
func runInit(c *Ctx, p Prompter, answers map[string]string, start bool) (int, error) {
	st := &initState{c: c, answers: answers, values: map[string]string{}}
	p.Say("sbd init: sets up .env for this checkout (%s). Nothing is written until you confirm.", c.Dir)
	if c.Env != nil {
		p.Say("An .env exists: its values are the defaults, and only what you change is rewritten.")
	}

	// 1. Preflight: warnings only, writing .env doesn't depend on them
	for _, ch := range Doctor(c) {
		if ch.Level != "ok" && ch.What != ".env" && ch.What != "disk space" && ch.What != "NETWORK" && ch.What != "COMPOSE_PROFILES" && ch.What != "signer key" {
			p.Say("  %s %s: %s", strings.ToUpper(ch.Level), ch.What, ch.Detail)
		}
	}

	// 2. Network: the existing one, else mainnet (one network per host)
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

	// 3. Services: the node and the API always; the rest optional
	current := List(st.get("COMPOSE_PROFILES"))
	profiles := []string{"node", "api"}
	if v, ok := answers["COMPOSE_PROFILES"]; ok {
		if err := checkProfiles(List(v)); err != nil {
			return 2, err
		}
		profiles = List(v)
	} else {
		if p.Confirm("Run a signer? (needs a registered signer key; see below)", slices.Contains(current, "signer")) {
			profiles = append(profiles, "signer")
		}
		if p.Confirm("Serve the API to other machines through the nginx proxy (HTTP, port 80)?", slices.Contains(current, "proxy")) {
			profiles = append(profiles, "proxy")
		}
		if p.Confirm("Run monitoring (Prometheus and Grafana, on this host only)?", slices.Contains(current, "monitoring")) {
			profiles = append(profiles, "monitoring")
		}
	}
	st.set("COMPOSE_PROFILES", strings.Join(profiles, ","))

	// 4. Signer: only when asked for, with what it implies
	if slices.Contains(profiles, "signer") {
		keyVar := signerKeyVar(net)
		key := st.get(keyVar)
		if v, ok := answers[keyVar]; ok {
			key = v
		}
		if !signerKeyRe.MatchString(key) {
			if _, ok := answers[keyVar]; ok || isDefaults(p) {
				return 2, usagef("%s must be a 64-character hex private key (optionally ending in 01)", keyVar)
			}
			p.Say(`  A signer:
    - changes the node's config (stacker = true, and an event observer for the signer);
    - becomes a liveness dependency: the node waits on it, so a stopped signer stalls the node;
    - signs only with a key registered on chain through stacking (stacked or delegated STX);
    - keeps the key in .env and Signer.toml: back it up, and never use it on a second signer;
    - can sign only once the node is at the tip, and should be monitored (it affects the network).`)
			if !p.Confirm("Continue with the signer?", true) {
				profiles = slices.DeleteFunc(profiles, func(s string) bool { return s == "signer" })
				st.set("COMPOSE_PROFILES", strings.Join(profiles, ","))
			} else {
				for !signerKeyRe.MatchString(key) {
					if key != "" {
						p.Say("  not a 64-character hex key (optionally ending in 01)")
					}
					key = p.Secret("Signer private key (hex, not shown)")
				}
			}
		}
		if slices.Contains(profiles, "signer") {
			st.set(keyVar, key)
		}
	}
	// The node/signer shared secret, also guarding the node's privileged RPC: never the sample's placeholder
	if tok := st.get("AUTH_TOKEN"); tok == "" || tok == "1234" {
		st.set("AUTH_TOKEN", randomHex(32))
		st.generated = append(st.generated, "AUTH_TOKEN")
	}

	// 5. Bitcoin peer: a real P2P handshake (a TCP connect would accept anything listening)
	btc := "BTC_"
	if net == "testnet" {
		btc = "TBTC_"
	}
	for attempt := 1; ; attempt++ {
		host := st.answer(p, btc+"HOST", "Bitcoin node to follow (host)", st.get(btc+"HOST"))
		port := st.answer(p, btc+"P2P_PORT", "Its P2P port", st.get(btc+"P2P_PORT"))
		st.set(btc+"HOST", host)
		st.set(btc+"P2P_PORT", port)
		_, scripted := answers[btc+"HOST"]
		if scripted || isDefaults(p) {
			break
		}
		peer, err := ProbeBtcPeer(net, host, port, 8*time.Second)
		if err == nil {
			p.Say("  %s:%s answers: %s at height %d", host, port, peer.UserAgent, peer.Height)
			break
		}
		if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "EOF") {
			err = fmt.Errorf("it accepted the connection but closed it without answering the handshake: it may be full, not accepting new peers, or on another Bitcoin network")
		}
		p.Say("  WARNING: %s:%s: %v", host, port, err)
		if attempt == 3 {
			p.Say("  keeping %s:%s; check it before starting (sbd doctor, or the node's log)", host, port)
			break
		}
		if p.Confirm("Use it anyway?", false) {
			break
		}
	}

	// 6. Data location
	root := st.answer(p, "DATA_ROOT", "Where to keep the data (chainstate, API database, signer)", orDefault(st.get("DATA_ROOT"), "./persistent-data"))
	if root != orDefault(st.get("DATA_ROOT"), "./persistent-data") {
		st.set("DATA_ROOT", root)
	}
	view := &Ctx{Dir: c.Dir, Sample: c.Sample, Docker: c.Docker, Remote: c.Remote, Env: overlay(c, st.values)}
	for _, ch := range diskChecks(view, profiles, c.Remote) {
		p.Say("  %s: %s", ch.What, ch.Detail)
	}

	// 7. Generated passwords (listed at the end)
	if slices.Contains(profiles, "monitoring") && st.get("GRAFANA_ADMIN_PASSWORD") == "" {
		st.set("GRAFANA_ADMIN_PASSWORD", randomHex(12))
		st.generated = append(st.generated, "GRAFANA_ADMIN_PASSWORD")
	}
	// Only for a database that doesn't exist yet: postgres keeps the password it was created with
	if orDefault(st.get("PG_PASSWORD"), "postgres") == "postgres" && !hasData(dataDir(view, "api")) {
		st.set("PG_PASSWORD", randomHex(16))
		st.generated = append(st.generated, "PG_PASSWORD")
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

	// 9. Bootstrap: keep an existing chainstate unless asked; seed by default when there is none
	fresh, err := LoadEnv(filepath.Join(c.Dir, ".env"))
	if err != nil {
		return 1, err
	}
	run := &Ctx{Dir: c.Dir, Env: fresh, Sample: c.Sample, Docker: c.Docker, Yes: c.Yes, Out: c.Out, Remote: c.Remote}
	var bootstrap string
	if hasData(dataDir(run, "node")) {
		bootstrap = strings.ToLower(st.answer(p, "SBD_INIT_BOOTSTRAP", "A "+net+" chainstate exists. Keep it, or delete it and seed from the Hiro archive / resync from genesis (keep, seed, resync)", "keep"))
	} else {
		bootstrap = strings.ToLower(st.answer(p, "SBD_INIT_BOOTSTRAP", "No "+net+" chainstate yet. Seed from the Hiro archive (hours) or sync from genesis (weeks on mainnet) (seed, genesis)", "seed"))
	}
	switch bootstrap {
	case "keep", "seed", "resync", "genesis":
	default:
		return 2, usagef("bootstrap %q: keep, seed, resync or genesis", bootstrap)
	}
	// A seed downloads hundreds of GB and a resync deletes data: each gets its own yes, never implied by
	// the choice above
	now := false
	if bootstrap == "seed" || bootstrap == "resync" {
		what := "Run the seed now? It downloads about 360 GB (mainnet) and takes a few hours"
		if bootstrap == "resync" {
			what = "Delete this network's data now (a resync from genesis follows on start)?"
		}
		now = !isDefaults(p) && p.Confirm(what, false)
	}
	if !start && !isDefaults(p) && (now || bootstrap == "keep" || bootstrap == "genesis") {
		q := "Start the stack now?"
		if now {
			q = "Start the stack when that's done?"
		}
		start = p.Confirm(q, true)
	}
	if now {
		cmd := map[string]string{"seed": "seed", "resync": "reset"}[bootstrap]
		if code, err := runCommand(run, cmd, nil); err != nil || code != 0 {
			return code, err
		}
	}
	if start && (now || bootstrap == "keep" || bootstrap == "genesis") {
		if code, err := runCommand(run, "start", nil); err != nil || code != 0 {
			return code, err
		}
	} else {
		start = false
	}
	initNotes(p, st, profiles, bootstrap, now, start)
	return 0, nil
}

// initNotes ends init like a Helm chart's NOTES: what was generated and how to read it back (values are
// never printed), where the services are, and what's next.
func initNotes(p Prompter, st *initState, profiles []string, bootstrap string, done, started bool) {
	p.Say("\nNotes:")
	if len(st.generated) > 0 {
		p.Say("  Generated and stored in .env (read them with sbd, or grep with plain Compose):")
		for _, k := range st.generated {
			p.Say("    sbd env get %-24s # grep '^%s=' .env", k, k)
		}
	}
	p.Say("  API:        http://127.0.0.1:%s (this host only)", orDefault(st.get("STACKS_BLOCKCHAIN_API_PORT"), "3999"))
	p.Say("  Node RPC:   http://127.0.0.1:%s/v2/info", orDefault(st.get("STACKS_CORE_RPC_PORT"), "20443"))
	if slices.Contains(profiles, "proxy") {
		p.Say("  Proxy:      http://<this host>:%s (the API, for other machines; terminate HTTPS in front of it)", orDefault(st.get("NGINX_PROXY_PORT"), "80"))
	}
	if slices.Contains(profiles, "monitoring") {
		p.Say("  Grafana:    http://127.0.0.1:%s (user admin)", orDefault(st.get("GRAFANA_PORT"), "3000"))
	}
	if slices.Contains(profiles, "signer") {
		p.Say("  Signer:     signs once the node is at the tip and its key is registered through stacking")
	}
	if !started {
		next := []string{"sbd doctor"}
		switch {
		case bootstrap == "seed" && !done:
			next = append(next, "sbd seed")
		case bootstrap == "resync" && !done:
			next = append(next, "sbd reset")
		}
		p.Say("  Next:       %s", strings.Join(append(next, "sbd start"), ", then "))
	} else {
		p.Say("  Next:       sbd status, sbd logs")
	}
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

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
