package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// Storage estimates per component, from this repo's seeds (refactor/15 "Phase 3c" and "Phase 3a"):
// mainnet 2026-10-28, chainstate about 620 GB and the API database about 510 GB with all indexes;
// testnet snapshots are 6.4 + 2.7 GiB compressed. They grow with the chain: update them with the
// requirements in docs/requirements.md.
var storageGB = map[string]map[string]uint64{
	"mainnet": {"node": 620, "api": 510},
	"testnet": {"node": 25, "api": 25},
}

// growthGB is the headroom wanted where a component's data already exists.
var growthGB = map[string]uint64{"mainnet": 50, "testnet": 5}

const gb = 1 << 30

// dataDir returns where a component keeps its data, resolved as compose.yaml does.
func dataDir(c *Ctx, component string) string {
	root := c.Value("DATA_ROOT")
	if root == "" {
		root = "./persistent-data"
	}
	key, sub := "STACKS_DATA_DIR", "stacks-blockchain"
	if component == "api" {
		key, sub = "POSTGRES_DATA_DIR", "postgres"
	}
	dir := c.Value(key)
	if dir == "" {
		dir = filepath.Join(root, c.Value("NETWORK"), sub)
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(c.Dir, dir)
	}
	return filepath.Clean(dir)
}

// existingAncestor returns path or its nearest existing parent (the filesystem it will be on).
func existingAncestor(path string) string {
	for {
		if _, err := os.Stat(path); err == nil {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return path
		}
		path = parent
	}
}

func hasData(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

// diskChecks compares the free space under each enabled component's data dir with what it needs:
// the full estimate (plus 20%) for a component without data yet, growth headroom otherwise. Needs
// on the same filesystem add up. Warnings only: nothing blocks (refactor/11 Q12).
func diskChecks(c *Ctx, profiles []string, remote bool) []Check {
	net := c.Value("NETWORK")
	if remote {
		return []Check{{"warn", "disk space", "not checked: the data is on the remote Docker host"}}
	}
	type fsNeed struct {
		free  uint64
		need  uint64
		parts []string
	}
	byFS := map[string]*fsNeed{}
	var order []string
	for _, comp := range []string{"node", "api"} {
		if !slices.Contains(profiles, comp) {
			continue
		}
		dir := dataDir(c, comp)
		free, fsID, err := freeSpace(existingAncestor(dir))
		if err != nil {
			return []Check{{"warn", "disk space", fmt.Sprintf("can't read the free space for %s: %v", dir, err)}}
		}
		need := storageGB[net][comp] * gb * 6 / 5
		what := fmt.Sprintf("%s %s (about %d GB)", comp, dir, storageGB[net][comp])
		if hasData(dir) {
			need = growthGB[net] * gb
			what = fmt.Sprintf("%s %s (has data; %d GB to grow)", comp, dir, growthGB[net])
		}
		if byFS[fsID] == nil {
			byFS[fsID] = &fsNeed{free: free}
			order = append(order, fsID)
		}
		byFS[fsID].need += need
		byFS[fsID].parts = append(byFS[fsID].parts, what)
	}
	var checks []Check
	for _, id := range order {
		f := byFS[id]
		detail := fmt.Sprintf("%d GB free, %d GB wanted: %s", f.free/gb, f.need/gb, strings.Join(f.parts, "; "))
		level := "ok"
		if f.free < f.need {
			level = "warn"
			detail = "low: " + detail + ". Point DATA_ROOT or the per-component *_DATA_DIR at a larger disk (docs/requirements.md)"
		}
		checks = append(checks, Check{level, "disk space", detail})
	}
	return checks
}

// DockerInfo is the part of `docker info` the host checks use.
type DockerInfo struct {
	ServerVersion   string `json:"ServerVersion"`
	OperatingSystem string `json:"OperatingSystem"`
	Architecture    string `json:"Architecture"`
	MemTotal        uint64 `json:"MemTotal"`
	NCPU            int    `json:"NCPU"`
}

func dockerInfo(d Docker) (DockerInfo, error) {
	var info DockerInfo
	out, err := d.Output([]string{"info", "--format", "{{json .}}"})
	if err != nil {
		return info, err
	}
	return info, json.Unmarshal(out, &info)
}

// hostChecks replace manage.sh's arm64 prompt with advice that applies to every OS (refactor/14 §5):
// memory and CPUs available to Docker, and Docker Desktop's slow bind mounts for chain data.
func hostChecks(c *Ctx, info DockerInfo, profiles []string) []Check {
	var checks []Check
	minMem := uint64(4)
	if c.Value("NETWORK") == "mainnet" && slices.Contains(profiles, "api") {
		minMem = 8
	}
	mem := fmt.Sprintf("%.1f GB memory, %d CPUs available to Docker (%s, %s)", float64(info.MemTotal)/gb, info.NCPU, info.OperatingSystem, info.Architecture)
	switch {
	case info.MemTotal > 0 && info.MemTotal < minMem*gb:
		checks = append(checks, Check{"warn", "resources", mem + fmt.Sprintf(": %d GB or more is recommended", minMem)})
	case info.NCPU > 0 && info.NCPU < 2:
		checks = append(checks, Check{"warn", "resources", mem + ": 2 or more CPUs are recommended"})
	default:
		checks = append(checks, Check{"ok", "resources", mem})
	}
	if strings.Contains(info.OperatingSystem, "Docker Desktop") {
		checks = append(checks, Check{"warn", "storage", "Docker Desktop: bind-mounted chain data is much slower than on Linux, so a mainnet sync takes far longer. Fine for testnet and trying things out; use a Linux host for mainnet (docs/requirements.md)"})
	}
	if runtime.GOOS == "windows" && !strings.HasPrefix(dataDir(c, "node"), `\\wsl`) {
		checks = append(checks, Check{"warn", "storage", "data on a Windows drive is slow through Docker Desktop: keep the checkout (or DATA_ROOT) inside the WSL filesystem"})
	}
	return checks
}
