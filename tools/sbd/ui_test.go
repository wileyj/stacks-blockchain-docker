package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestParseStatsLine(t *testing.T) {
	// As the stream prints it: cursor home before, clear-line after
	line := "\x1b[H{\"BlockIO\":\"14.5GB / 138GB\",\"CPUPerc\":\"61.20%\",\"MemUsage\":\"3.1GiB / 62.54GiB\",\"Name\":\"sbd-soak-a-stacks-blockchain\"}\x1b[K"
	r, ok := parseStatsLine(line)
	if !ok || r.Name != "sbd-soak-a-stacks-blockchain" || r.CPUPerc != "61.20%" {
		t.Fatalf("got %+v %v", r, ok)
	}
	if _, ok := parseStatsLine("\x1b[J"); ok {
		t.Error("a line without JSON parsed")
	}
}

func TestParseSize(t *testing.T) {
	for in, want := range map[string]uint64{
		"0B": 0, "13.5MB": 13_500_000, "1.2kB": 1200, "40.73MiB": 42_708_500, "3GiB": 3 << 30, "138GB": 138e9,
	} {
		if got, ok := parseSize(in); !ok || got != want {
			t.Errorf("parseSize(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	if _, ok := parseSize("--"); ok {
		t.Error("parsed --")
	}
}

func TestUsageRates(t *testing.T) {
	var u resUse
	t0 := time.Unix(1000, 0)
	u.update(statsRow{CPUPerc: "50.5%", MemUsage: "1GiB / 8GiB"}, ioCount{10e6, 20e6, true}, t0)
	if u.CPU != 50.5 || u.Mem != 1<<30 || !u.HasIO || u.ReadRate != 0 {
		t.Fatalf("first sample: %+v", u)
	}
	u.update(statsRow{CPUPerc: "1%", MemUsage: "1GiB / 8GiB"}, ioCount{12e6, 30e6, true}, t0.Add(2*time.Second))
	if u.ReadRate != 1e6 || u.WriteRate != 5e6 {
		t.Errorf("rates %v/%v, want 1e6/5e6", u.ReadRate, u.WriteRate)
	}
	u.update(statsRow{CPUPerc: "1%"}, ioCount{}, t0.Add(3*time.Second))
	if u.HasIO {
		t.Error("IO shown without a cgroup")
	}
}

func TestParseIOStat(t *testing.T) {
	// Run A: the LV (254:3) and the NVMe disk under it (259:0) carry the same IO
	c := parseIOStat("254:1 rbytes=987136 wbytes=0 rios=29 wios=0 dbytes=0 dios=0\n" +
		"259:0 rbytes=20638892032 wbytes=269937957888 rios=4200259 wios=13474513 dbytes=0 dios=0\n" +
		"254:3 rbytes=20637904896 wbytes=269937957888 rios=4200230 wios=12583300 dbytes=0 dios=0\n")
	if !c.ok || c.read != 20638892032 || c.written != 269937957888 {
		t.Errorf("got %+v", c)
	}
	if parseIOStat("").ok {
		t.Error("empty io.stat is ok")
	}
}

func TestEndpoint(t *testing.T) {
	svcs := []uiService{
		{psRow: psRow{Service: "stacks-blockchain-api", State: "running", Publishers: []publisher{{URL: "127.0.0.1", TargetPort: 3999, PublishedPort: 3999}}}, IP: "172.18.0.9"},
		{psRow: psRow{Service: "stacks-blockchain", State: "running", Publishers: []publisher{{URL: "0.0.0.0", TargetPort: 20443, PublishedPort: 21443}}}},
		{psRow: psRow{Service: "postgres", State: "running", Publishers: []publisher{{TargetPort: 5432}}}, IP: "172.18.0.3"},
		{psRow: psRow{Service: "stacks-signer", State: "exited"}, IP: "172.18.0.5"},
	}
	if got := endpoint(svcs, "stacks-blockchain-api", 3999); got != "http://127.0.0.1:3999" {
		t.Errorf("api: %q", got)
	}
	if got := endpoint(svcs, "stacks-blockchain", 20443); got != "http://127.0.0.1:21443" {
		t.Errorf("node on all interfaces: %q", got)
	}
	want := ""
	if runtime.GOOS == "linux" {
		want = "http://172.18.0.3:5432"
	}
	if got := endpoint(svcs, "postgres", 5432); got != want {
		t.Errorf("unpublished: %q, want %q", got, want)
	}
	if got := endpoint(svcs, "stacks-signer", 30000); got != "" {
		t.Errorf("stopped service: %q", got)
	}
}

func TestSyncStatus(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	cases := []struct {
		ci   chainInfo
		want string
		lv   int
	}{
		{chainInfo{Tip: 100, BlockTime: now.Add(-29 * time.Second)}, "synced ✔ latest block 29s ago", lvOK},
		{chainInfo{Tip: 100, BlockTime: now.Add(-(3*time.Hour + 12*time.Minute))}, "behind: latest block 3h12m old", lvWarn},
		{chainInfo{Tip: 100}, "unknown: no block from the API", lvWarn},
		// A public tip is a moving target: up to 15 blocks short is synced
		{chainInfo{Tip: 100, RefSet: true, RefTip: 115, BlockTime: now.Add(-time.Hour)}, "synced ✔ (reference 115)", lvOK},
		{chainInfo{Tip: 100, RefSet: true, RefTip: 116, BlockTime: now}, "16 blocks behind reference", lvWarn},
		{chainInfo{Tip: 100, RefSet: true, RefErr: "timeout", BlockTime: now.Add(-10 * time.Second)}, "synced ✔ latest block 10s ago (no reference)", lvOK},
	}
	for _, c := range cases {
		got, lv := syncStatus(c.ci, now)
		if got != c.want || lv != c.lv {
			t.Errorf("%+v: got %q/%d, want %q/%d", c.ci, got, lv, c.want, c.lv)
		}
	}
}

func TestFetchChain(t *testing.T) {
	now := time.Now().Unix()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/info":
			fmt.Fprint(w, `{"stacks_tip_height":9449931,"burn_block_height":975206}`)
		case "/v2/neighbors":
			fmt.Fprint(w, `{"sample":[{},{}],"inbound":[],"outbound":[{},{},{}]}`)
		case "/extended/v2/blocks":
			fmt.Fprintf(w, `{"results":[{"height":9449930,"block_time":%d}]}`, now-12)
		case "/extended/v2/burn-blocks":
			fmt.Fprintf(w, `{"results":[{"burn_block_time":%d}]}`, now-600)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ci := fetchChain(srv.Client(), srv.URL, srv.URL, "")
	if ci.NodeErr != "" || ci.APIErr != "" || ci.Tip != 9449931 || ci.Burn != 975206 || ci.PeersOut != 3 ||
		ci.APITip != 9449930 || ci.BlockTime.Unix() != now-12 || ci.BurnTime.Unix() != now-600 || ci.RefSet {
		t.Fatalf("got %+v", ci)
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	ci = fetchChain(srv.Client(), closed.URL, srv.URL+"/nope", "")
	if ci.NodeErr != "connection refused" || ci.APIErr != "404 Not Found" {
		t.Errorf("errors: node %q, api %q", ci.NodeErr, ci.APIErr)
	}
	ci = fetchChain(srv.Client(), "", srv.URL, srv.URL+"/")
	if ci.NodeErr != "not reachable" || !ci.RefSet || ci.RefTip != 9449931 {
		t.Fatalf("without the node, with a reference: %+v", ci)
	}
}

func TestCollectServices(t *testing.T) {
	ps := `{"Service":"stacks-blockchain","Name":"sbd-x-stacks-blockchain","State":"running","Publishers":[{"URL":"","TargetPort":20443,"PublishedPort":0}]}
{"Service":"seed","Name":"sbd-x-seed-1","State":"exited"}`
	started := time.Date(2026, 11, 6, 22, 4, 18, 0, time.UTC)
	ins, _ := json.Marshal([]map[string]any{{"Name": "/sbd-x-stacks-blockchain", "RestartCount": 2,
		"State": map[string]any{"StartedAt": started}, "NetworkSettings": map[string]any{"Networks": map[string]any{"sbd-x": map[string]any{"IPAddress": "172.18.0.4"}}}}})
	svcs, err := collectServices(&fakeDocker{ps: ps, inspect: string(ins)})
	if err != nil || len(svcs) != 2 {
		t.Fatalf("%v %+v", err, svcs)
	}
	if s := svcs[0]; s.Restarts != 2 || !s.Started.Equal(started) || s.IP != "172.18.0.4" {
		t.Errorf("inspect not merged: %+v", s)
	}
}

func TestHostParsers(t *testing.T) {
	if got := parseLoadavg("1.80 1.50 1.20 3/1200 4567\n"); got != "1.80 1.50 1.20" {
		t.Errorf("loadavg %q", got)
	}
	total, avail := parseMeminfo(strings.NewReader("MemTotal:       65536000 kB\nMemFree:  100 kB\nMemAvailable:   32768000 kB\n"))
	if total != 65536000*1024 || avail != 32768000*1024 {
		t.Errorf("meminfo %d %d", total, avail)
	}
}

func TestWarnFilter(t *testing.T) {
	for line, want := range map[string]bool{
		"WARN [1731000000.1] [src/net/p2p.rs:1] connection refused":       true,
		"ERRO [1731000000.1] [src/chainstate] failed":                     true,
		"INFO [1731000000.1] [src/main.rs] Miner: false":                  false,
		`{"level":40,"msg":"slow query"}`:                                 true,
		`{"level":30,"msg":"POST /new_block 200"}`:                        false,
		"2026-11-09 10:00:00 UTC [42] WARNING:  checkpoints too frequent": true,
		"2026-11-09 10:00:00 UTC [42] LOG:  checkpoint complete":          false,
	} {
		if got := warnRe.MatchString(line); got != want {
			t.Errorf("%q: %v, want %v", line, got, want)
		}
	}
}

func TestRingAndSparkline(t *testing.T) {
	r := newRing(4)
	for i := 1; i <= 6; i++ {
		r.add(float64(i))
	}
	if got := fmt.Sprint(r.values()); got != "[3 4 5 6]" {
		t.Errorf("ring %s", got)
	}
	if got := sparkline([]float64{3, 0, 7, 14}, 3); got != "▁▄█" {
		t.Errorf("sparkline %q", got)
	}
	if got := sparkline([]float64{5, 5}, 10); got != "▁▁" {
		t.Errorf("flat sparkline %q", got)
	}
}

func TestFormat(t *testing.T) {
	if commas(9449931) != "9,449,931" || commas(-1234) != "-1,234" || commas(12) != "12" {
		t.Error("commas")
	}
	for d, want := range map[time.Duration]string{12 * time.Second: "12s", 250 * time.Second: "4m10s",
		3*time.Hour + 12*time.Minute: "3h12m", 52 * time.Hour: "2d4h"} {
		if got := shortDur(d); got != want {
			t.Errorf("shortDur(%v) = %q", d, got)
		}
	}
	if bytesIEC(3<<30+100<<20) != "3.1 GiB" || bytesIEC(512) != "512 B" || rate(2.4e6) != "2.4M" || rate(812e3) != "812k" {
		t.Error("sizes")
	}
}

// uiFixture is a dashboard with a running stack and some history.
func uiFixture(t *testing.T) *uiModel {
	t.Helper()
	c := &Ctx{Env: mustParse(t, "NETWORK=mainnet\nCOMPOSE_PROFILES=node,api,monitoring\n")}
	m := newUIModel(c, "sbd")
	now := time.Unix(2_000_000_000, 0)
	m.now = now
	m.svcs = []uiService{
		{psRow: psRow{Service: "prometheus", Name: "sbd-x-prometheus", State: "running"}, Started: now.Add(-50 * time.Hour)},
		{psRow: psRow{Service: "stacks-blockchain-api", Name: "sbd-x-stacks-blockchain-api", State: "running", Health: "healthy"}, Started: now.Add(-50 * time.Hour)},
		{psRow: psRow{Service: "stacks-blockchain", Name: "sbd-x-stacks-blockchain", State: "running"}, Started: now.Add(-50 * time.Hour), Restarts: 1},
		{psRow: psRow{Service: "postgres", Name: "sbd-x-postgres", State: "running"}, Started: now.Add(-50 * time.Hour)},
		{psRow: psRow{Service: "seed", Name: "sbd-x-seed-1", State: "exited"}},
		{psRow: psRow{Service: "stacks-signer", Name: "sbd-x-stacks-signer", State: "exited", ExitCode: 1}},
	}
	m.use["sbd-x-stacks-blockchain"] = &resUse{CPU: 61, Mem: 3 << 30, HasIO: true, ReadRate: 42e6, WriteRate: 118e6}
	m.chain = chainInfo{Tip: 9449931, Burn: 975206, PeersOut: 6, APITip: 9449931, BlockTime: now.Add(-12 * time.Second), BurnTime: now.Add(-28 * time.Minute)}
	m.chainAt = now
	m.host = hostInfo{Disks: []diskFree{{"data", 1300 << 30}}, Load: "1.80 1.50 1.20", MemTotal: 64 << 30, MemAvail: 30 << 30}
	for i := range 20 {
		m.cpuHist.add(float64(40 + i))
		m.tipHist.add(float64(9449900 + i*2))
	}
	for i := range 40 {
		m.lines = append(m.lines, fmt.Sprintf("INFO [%d] line %d", i, i))
	}
	m.lines = append(m.lines, "WARN [99] something")
	m.warns = []string{"WARN [99] something"}
	return m
}

func mustParse(t *testing.T, s string) *Env {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(path, []byte(s), 0o600)
	e, err := LoadEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestRenderFits(t *testing.T) {
	m := uiFixture(t)
	for _, size := range [][2]int{{80, 24}, {120, 40}, {60, 30}, {40, 10}} {
		m.w, m.h = size[0], size[1]
		out := m.render()
		lines := strings.Split(out, "\n")
		if len(lines) != m.h {
			t.Errorf("%dx%d: %d lines", m.w, m.h, len(lines))
		}
		for i, l := range lines {
			if w := lipgloss.Width(l); w != m.w {
				t.Errorf("%dx%d: line %d is %d wide: %q", m.w, m.h, i, w, l)
			}
		}
	}
	m.w, m.h = 80, 24
	out := m.render()
	for _, want := range []string{"sbd ui · mainnet · node,api,monitoring", "stacks-blockchain ", "healthy", "3.0 GiB", "42.0M/118.0M",
		"9,449,931", "synced ✔ latest block 12s ago", "block 28m00s ago", "lag 0", "6 out, 0 in", "data 1.3 TiB", "WARN [99] something", "q quit"} {
		if !strings.Contains(out, want) {
			t.Errorf("80x24 view lacks %q:\n%s", want, out)
		}
	}
	// A finished job is not a service, a failed one shows; the node comes first
	if strings.Contains(out, "seed") || !strings.Contains(out, "stacks-signer") {
		t.Error("the finished seed job is listed, or the failed signer isn't")
	}
	if strings.Index(out, "stacks-blockchain ") > strings.Index(out, "prometheus") {
		t.Error("the node isn't listed first")
	}
}

func TestKeys(t *testing.T) {
	m := uiFixture(t)
	press := func(k string) tea.Cmd {
		_, cmd := m.Update(tea.KeyPressMsg{Code: rune(k[0]), Text: k})
		return cmd
	}
	// Stop and restart ask first; anything but y cancels
	if press("x") != nil || m.confirm != "x" || !strings.Contains(m.render(), "Stop the stack?") {
		t.Fatal("x didn't ask")
	}
	if press("n") != nil || m.confirm != "" || m.status != "cancelled" {
		t.Fatal("n didn't cancel")
	}
	press("r")
	if cmd := press("y"); cmd == nil {
		t.Fatal("y didn't run restart")
	}
	// Start runs without asking
	if cmd := press("s"); cmd == nil || m.confirm != "" {
		t.Fatal("s didn't run")
	}
	press("w")
	out := m.render()
	if strings.Contains(out, "INFO [39]") || !strings.Contains(out, "WARN [99]") || !strings.Contains(out, "LOGS stacks-blockchain, WARN+") {
		t.Errorf("warn filter:\n%s", out)
	}
	press("l")
	if m.logSvc != "stacks-blockchain-api" || len(m.lines) != 0 {
		t.Errorf("l: %q %d", m.logSvc, len(m.lines))
	}
	press("?")
	if !strings.Contains(m.render(), "sbd reset") {
		t.Error("help lacks the CLI-only commands")
	}
	press("a")
	if m.help {
		t.Error("help didn't close")
	}
	if _, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"}); cmd == nil {
		t.Error("q didn't quit")
	}
}

func TestActionsAreCLICommands(t *testing.T) {
	// Every dashboard action is an sbd command that has a plan (refactor/13 acceptance 4)
	for key, args := range actions {
		if cmd := lookup(args[0]); cmd == nil || cmd.Plan == nil {
			t.Errorf("%s: sbd %v is not a planned command", key, args)
		}
	}
}
