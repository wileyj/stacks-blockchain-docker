package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Pins are the versions pinned in .env.
type Pins struct {
	Core, API string
	PG        int
}

// Snapshot is one chainstate archive + API dump pair taken on the same date.
type Snapshot struct {
	Date, Core, API string
	PG              int
}

func (s Snapshot) ChainFile(network string) string {
	return fmt.Sprintf("%s-stacks-blockchain-%s-%s.tar.zst", network, s.Core, s.Date)
}

func (s Snapshot) DumpFile() string {
	return fmt.Sprintf("stacks-blockchain-api-pg-%d-%s-%s.dump", s.PG, s.API, s.Date)
}

func (s Snapshot) String() string {
	return fmt.Sprintf("%s: core %s, API %s, pg%d", s.Date, s.Core, s.API, s.PG)
}

// Selection is the outcome of the Q24 pair rule (refactor/11 Q24).
type Selection struct {
	Chosen   *Snapshot // newest same-date pair compatible with the pins
	Latest   *Snapshot // newest same-date pair at any version, for messages
	Warnings []string
}

var (
	chainRe = regexp.MustCompile(`([a-z]+)-stacks-blockchain-([0-9][0-9.]*)-([0-9]{8})\.tar\.zst`)
	dumpRe  = regexp.MustCompile(`stacks-blockchain-api-pg-([0-9]+)-([0-9][0-9.]*)-([0-9]{8})\.dump`)
)

// Select applies the Q24 rule to the archive listings: the newest date on which both a chainstate
// archive and an API dump exist with compatible versions (same major as the pin and not newer;
// dump postgres major not newer than POSTGRES_VERSION). Dates are never mixed, `-latest` is never
// used. An error explains why nothing qualifies and names the latest snapshot.
func Select(network string, chainListing, dumpListing string, pins Pins) (Selection, error) {
	chains := map[string][]string{}   // date -> core versions
	dumps := map[string][][2]string{} // date -> (pg, api)
	for _, m := range chainRe.FindAllStringSubmatch(chainListing, -1) {
		if m[1] == network {
			chains[m[3]] = appendUnique(chains[m[3]], m[2])
		}
	}
	for _, m := range dumpRe.FindAllStringSubmatch(dumpListing, -1) {
		dumps[m[3]] = append(dumps[m[3]], [2]string{m[1], m[2]})
	}
	if len(chains) == 0 || len(dumps) == 0 {
		return Selection{}, fmt.Errorf("no %s snapshots found in the archive listing", network)
	}
	dates := make([]string, 0, len(chains))
	for d := range chains {
		dates = append(dates, d)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))

	var sel Selection
	for _, d := range dates {
		var best, any *Snapshot
		for _, core := range chains[d] {
			for _, dp := range dumps[d] {
				pg, _ := strconv.Atoi(dp[0])
				s := Snapshot{Date: d, Core: core, API: dp[1], PG: pg}
				if any == nil || better(s, *any) {
					c := s
					any = &c
				}
				if compatible(core, pins.Core) && compatible(dp[1], pins.API) && pg <= pins.PG && (best == nil || better(s, *best)) {
					c := s
					best = &c
				}
			}
		}
		if sel.Latest == nil && any != nil {
			sel.Latest = any
		}
		if best != nil {
			sel.Chosen = best
			break
		}
	}
	if sel.Chosen == nil {
		return sel, fmt.Errorf("no compatible %s snapshot for core %s / API %s / pg%d (%s)",
			network, pins.Core, pins.API, pins.PG, noPairReason(sel.Latest, pins))
	}
	c, l := *sel.Chosen, sel.Latest
	if l != nil && (l.Date != c.Date || l.Core != c.Core || l.API != c.API) {
		sel.Warnings = append(sel.Warnings, fmt.Sprintf(
			"a newer snapshot exists (%s) but doesn't match the versions pinned in .env, so it is not used; "+
				"to use it, update STACKS_BLOCKCHAIN_VERSION / STACKS_BLOCKCHAIN_API_VERSION / POSTGRES_VERSION", l))
	}
	if c.Core != pins.Core || c.API != pins.API {
		sel.Warnings = append(sel.Warnings, fmt.Sprintf(
			"the snapshot is older than the pinned versions (core %s, API %s); the node and API migrate the data on first start",
			pins.Core, pins.API))
	}
	return sel, nil
}

func noPairReason(latest *Snapshot, pins Pins) string {
	switch {
	case latest == nil:
		return "no date has both files"
	case major(latest.Core) != major(pins.Core):
		return fmt.Sprintf("the archive is on core %s.x", major(latest.Core))
	case major(latest.API) != major(pins.API):
		return fmt.Sprintf("the archive is on API %s.x", major(latest.API))
	case latest.PG > pins.PG:
		return fmt.Sprintf("the dumps need postgres %d, POSTGRES_VERSION is %d", latest.PG, pins.PG)
	case !versionLE(latest.Core, pins.Core):
		return "the snapshots are newer than core " + pins.Core
	case !versionLE(latest.API, pins.API):
		return "the snapshots are newer than API " + pins.API
	}
	return "no date in the archive window has both files at compatible versions"
}

// better orders same-date candidates: higher core, then higher API, then higher pg.
func better(a, b Snapshot) bool {
	if a.Core != b.Core {
		return !versionLE(a.Core, b.Core)
	}
	if a.API != b.API {
		return !versionLE(a.API, b.API)
	}
	return a.PG > b.PG
}

func major(v string) string { return strings.SplitN(v, ".", 2)[0] }

// compatible: same major as the pin and not newer (older is fine: the node/API migrate on start)
func compatible(archive, pin string) bool {
	return major(archive) == major(pin) && versionLE(archive, pin)
}

// versionLE compares dotted numeric versions (4.0.4 <= 4.0.10; 3.3.0.0.1 <= 3.3.0.0.2).
func versionLE(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			return x < y
		}
	}
	return true
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}
