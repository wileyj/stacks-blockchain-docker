package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

// `sbd ui` is the live dashboard (refactor/13 §2, T6–T8). It reads the stack and runs the routine
// actions as the CLI itself (`sbd start` etc.), so the two can never disagree; everything destructive
// stays CLI-only. Quitting leaves the stack running.

func init() {
	Commands = append(Commands, &Command{Name: "ui", Usage: "ui", Help: "Live dashboard: services, chain, host, logs; start/stop/restart/pull", Run: uiRun})
}

const (
	logKeep  = 500
	histLen  = 720 // 1 h at the 5 s chain poll
	pollTime = 5 * time.Second
)

func uiRun(c *Ctx, args []string) (int, error) {
	if len(args) > 0 {
		return 2, usagef("ui: unexpected argument %q", args[0])
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return 2, usagef("ui needs a terminal; for scripts use sbd status --json")
	}
	if c.Remote {
		return 2, usagef("ui runs on the node's host for now (it reads the node and API on localhost); run it there")
	}
	d, ok := c.Docker.(execDocker)
	if !ok {
		return 1, errors.New("ui needs the docker CLI")
	}
	cfg, err := loadProjectConfig(d)
	if err != nil {
		return 1, err
	}
	self, err := os.Executable()
	if err != nil {
		return 1, err
	}
	m := newUIModel(c, self)
	// The terminal's Ctrl-C reaches an action's docker command (which decides how to stop), not the
	// dashboard: in the dashboard itself Ctrl-C is a key press
	p := tea.NewProgram(m, tea.WithFPS(10), tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg {
		if _, ok := msg.(tea.InterruptMsg); ok {
			return nil
		}
		return msg
	}))
	done := make(chan struct{})
	refresh := make(chan struct{}, 1)
	m.logs = &logFollower{d: d, send: p.Send}
	cgroups := map[string]string{} // used by the stats stream only
	// Stats arrive a line per container per second; the screen gets them as one message a second
	var smu sync.Mutex
	pending := statsMsg{}
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
			}
			smu.Lock()
			batch := pending
			pending = statsMsg{}
			smu.Unlock()
			if len(batch) > 0 {
				p.Send(batch)
			}
		}
	}()
	streams := []*exec.Cmd{
		streamLines(d, []string{"stats", "--format", "json"}, func(line string) {
			if r, ok := parseStatsLine(line); ok {
				s := statsSample{r, cgroupIO(r.ID, cgroups), time.Now()}
				smu.Lock()
				pending[r.Name] = s
				smu.Unlock()
			}
		}),
		streamLines(d, []string{"events", "--format", "{{.Action}}", "--filter", "type=container",
			"--filter", "label=com.docker.compose.project=" + cfg.Name}, func(string) {
			select {
			case refresh <- struct{}{}:
			default:
			}
		}),
	}
	go poll(p, c, refresh, done)
	_, err = p.Run()
	close(done)
	m.logs.stop()
	for _, s := range streams {
		if s != nil && s.Process != nil {
			s.Process.Kill()
		}
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}

// streamLines runs a long-lived docker command and calls fn for each line of its output.
func streamLines(d execDocker, args []string, fn func(string)) *exec.Cmd {
	cmd := d.cmd(args)
	out, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		return nil
	}
	go func() {
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			fn(sc.Text())
		}
		cmd.Wait()
	}()
	return cmd
}

// poll refreshes the containers every 5 s and whenever docker reports a change, and the chain and
// host with every 5 s tick.
func poll(p *tea.Program, c *Ctx, refresh <-chan struct{}, done <-chan struct{}) {
	client := &http.Client{Timeout: 3 * time.Second}
	rpcPort := portSetting(c, "STACKS_CORE_RPC_PORT", 20443)
	apiPort := portSetting(c, "STACKS_BLOCKCHAIN_API_PORT", 3999)
	signers := map[int64]int{}
	t := time.NewTicker(pollTime)
	defer t.Stop()
	for full := true; ; {
		svcs, err := collectServices(c.Docker)
		p.Send(svcMsg{svcs, err})
		if full {
			ci := fetchChain(client, endpoint(svcs, "stacks-blockchain", rpcPort),
				endpoint(svcs, "stacks-blockchain-api", apiPort), c.Value(refKey), signers)
			p.Send(chainMsg{ci, time.Now()})
			p.Send(hostMsg(collectHost(c)))
		}
		select {
		case <-done:
			return
		case <-t.C:
			full = true
		case <-refresh:
			full = false
		}
	}
}

func portSetting(c *Ctx, key string, def int) int {
	if n, err := strconv.Atoi(c.Value(key)); err == nil {
		return n
	}
	return def
}

// logFollower follows one service's log at a time; lines of a replaced follower are dropped by gen.
type logFollower struct {
	d    execDocker
	send func(tea.Msg)
	mu   sync.Mutex
	cmd  *exec.Cmd
	gen  int
}

// logMsg carries the lines of a quarter second: a busy node logs dozens a second, and drawing the
// screen for each would cost more CPU than the rest of the dashboard.
type logMsg struct {
	gen   int
	lines []string
}

func (f *logFollower) follow(service string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopLocked()
	f.gen++
	gen := f.gen
	lines := make(chan string, 1000)
	f.cmd = streamLines(f.d, []string{"compose", "--profile", "*", "logs", "-f", "--no-color", "--no-log-prefix", "--tail", "200", service},
		func(line string) {
			select {
			case lines <- line:
			default: // the screen can't show that many anyway
			}
		})
	go func() {
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		for range t.C {
			var batch []string
		drain:
			for {
				select {
				case l := <-lines:
					batch = append(batch, l)
				default:
					break drain
				}
			}
			if f.current() != gen {
				return
			}
			if len(batch) > 0 {
				f.send(logMsg{gen, batch})
			}
		}
	}()
}

func (f *logFollower) current() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gen
}

func (f *logFollower) stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopLocked()
}

func (f *logFollower) stopLocked() {
	if f.cmd != nil && f.cmd.Process != nil {
		f.cmd.Process.Kill()
	}
	f.cmd = nil
}

type (
	svcMsg struct {
		svcs []uiService
		err  error
	}
	statsSample struct {
		row statsRow
		io  ioCount
		at  time.Time
	}
	statsMsg map[string]statsSample // by container name
	chainMsg struct {
		ci chainInfo
		at time.Time
	}
	hostMsg   hostInfo
	clockMsg  time.Time
	actionMsg struct {
		name string
		err  error
	}
)

type uiModel struct {
	c        *Ctx
	self     string
	logs     *logFollower
	w, h     int
	now      time.Time
	svcs     []uiService
	svcErr   string
	use      map[string]*resUse // by container name
	chain    chainInfo
	chainAt  time.Time
	host     hostInfo
	cpuHist  *ring
	tipHist  *ring
	logSvc   string
	lines    []string
	warns    []string // warning and error lines, kept apart so the chatty INFO lines don't push them out
	warnOnly bool
	logView  bool   // the full-screen log view; logs are followed only while it is open
	confirm  string // the action awaiting y/N
	help     bool
	status   string // result of the last action
}

func newUIModel(c *Ctx, self string) *uiModel {
	return &uiModel{c: c, self: self, now: time.Now(), use: map[string]*resUse{},
		cpuHist: newRing(histLen), tipHist: newRing(histLen), logSvc: "stacks-blockchain"}
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return clockMsg(t) })
}

func (m *uiModel) followLogs() tea.Cmd {
	m.lines, m.warns = nil, nil
	svc := m.logSvc
	return func() tea.Msg {
		if m.logs != nil {
			m.logs.follow(svc)
		}
		return nil
	}
}

func (m *uiModel) closeLogs() {
	m.logView, m.lines, m.warns = false, nil, nil
	if m.logs != nil {
		m.logs.stop()
	}
}

func (m *uiModel) Init() tea.Cmd { return tick() }

// shownServices are the containers to list: running ones, and ones that failed. A job that finished
// (seed, config-check) is not a service.
func (m *uiModel) shownServices() []uiService {
	var out []uiService
	for _, s := range m.svcs {
		if s.State != "exited" || s.ExitCode != 0 {
			out = append(out, s)
		}
	}
	rank := map[string]int{"stacks-blockchain": 0, "stacks-blockchain-api": 1, "postgres": 2, "stacks-signer": 3, "nginx": 4}
	sort.SliceStable(out, func(i, j int) bool {
		ri, ok := rank[out[i].Service]
		if !ok {
			ri = 10
		}
		rj, ok := rank[out[j].Service]
		if !ok {
			rj = 10
		}
		if ri != rj {
			return ri < rj
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (m *uiModel) container(service string) string {
	for _, s := range m.svcs {
		if s.Service == service {
			return s.Name
		}
	}
	return ""
}

// actions run the CLI itself in the terminal (its own output and progress), then return.
var actions = map[string][]string{
	"s": {"start"}, "x": {"stop"}, "r": {"restart"}, "p": {"pull"}, "e": {"logs", "export"},
}

var confirmText = map[string]string{
	"x": "Stop the stack? The node stops first (up to STACKS_SHUTDOWN_TIMEOUT). y/N",
	"r": "Restart the stack? The node is offline until it's back. y/N",
}

func (m *uiModel) run(key string) tea.Cmd {
	args := actions[key]
	a := &actionCmd{self: m.self, dir: m.c.Dir, args: args}
	name := strings.Join(args, " ")
	return tea.Exec(a, func(err error) tea.Msg { return actionMsg{name, err} })
}

func (m *uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case clockMsg:
		m.now = time.Time(msg)
		return m, tick()
	case svcMsg:
		m.svcs, m.svcErr = msg.svcs, ""
		if msg.err != nil {
			m.svcErr = msg.err.Error()
		}
	case statsMsg:
		for name, st := range msg {
			u := m.use[name]
			if u == nil {
				u = &resUse{}
				m.use[name] = u
			}
			u.update(st.row, st.io, st.at)
		}
	case chainMsg:
		m.chain, m.chainAt = msg.ci, msg.at
		if u := m.use[m.container("stacks-blockchain")]; u != nil {
			m.cpuHist.add(u.CPU)
		}
		if msg.ci.Tip > 0 {
			m.tipHist.add(float64(msg.ci.Tip))
		}
	case hostMsg:
		m.host = hostInfo(msg)
	case logMsg:
		if m.logs != nil && msg.gen != m.logs.current() {
			break
		}
		for _, l := range msg.lines {
			l = cleanLine(l)
			m.lines = append(m.lines, l)
			if warnRe.MatchString(l) {
				m.warns = append(m.warns, l)
			}
		}
		m.lines, m.warns = keepLast(m.lines, logKeep), keepLast(m.warns, logKeep)
	case actionMsg:
		m.status = "sbd " + msg.name + ": done"
		if msg.err != nil {
			m.status = "sbd " + msg.name + ": " + msg.err.Error()
		}
		if m.logView {
			return m, m.followLogs()
		}
	case tea.KeyPressMsg:
		return m.key(msg.String())
	}
	return m, nil
}

func (m *uiModel) key(k string) (tea.Model, tea.Cmd) {
	if m.confirm != "" {
		a := m.confirm
		m.confirm = ""
		if k == "y" || k == "Y" {
			return m, m.run(a)
		}
		m.status = "cancelled"
		return m, nil
	}
	if m.help {
		m.help = false
		if k != "q" && k != "ctrl+c" {
			return m, nil
		}
	}
	switch k {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "?", "h":
		m.help = true
	case "x", "r":
		m.confirm = k
	case "s", "p", "e":
		return m, m.run(k)
	case "w":
		m.warnOnly = !m.warnOnly
	case "esc":
		if m.logView {
			m.closeLogs()
		}
	case "l":
		if !m.logView {
			m.logView = true
			return m, m.followLogs()
		}
		var names []string
		for _, s := range m.shownServices() {
			if !slices.Contains(names, s.Service) {
				names = append(names, s.Service)
			}
		}
		if len(names) > 0 {
			i := slices.Index(names, m.logSvc)
			m.logSvc = names[(i+1)%len(names)]
			return m, m.followLogs()
		}
	}
	return m, nil
}

func keepLast(l []string, n int) []string {
	if len(l) > n {
		return l[len(l)-n:]
	}
	return l
}

func cleanLine(s string) string {
	return strings.ReplaceAll(ansi.Strip(s), "\t", "    ")
}

var (
	stOK   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	stWarn = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	stBad  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	stDim  = lipgloss.NewStyle().Faint(true)
	stHead = lipgloss.NewStyle().Bold(true)
)

func level(s string, lv int) string {
	switch lv {
	case lvOK:
		return stOK.Render(s)
	case lvWarn:
		return stWarn.Render(s)
	}
	return stBad.Render(s)
}

// fit truncates or pads a styled string to exactly w cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = ansi.Truncate(s, w, "…")
	if n := lipgloss.Width(s); n < w {
		s += strings.Repeat(" ", w-n)
	}
	return s
}

func rule(title string, w int) string {
	t := "── " + title + " "
	return stDim.Render(t + strings.Repeat("─", max(w-lipgloss.Width(t), 0)))
}

func (m *uiModel) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "sbd ui"
	return v
}

func (m *uiModel) render() string {
	w, h := m.w, m.h
	if w <= 0 || h <= 0 {
		w, h = 80, 24
	}
	if m.help {
		return clip(m.helpLines(), w, h)
	}
	if m.logView {
		return m.renderLogs(w, h)
	}
	var top []string
	title := stHead.Render(fmt.Sprintf("sbd ui · %s · %s", m.c.Value("NETWORK"), m.c.Value("COMPOSE_PROFILES")))
	clock := m.now.Format("15:04:05")
	top = append(top, fit(title, w-len(clock)-1)+" "+clock)
	top = append(top, m.serviceLines(w)...)
	left, right := m.chainLines(), m.hostLines()
	if w >= 80 {
		lw := w / 2
		top = append(top, fit(rule("CHAIN", lw-1)+" "+rule("HOST", w-lw), w))
		for i := 0; i < max(len(left), len(right)); i++ {
			var l, r string
			if i < len(left) {
				l = left[i]
			}
			if i < len(right) {
				r = right[i]
			}
			top = append(top, fit(" "+l, lw)+fit(r, w-lw))
		}
	} else {
		top = append(top, rule("CHAIN", w))
		for _, l := range left {
			top = append(top, " "+l)
		}
		top = append(top, rule("HOST", w))
		for _, l := range right {
			top = append(top, " "+l)
		}
	}
	// Problems before PoX: on a short terminal the bottom panel is the one cut
	top = append(top, rule("PROBLEMS", w))
	for _, p := range m.problems() {
		top = append(top, " "+level(p.text, p.lv))
	}
	top = append(top, rule("POX", w))
	for _, l := range m.poxLines() {
		top = append(top, " "+l)
	}
	keys := "s start  x stop  r restart  p pull  l logs  e export  ? help  q quit"
	return m.screen(top, nil, keys, w, h)
}

// screen lays out a view: top, then the tail of fill, then the status line and the keys at the bottom.
func (m *uiModel) screen(top, fill []string, keys string, w, h int) string {
	var bottom []string
	switch {
	case m.confirm != "":
		bottom = append(bottom, stWarn.Render(confirmText[m.confirm]))
	case m.status != "" || m.svcErr != "":
		s := m.status
		if m.svcErr != "" {
			s = stBad.Render("docker: " + m.svcErr)
		}
		bottom = append(bottom, s)
	}
	if w < lipgloss.Width(keys) {
		keys = "? keys  q quit"
	}
	bottom = append(bottom, stDim.Render(keys))
	room := max(h-len(bottom), 0)
	if len(top) > room {
		top = top[:room]
	}
	if n := room - len(top); len(fill) > n {
		fill = fill[len(fill)-n:]
	}
	out := append(top, fill...)
	for len(out) < room {
		out = append(out, "")
	}
	return clip(append(out, bottom...), w, h)
}

// renderLogs is the full-screen log view of one service.
func (m *uiModel) renderLogs(w, h int) string {
	filter := ""
	logs := m.lines
	if m.warnOnly {
		filter = ", WARN+"
		logs = m.warns
		if len(logs) == 0 {
			logs = []string{stDim.Render(fmt.Sprintf("no warnings or errors in the last %d lines (w shows all)", len(m.lines)))}
		}
	}
	top := []string{rule("LOGS "+m.logSvc+filter, w)}
	return m.screen(top, logs, "l next service  w warn+  esc back  q quit", w, h)
}

// clip makes every line exactly w wide and keeps the first h lines.
func clip(lines []string, w, h int) string {
	if len(lines) > h {
		lines = lines[:h]
	}
	for i, l := range lines {
		lines[i] = fit(l, w)
	}
	return strings.Join(lines, "\n")
}

func (m *uiModel) serviceLines(w int) []string {
	wide := w >= 80
	row := func(svc, state, up, cpu, mem, io, rst string) string {
		if wide {
			return fit(svc, 22) + " " + fit(state, 9) + " " + fmt.Sprintf("%6s %6s %10s %13s %4s", up, cpu, mem, io, rst)
		}
		return fit(svc, max(w-36, 8)) + " " + fit(state, 9) + " " + fmt.Sprintf("%6s %10s", cpu, mem)
	}
	lines := []string{stDim.Render(row("SERVICE", "STATE", "UP", "CPU", "MEM", "IO R/W", "RST"))}
	svcs := m.shownServices()
	if len(svcs) == 0 {
		return append(lines, stWarn.Render("no containers: the stack isn't running (s starts it)"))
	}
	for _, s := range svcs {
		state, lv := s.State, lvBad
		switch {
		case s.State == "running" && s.Health == "unhealthy":
			state, lv = "unhealthy", lvBad
		case s.State == "running" && s.Health == "starting":
			state, lv = "starting", lvWarn
		case s.State == "running" && s.Health == "healthy":
			state, lv = "healthy", lvOK
		case s.State == "running":
			lv = lvOK
		case s.State == "restarting":
			lv = lvWarn
		}
		up, cpu, mem, io, rst := "-", "-", "-", "-", strconv.Itoa(s.Restarts)
		if s.State == "running" {
			if !s.Started.IsZero() {
				up = shortDur(m.now.Sub(s.Started))
			}
			if u := m.use[s.Name]; u != nil {
				cpu, mem = fmt.Sprintf("%.0f%%", u.CPU), bytesIEC(u.Mem)
				if u.HasIO {
					io = rate(u.ReadRate) + "/" + rate(u.WriteRate)
				}
			}
		}
		rl := lvOK
		if s.Restarts > 0 {
			rl = lvWarn
		}
		l := row(s.Service, level(state, lv), up, cpu, mem, io, "")
		if wide {
			l = strings.TrimRight(l, " ") + " " + level(fmt.Sprintf("%3s", rst), rl)
		}
		lines = append(lines, l)
	}
	return lines
}

func (m *uiModel) chainLines() []string {
	ci := m.chain
	if m.chainAt.IsZero() {
		return []string{stDim.Render("waiting for the first poll…")}
	}
	var l []string
	if ci.NodeErr != "" {
		l = append(l, "node    "+stBad.Render(ci.NodeErr))
	} else {
		tip := "tip     " + commas(ci.Tip)
		if vals := m.tipHist.values(); len(vals) > 12 {
			tip += stDim.Render(fmt.Sprintf("  +%.0f/min", vals[len(vals)-1]-vals[len(vals)-13]))
		}
		l = append(l, tip)
	}
	s, lv := syncStatus(ci, m.now)
	l = append(l, "status  "+level(s, lv))
	if ci.NodeErr == "" {
		burn := "burn    " + commas(ci.Burn)
		if !ci.BurnTime.IsZero() {
			age := m.now.Sub(ci.BurnTime)
			blv := lvOK
			if age > burnStaleAge {
				blv = lvWarn
			}
			burn += "  " + level("block "+shortDur(max(age, 0))+" ago", blv)
		}
		l = append(l, burn)
	}
	if ci.APIErr != "" && ci.APITip == 0 {
		l = append(l, "api     "+stBad.Render(ci.APIErr))
	} else {
		lag := ci.Tip - ci.APITip
		llv := lvOK
		if lag > refTolerance {
			llv = lvWarn
		}
		l = append(l, "api     "+commas(ci.APITip)+"  "+level("lag "+commas(lag), llv))
	}
	if ci.NodeErr == "" {
		plv := lvOK
		if ci.PeersIn+ci.PeersOut == 0 {
			plv = lvBad
		}
		l = append(l, "peers   "+level(fmt.Sprintf("%d out, %d in", ci.PeersOut, ci.PeersIn), plv))
	}
	return l
}

func (m *uiModel) hostLines() []string {
	h := m.host
	var l []string
	if len(h.Disks) > 0 {
		var parts []string
		for _, d := range h.Disks {
			parts = append(parts, d.Label+" "+bytesIEC(d.Free))
		}
		l = append(l, "free    "+strings.Join(parts, ", "))
	}
	if h.Load != "" {
		l = append(l, "load    "+h.Load)
	}
	if h.MemTotal > 0 {
		l = append(l, fmt.Sprintf("memory  %s used of %s", bytesIEC(h.MemTotal-h.MemAvail), bytesIEC(h.MemTotal)))
	}
	cpu := m.cpuHist.values()
	if len(cpu) > 0 {
		l = append(l, fmt.Sprintf("cpu     %s %.0f%%", sparkline(cpu, 24), cpu[len(cpu)-1]))
	}
	if tips := m.tipHist.values(); len(tips) > 1 {
		l = append(l, "tip     "+sparkline(tips, 24))
	}
	return l
}

type problem struct {
	text string
	lv   int
}

// problems are what needs a look, from state only: containers, whether the node and the API answer,
// and the chain checks. Log messages change between releases, so none are matched (owner, 2026-11-09).
func (m *uiModel) problems() []problem {
	if m.chainAt.IsZero() {
		return []problem{{"checking…", lvOK}}
	}
	var ps []problem
	add := func(lv int, format string, a ...any) { ps = append(ps, problem{fmt.Sprintf(format, a...), lv}) }
	running := map[string]bool{}
	for _, s := range m.shownServices() {
		switch {
		case s.State == "exited":
			add(lvBad, "%s exited with code %d", s.Service, s.ExitCode)
		case s.State == "restarting":
			add(lvBad, "%s is restarting (%d restarts)", s.Service, s.Restarts)
		case s.State == "running" && s.Health == "unhealthy":
			add(lvBad, "%s is unhealthy", s.Service)
		case s.State == "running" && s.Restarts > 0 && m.now.Sub(s.Started) < 15*time.Minute:
			add(lvWarn, "%s restarted %s ago (%d restarts)", s.Service, shortDur(m.now.Sub(s.Started)), s.Restarts)
		}
		running[s.Service] = running[s.Service] || s.State == "running"
	}
	ci := m.chain
	if running["stacks-blockchain"] && ci.NodeErr != "" {
		add(lvBad, "the node doesn't answer on its RPC port: %s", ci.NodeErr)
	}
	if running["stacks-blockchain-api"] && ci.APIErr != "" {
		add(lvBad, "the API doesn't answer: %s", ci.APIErr)
	}
	if ci.NodeErr == "" && ci.APIErr == "" {
		if s, lv := syncStatus(ci, m.now); lv != lvOK {
			add(lv, "%s", s)
		}
		if lag := ci.Tip - ci.APITip; lag > refTolerance {
			add(lvWarn, "the API is %s blocks behind the node: is it receiving blocks?", commas(lag))
		}
	}
	if !ci.BurnTime.IsZero() && m.now.Sub(ci.BurnTime) > burnStaleAge {
		add(lvWarn, "no new Bitcoin block for %s: check the Bitcoin peer (BTC_HOST)", shortDur(m.now.Sub(ci.BurnTime)))
	}
	if ci.NodeErr == "" && ci.PeersIn+ci.PeersOut == 0 {
		add(lvBad, "the node has no peers")
	}
	if len(ps) == 0 {
		return []problem{{"none", lvOK}}
	}
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].lv > ps[j].lv })
	return ps
}

// poxLines show the stacking cycle and how many signers signed the latest block. The count is of
// signatures, not signing weight (blocks need 70% of the weight): the API doesn't say who signed.
func (m *uiModel) poxLines() []string {
	ci := m.chain
	if ci.Pox == nil {
		return []string{stDim.Render("no PoX data from the node yet")}
	}
	p := ci.Pox
	eta := func(blocks int64) string { return shortDur(time.Duration(blocks) * 10 * time.Minute) }
	cycle := fmt.Sprintf("cycle   %d, reward phase · %d starts in %s blocks (~%s)", p.Current.ID, p.Next.ID, commas(p.Next.UntilReward), eta(p.Next.UntilReward))
	if p.Next.UntilPrepare <= 0 {
		cycle = fmt.Sprintf("cycle   %d, prepare phase for %d · starts in %s blocks (~%s)", p.Current.ID, p.Next.ID, commas(p.Next.UntilReward), eta(p.Next.UntilReward))
	}
	l := []string{cycle, fmt.Sprintf("stacked %s · %d so far %s · minimum %s", stx(p.Current.Stacked), p.Next.ID, stx(p.Next.Stacked), stx(p.MinUSTX))}
	switch {
	case ci.Signatures >= 0 && ci.Signers > 0:
		l = append(l, fmt.Sprintf("signed  %d signatures on block %s (%d signers in cycle %d)", ci.Signatures, commas(ci.APITip), ci.Signers, p.Current.ID))
	case ci.Signatures >= 0:
		l = append(l, fmt.Sprintf("signed  %d signatures on block %s", ci.Signatures, commas(ci.APITip)))
	}
	return l
}

func (m *uiModel) helpLines() []string {
	return []string{
		stHead.Render("sbd ui: keys"),
		"",
		"  s  start      sbd start (the services in COMPOSE_PROFILES)",
		"  x  stop       sbd stop (asks first; the node stops first)",
		"  r  restart    sbd restart (asks first)",
		"  p  pull       sbd pull (then restart to use the new images)",
		"  e  export     sbd logs export (to exported-logs/)",
		"  l  logs       full-screen log: l next service, w warnings only, esc back",
		"  q  quit       the stack keeps running",
		"",
		stHead.Render("In a shell (not in the dashboard)"),
		"",
		"  sbd seed                  restore from the Hiro archive (stack stopped)",
		"  sbd reset                 delete this network's chainstate and API database",
		"  sbd db upgrade            upgrade the API database to POSTGRES_VERSION",
		"  sbd env set KEY=VALUE     change a setting, e.g. COMPOSE_PROFILES",
		"  sbd init, sbd migrate, sbd bns, sbd doctor",
		"",
		"Every action is a plain docker compose command: sbd plan <command> shows it.",
		"",
		stDim.Render("any key returns"),
	}
}

// actionCmd runs `sbd <args>` in the terminal while the dashboard is paused, then waits for Enter
// so its output can be read.
type actionCmd struct {
	self, dir string
	args      []string
	in        io.Reader
	out, err  io.Writer
}

func (a *actionCmd) SetStdin(r io.Reader)  { a.in = r }
func (a *actionCmd) SetStdout(w io.Writer) { a.out = w }
func (a *actionCmd) SetStderr(w io.Writer) { a.err = w }

func (a *actionCmd) Run() error {
	fmt.Fprintf(a.out, "$ sbd %s\n", strings.Join(a.args, " "))
	cmd := exec.Command(a.self, append([]string{"--project-dir", a.dir}, a.args...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = a.in, a.out, a.err
	err := cmd.Run()
	fmt.Fprint(a.out, "\nPress Enter to return to the dashboard.")
	bufio.NewReader(a.in).ReadString('\n')
	return err
}
