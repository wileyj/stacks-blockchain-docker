package main

import (
	"errors"
	"strings"
	"testing"
)

func TestPickMode(t *testing.T) {
	const gib = int64(1) << 30
	yes, no := func() bool { return true }, func() bool { return false }
	for _, tc := range []struct {
		mode       string
		size, free int64
		clone      func() bool
		want, err  string
	}{
		{"auto", 500 * gib, 10 * gib, yes, "clone", ""},
		{"auto", 500 * gib, 600 * gib, no, "copy", ""},
		{"auto", 500 * gib, 540 * gib, no, "", "--mode link"}, // copy needs size + 10%
		{"copy", 500 * gib, 100 * gib, yes, "", "needs about"},
		{"clone", 1, 1, no, "", "can't clone"},
		{"link", 500 * gib, 1, no, "link", ""},
	} {
		got, err := pickMode(tc.mode, tc.size, tc.free, tc.clone)
		if tc.err != "" {
			var ee exitError
			if err == nil || !strings.Contains(err.Error(), tc.err) || !errors.As(err, &ee) || ee.code != 2 {
				t.Errorf("%s %d/%d: want a refusal mentioning %q, got %v", tc.mode, tc.size, tc.free, tc.err, err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s %d/%d: got %q, %v; want %q", tc.mode, tc.size, tc.free, got, err, tc.want)
		}
	}
}

func TestParseMajorAndInitdbArgs(t *testing.T) {
	for in, want := range map[string]int{"16\n": 16, "17.6": 17, "postgres (PostgreSQL) 18.1": 18} {
		if got, err := parseMajor(in); err != nil || got != want {
			t.Errorf("parseMajor(%q) = %d, %v", in, got, err)
		}
	}
	if _, err := parseMajor("x"); err == nil {
		t.Errorf("garbage must fail")
	}
	libc := cluster{user: "postgres", encoding: "UTF8", provider: "c", collate: "en_US.utf8", ctype: "en_US.utf8"}
	if got := strings.Join(libc.initdbArgs(17), " "); got != "--username=postgres --encoding=UTF8 --lc-collate=en_US.utf8 --lc-ctype=en_US.utf8 --auth=trust" {
		t.Errorf("libc to 17: %s", got)
	}
	// 18's initdb turns checksums on by default; pg_upgrade needs the old cluster's setting
	if got := strings.Join(libc.initdbArgs(18), " "); !strings.HasSuffix(got, "--auth=trust --no-data-checksums") {
		t.Errorf("libc to 18: %s", got)
	}
	libc.checksums = true
	if got := strings.Join(libc.initdbArgs(18), " "); !strings.HasSuffix(got, "--auth=trust --data-checksums") {
		t.Errorf("checksums on: %s", got)
	}
	icu := cluster{user: "u", encoding: "UTF8", provider: "i", collate: "C", ctype: "C", locale: "und"}
	if got := strings.Join(icu.initdbArgs(17), " "); !strings.HasSuffix(got, "--locale-provider=icu --icu-locale=und") {
		t.Errorf("icu: %s", got)
	}
}
