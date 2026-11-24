package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const signerRunningPS = `{"Service":"stacks-blockchain","Name":"stacks-blockchain","State":"running"}
{"Service":"stacks-blockchain-api","Name":"stacks-blockchain-api","State":"running"}
{"Service":"stacks-signer","Name":"stacks-signer","State":"running"}`

func TestUpdatePlan(t *testing.T) {
	dir := project(t, sampleEnv+"STACKS_BLOCKCHAIN_VERSION=4.0.7\nSTACKS_SIGNER_VERSION=4.0.7\nSTACKS_BLOCKCHAIN_API_VERSION=9.10.0\n")
	code, out, errOut := sbd(t, dir, &fakeDocker{}, "plan", "update", "api")
	want := "# the node holds its tip while the API restarts (a database migration can take minutes), then catches up\n" +
		"docker compose pull stacks-blockchain-api\ndocker compose run --rm config\n" +
		"docker compose up -d --no-deps --force-recreate stacks-blockchain-api\n"
	if code != 0 || out != want {
		t.Fatalf("exit %d %s\ngot:\n%s\nwant:\n%s", code, errOut, out, want)
	}
	// --version pins first; the same version is no edit
	_, out, _ = sbd(t, dir, &fakeDocker{}, "plan", "update", "signer", "--version", "4.0.6")
	if !strings.HasPrefix(out, "edit .env: STACKS_SIGNER_VERSION=4.0.6\n") || !strings.Contains(out, "--force-recreate stacks-signer") {
		t.Errorf("signer --version:\n%s", out)
	}
	// The compose service name works the same as the short name
	if _, byService, _ := sbd(t, dir, &fakeDocker{}, "plan", "update", "stacks-blockchain-api"); byService != want {
		t.Errorf("by service name:\n%s", byService)
	}
	if _, out, _ = sbd(t, dir, &fakeDocker{}, "plan", "update", "--version", "9.10.0", "api"); strings.Contains(out, "edit .env") {
		t.Errorf("an unchanged pin is no edit:\n%s", out)
	}
	for _, tc := range []struct {
		args []string
		msg  string
	}{
		{[]string{"update"}, "usage: sbd update <proxy|stacks-blockchain|stacks-blockchain-api|stacks-signer>"},
		{[]string{"update", "postgres"}, "sbd db upgrade"},
		{[]string{"update", "grafana"}, "unknown service"},
		{[]string{"update", "api", "--version", "9.10;rm"}, "isn't an image tag"},
		{[]string{"update", "signer", "--version", "4.0.8"}, "update the node first"},
		{[]string{"update", "stacks-signer", "--version", "4.0.8"}, "update the node first"},
	} {
		code, _, errOut := sbd(t, dir, &fakeDocker{}, append([]string{"plan"}, tc.args...)...)
		if code != 2 || !strings.Contains(errOut, tc.msg) {
			t.Errorf("%v: exit %d, %q", tc.args, code, errOut)
		}
	}
}

func TestUpdateNeedsTheServiceRunning(t *testing.T) {
	dir := project(t, sampleEnv)
	d := &fakeDocker{ps: runningPS} // the node only
	code, _, errOut := sbd(t, dir, d, "update", "signer")
	if code != 1 || !strings.Contains(errOut, "stacks-signer isn't running") || len(d.ran) != 0 {
		t.Errorf("exit %d %q, ran %v", code, errOut, d.ran)
	}
	d = &fakeDocker{ps: signerRunningPS}
	if code, _, errOut := sbd(t, dir, d, "update", "signer"); code != 0 || len(d.ran) != 3 || d.ran[2] != "compose up -d --no-deps --force-recreate stacks-signer" {
		t.Errorf("exit %d %q, ran %v", code, errOut, d.ran)
	}
}

func TestNewer(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{{"4.0.8", "4.0.7", true}, {"4.0.7", "4.0.7", false}, {"4.0.10", "4.0.9", true}, {"4.0.6", "4.0.7", false}, {"4.1", "4.0.9", true}, {"v4.0.8", "4.0.7", true}} {
		if got := newer(tc.a, tc.b); got != tc.want {
			t.Errorf("newer(%s, %s) = %v", tc.a, tc.b, got)
		}
	}
}

// sbd update --all takes sample.env's versions (owner, 2026-11-23)
func TestUpdateAll(t *testing.T) {
	sample := sampleEnv + "STACKS_BLOCKCHAIN_VERSION=4.0.7\nSTACKS_SIGNER_VERSION=4.0.7\nSTACKS_BLOCKCHAIN_API_VERSION=9.10.0\nPOSTGRES_VERSION=18\n# CADDY_VERSION=2.11.9-alpine\n"
	dir := project(t, "NETWORK=mainnet\nCOMPOSE_PROFILES=node,api\nSTACKS_BLOCKCHAIN_VERSION=4.0.1\nSTACKS_SIGNER_VERSION=4.0.1\n"+
		"STACKS_BLOCKCHAIN_API_VERSION=9.11.0\nPOSTGRES_VERSION=17\n")
	os.WriteFile(filepath.Join(dir, "sample.env"), []byte(sample), 0o644)
	code, out, errOut := sbd(t, dir, &fakeDocker{}, "plan", "update", "--all")
	want := "# STACKS_BLOCKCHAIN_VERSION 4.0.1 -> 4.0.7\nedit .env: STACKS_BLOCKCHAIN_VERSION=4.0.7\n" +
		"# STACKS_SIGNER_VERSION 4.0.1 -> 4.0.7\nedit .env: STACKS_SIGNER_VERSION=4.0.7\n" +
		"# keeping STACKS_BLOCKCHAIN_API_VERSION=9.11.0, newer than sample.env's 9.10.0\n" +
		"# the node stops (up to STACKS_SHUTDOWN_TIMEOUT); the node and the API migrate their data on start\n" +
		"docker compose pull --ignore-buildable\n"
	if code != 0 || !strings.HasPrefix(out, want) || !strings.HasSuffix(out, "docker compose up -d\n") || strings.Contains(out, "POSTGRES") {
		t.Fatalf("exit %d %s\n%s", code, errOut, out)
	}
	if code, _, errOut := sbd(t, dir, &fakeDocker{}, "update", "--all"); code != 0 {
		t.Fatalf("exit %d %s", code, errOut)
	}
	if _, out, _ := sbd(t, dir, &fakeDocker{}, "plan", "update", "--all"); !strings.Contains(out, "nothing to update") || strings.Contains(out, "docker") {
		t.Errorf("second run:\n%s", out)
	}
	if code, _, errOut := sbd(t, dir, &fakeDocker{}, "plan", "update", "--all", "api"); code != 2 || !strings.Contains(errOut, "usage: sbd update --all") {
		t.Errorf("--all with a service: exit %d %q", code, errOut)
	}
}
