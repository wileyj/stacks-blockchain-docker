package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Data for `sbd ui` (refactor/13 T6–T8): the docker CLI for containers, stats and logs, and plain HTTP
// to the node and API on this host. Nothing here changes anything.

// Sync rules (owner, 2026-11-09). Nakamoto blocks come every few seconds, so a node at the tip always
// has a recent latest block; a public tip is a moving target, so being a few blocks short is synced.
const (
	syncedAge    = 5 * time.Minute
	burnStaleAge = 2 * time.Hour // the StacksBurnchainStalled alert's threshold
	refTolerance = 15
	refKey       = "SBD_UI_REFERENCE_API"
)

type publisher struct {
	URL           string `json:"URL"`
	TargetPort    int    `json:"TargetPort"`
	PublishedPort int    `json:"PublishedPort"`
}

// psRow is the part of `docker compose ps --format json` the dashboard uses.
type psRow struct {
	Service    string      `json:"Service"`
	Name       string      `json:"Name"`
	State      string      `json:"State"`
	Health     string      `json:"Health"`
	ExitCode   int         `json:"ExitCode"`
	Publishers []publisher `json:"Publishers"`
}

type inspectRow struct {
	Name         string `json:"Name"`
	RestartCount int    `json:"RestartCount"`
	State        struct {
		StartedAt time.Time `json:"StartedAt"`
	} `json:"State"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

// uiService is one container of the project.
type uiService struct {
	psRow
	Started  time.Time
	Restarts int
	IP       string
}

// collectServices lists the project's containers (every profile, stopped ones too) with restart
// counts, start times and addresses from `docker inspect`.
func collectServices(d Docker) ([]uiService, error) {
	out, err := d.Output(psArgs)
	if err != nil {
		return nil, err
	}
	rows, err := parseRows[psRow](out)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	args := []string{"inspect"}
	for _, r := range rows {
		args = append(args, r.Name)
	}
	byName := map[string]inspectRow{}
	// A container removed between the two calls fails inspect; the ps rows still show
	if out, err := d.Output(args); err == nil {
		var ins []inspectRow
		if json.Unmarshal(out, &ins) == nil {
			for _, i := range ins {
				byName[strings.TrimPrefix(i.Name, "/")] = i
			}
		}
	}
	svcs := make([]uiService, 0, len(rows))
	for _, r := range rows {
		s := uiService{psRow: r}
		if i, ok := byName[r.Name]; ok {
			s.Started, s.Restarts = i.State.StartedAt, i.RestartCount
			for _, n := range i.NetworkSettings.Networks {
				if n.IPAddress != "" {
					s.IP = n.IPAddress
					break
				}
			}
		}
		svcs = append(svcs, s)
	}
	return svcs, nil
}

// endpoint is the URL this host reaches a service's port on: the published port (API_BIND_ADDR and
// RPC_BIND_ADDR default to 127.0.0.1), else, on Linux, the container's address on the compose network.
func endpoint(svcs []uiService, service string, port int) string {
	for _, s := range svcs {
		if s.Service != service || s.State != "running" {
			continue
		}
		for _, p := range s.Publishers {
			if p.TargetPort == port && p.PublishedPort > 0 {
				host := p.URL
				if host == "" || host == "0.0.0.0" || host == "::" {
					host = "127.0.0.1"
				}
				return "http://" + net.JoinHostPort(host, strconv.Itoa(p.PublishedPort))
			}
		}
		if s.IP != "" && runtime.GOOS == "linux" {
			return "http://" + net.JoinHostPort(s.IP, strconv.Itoa(port))
		}
	}
	return ""
}

// statsRow is one line of `docker stats --format json`.
type statsRow struct {
	Name     string `json:"Name"`
	CPUPerc  string `json:"CPUPerc"`
	MemUsage string `json:"MemUsage"`
	ID       string `json:"ID"` // short
}

// parseStatsLine reads one stats line; the stream wraps each in cursor movement and line clearing.
func parseStatsLine(line string) (statsRow, bool) {
	i, j := strings.IndexByte(line, '{'), strings.LastIndexByte(line, '}')
	var r statsRow
	if i < 0 || j < i || json.Unmarshal([]byte(line[i:j+1]), &r) != nil || r.Name == "" {
		return r, false
	}
	return r, true
}

var sizeRe = regexp.MustCompile(`^([0-9.]+)\s*([kKMGTP]?i?B)$`)

// parseSize reads docker's sizes: decimal (kB, MB, GB) and binary (KiB, MiB, GiB) units.
func parseSize(s string) (uint64, bool) {
	m := sizeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	unit := strings.ToUpper(m[2])
	base := 1000.0
	if strings.Contains(unit, "I") {
		base = 1024
	}
	exp := strings.Index("BKMGTP", unit[:1])
	return uint64(v * math.Pow(base, float64(exp))), true
}

// ioCount is a container's cumulative disk IO in bytes.
type ioCount struct {
	read, written uint64
	ok            bool
}

// cgroupIO reads a container's exact disk IO from its cgroup (Linux, cgroup v2; docker stats rounds
// to three digits, too coarse for a rate). dirs caches the cgroup dir by short ID.
func cgroupIO(id string, dirs map[string]string) ioCount {
	if runtime.GOOS != "linux" || id == "" {
		return ioCount{}
	}
	dir, ok := dirs[id]
	if !ok {
		for _, pat := range []string{"/sys/fs/cgroup/system.slice/docker-" + id + "*.scope", "/sys/fs/cgroup/docker/" + id + "*"} {
			if m, _ := filepath.Glob(pat); len(m) == 1 {
				dir = m[0]
				break
			}
		}
		dirs[id] = dir
	}
	if dir == "" {
		return ioCount{}
	}
	b, err := os.ReadFile(filepath.Join(dir, "io.stat"))
	if err != nil {
		return ioCount{}
	}
	return parseIOStat(string(b))
}

// parseIOStat reads cgroup v2 io.stat. The busiest device counts: summing would count IO through a
// device-mapper (LVM, LUKS) device twice, once on it and once on the disk below.
func parseIOStat(s string) ioCount {
	var c ioCount
	for _, line := range strings.Split(s, "\n") {
		for _, f := range strings.Fields(line) {
			k, v, ok := strings.Cut(f, "=")
			if !ok {
				continue
			}
			n, _ := strconv.ParseUint(v, 10, 64)
			switch k {
			case "rbytes":
				c.read, c.ok = max(c.read, n), true
			case "wbytes":
				c.written, c.ok = max(c.written, n), true
			}
		}
	}
	return c
}

// resUse holds the latest resource use of a container; disk IO rates are the change between two
// samples.
type resUse struct {
	CPU                 float64
	Mem                 uint64
	HasIO               bool
	ReadRate, WriteRate float64
	io                  ioCount
	at                  time.Time
}

func (u *resUse) update(r statsRow, io ioCount, now time.Time) {
	u.CPU, _ = strconv.ParseFloat(strings.TrimSuffix(r.CPUPerc, "%"), 64)
	mem, _, _ := strings.Cut(r.MemUsage, "/")
	u.Mem, _ = parseSize(mem)
	u.HasIO = io.ok
	if !io.ok {
		return
	}
	if dt := now.Sub(u.at).Seconds(); u.io.ok && dt > 0 && io.read >= u.io.read && io.written >= u.io.written {
		u.ReadRate, u.WriteRate = float64(io.read-u.io.read)/dt, float64(io.written-u.io.written)/dt
	}
	u.io, u.at = io, now
}

// chainInfo is one poll of the node, the API and, if set, the reference API.
type chainInfo struct {
	NodeErr, APIErr   string
	Tip, Burn         int64
	PeersIn, PeersOut int
	APITip            int64
	BlockTime         time.Time // latest Stacks block the API has
	BurnTime          time.Time // latest Bitcoin block the API has
	RefSet            bool
	RefTip            int64
	RefErr            string
}

// getJSON fetches url into v. Its errors are short enough for the chain panel: "connection refused",
// "timeout", "503 Service Unavailable".
func getJSON(client *http.Client, url string, v any) error {
	resp, err := client.Get(url)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return errors.New("timeout")
		}
		msg := err.Error()
		return errors.New(msg[strings.LastIndex(msg, ": ")+2:])
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v)
}

// fetchChain polls the node, the API and the reference in parallel. An empty URL means the service
// isn't reachable from here (not running, or its port not published).
func fetchChain(client *http.Client, node, api, ref string) chainInfo {
	var ci chainInfo
	var wg sync.WaitGroup
	var mu sync.Mutex
	set := func(f func()) { mu.Lock(); f(); mu.Unlock() }
	wg.Go(func() {
		if node == "" {
			set(func() { ci.NodeErr = "not reachable" })
			return
		}
		var info struct {
			Tip  int64 `json:"stacks_tip_height"`
			Burn int64 `json:"burn_block_height"`
		}
		var nb struct {
			Inbound, Outbound []json.RawMessage
		}
		err := getJSON(client, node+"/v2/info", &info)
		if err == nil {
			err = getJSON(client, node+"/v2/neighbors", &nb)
		}
		set(func() {
			ci.Tip, ci.Burn, ci.PeersIn, ci.PeersOut = info.Tip, info.Burn, len(nb.Inbound), len(nb.Outbound)
			if err != nil {
				ci.NodeErr = err.Error()
			}
		})
	})
	wg.Go(func() {
		if api == "" {
			set(func() { ci.APIErr = "not reachable" })
			return
		}
		var blocks struct {
			Results []struct {
				Height    int64 `json:"height"`
				BlockTime int64 `json:"block_time"`
			} `json:"results"`
		}
		var burns struct {
			Results []struct {
				Time int64 `json:"burn_block_time"`
			} `json:"results"`
		}
		err := getJSON(client, api+"/extended/v2/blocks?limit=1", &blocks)
		if err == nil {
			err = getJSON(client, api+"/extended/v2/burn-blocks?limit=1", &burns)
		}
		set(func() {
			if err != nil {
				ci.APIErr = err.Error()
			}
			if len(blocks.Results) > 0 {
				ci.APITip, ci.BlockTime = blocks.Results[0].Height, time.Unix(blocks.Results[0].BlockTime, 0)
			}
			if len(burns.Results) > 0 {
				ci.BurnTime = time.Unix(burns.Results[0].Time, 0)
			}
		})
	})
	if ref != "" {
		ci.RefSet = true
		wg.Go(func() {
			var info struct {
				Tip int64 `json:"stacks_tip_height"`
			}
			err := getJSON(client, strings.TrimRight(ref, "/")+"/v2/info", &info)
			set(func() {
				ci.RefTip = info.Tip
				if err != nil {
					ci.RefErr = err.Error()
				}
			})
		})
	}
	wg.Wait()
	return ci
}

// Levels for colouring.
const (
	lvOK = iota
	lvWarn
	lvBad
)

// syncStatus says whether the node is at the chain tip: by the reference API's tip when one is set
// and answers, else by the age of the latest block.
func syncStatus(ci chainInfo, now time.Time) (string, int) {
	if ci.RefSet && ci.RefErr == "" && ci.RefTip > 0 && ci.Tip > 0 {
		behind := ci.RefTip - ci.Tip
		if behind <= refTolerance {
			return fmt.Sprintf("synced ✔ (reference %s)", commas(ci.RefTip)), lvOK
		}
		return fmt.Sprintf("%s blocks behind reference", commas(behind)), lvWarn
	}
	note := ""
	if ci.RefSet {
		note = " (no reference)"
	}
	if ci.BlockTime.IsZero() {
		return "unknown: no block from the API" + note, lvWarn
	}
	age := max(now.Sub(ci.BlockTime), 0)
	if age < syncedAge {
		return "synced ✔ latest block " + shortDur(age) + " ago" + note, lvOK
	}
	return "behind: latest block " + shortDur(age) + " old" + note, lvWarn
}

// hostInfo is this host's free space under the data dirs, load and memory (Linux; empty elsewhere).
type hostInfo struct {
	Disks              []diskFree
	Load               string
	MemTotal, MemAvail uint64
}

type diskFree struct {
	Label string
	Free  uint64
}

func collectHost(c *Ctx) hostInfo {
	var h hostInfo
	seen := map[string]int{}
	for _, comp := range []struct{ key, label string }{{"node", "chainstate"}, {"api", "postgres"}} {
		free, fsID, err := freeSpace(existingAncestor(dataDir(c, comp.key)))
		if err != nil {
			continue
		}
		if i, ok := seen[fsID]; ok {
			h.Disks[i].Label = "data"
			continue
		}
		seen[fsID] = len(h.Disks)
		h.Disks = append(h.Disks, diskFree{comp.label, free})
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		h.Load = parseLoadavg(string(b))
	}
	if f, err := os.Open("/proc/meminfo"); err == nil {
		h.MemTotal, h.MemAvail = parseMeminfo(f)
		f.Close()
	}
	return h
}

func parseLoadavg(s string) string {
	f := strings.Fields(s)
	if len(f) < 3 {
		return ""
	}
	return strings.Join(f[:3], " ")
}

func parseMeminfo(r io.Reader) (total, avail uint64) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		kb, _ := strconv.ParseUint(f[1], 10, 64)
		switch f[0] {
		case "MemTotal:":
			total = kb * 1024
		case "MemAvailable:":
			avail = kb * 1024
		}
	}
	return total, avail
}

// warnRe matches warning and error lines of the node (WARN/ERRO), postgres (WARNING:/ERROR:/FATAL:)
// and the API (pino JSON levels 40, 50, 60).
var warnRe = regexp.MustCompile(`(?i)\b(WARN|WARNING|ERRO|ERROR|FATAL|CRIT|PANIC)\b|"level":\s*"?(warn|error|fatal|40|50|60)\b`)

// ring keeps the last len(v) samples.
type ring struct {
	v    []float64
	next int
	full bool
}

func newRing(n int) *ring { return &ring{v: make([]float64, n)} }

func (r *ring) add(x float64) {
	r.v[r.next] = x
	r.next = (r.next + 1) % len(r.v)
	r.full = r.full || r.next == 0
}

// values returns the samples, oldest first.
func (r *ring) values() []float64 {
	if !r.full {
		return slices.Clone(r.v[:r.next])
	}
	return append(slices.Clone(r.v[r.next:]), r.v[:r.next]...)
}

var bars = []rune("▁▂▃▄▅▆▇█")

// sparkline draws the last width values, scaled between their minimum and maximum.
func sparkline(vals []float64, width int) string {
	if len(vals) > width {
		vals = vals[len(vals)-width:]
	}
	if len(vals) == 0 {
		return ""
	}
	lo, hi := slices.Min(vals), slices.Max(vals)
	var b strings.Builder
	for _, v := range vals {
		i := 0
		if hi > lo {
			i = int((v - lo) / (hi - lo) * float64(len(bars)-1))
		}
		b.WriteRune(bars[i])
	}
	return b.String()
}

func commas(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		s = "-" + s
	}
	return s
}

// shortDur prints the two largest units: 12s, 4m10s, 3h12m, 2d4h.
func shortDur(d time.Duration) string {
	s := int64(d.Seconds())
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	case s < 86400:
		return fmt.Sprintf("%dh%02dm", s/3600, s%3600/60)
	}
	return fmt.Sprintf("%dd%dh", s/86400, s%86400/3600)
}

// bytesIEC prints a size in binary units: 512 B, 3.1 GiB, 1.2 TiB.
func bytesIEC(n uint64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	v, i := float64(n), -1
	for v >= 1024 && i < 4 {
		v /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %ciB", v, "KMGTP"[i])
}

// rate prints bytes per second compactly (2.4M, 812k, 0).
func rate(b float64) string {
	switch {
	case b >= 1e9:
		return fmt.Sprintf("%.1fG", b/1e9)
	case b >= 1e6:
		return fmt.Sprintf("%.1fM", b/1e6)
	case b >= 1e3:
		return fmt.Sprintf("%.0fk", b/1e3)
	}
	return fmt.Sprintf("%.0f", b)
}
