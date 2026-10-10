package main

import (
	"strings"
	"testing"
)

func TestOperatorBuilderPilotOnlyTargetsTheTwoApprovedGitSources(t *testing.T) {
	a, err := projectFor("testA")
	if err != nil || a.source != "e705152a12b9cd06d3881c97240b53fb345267e4aa7ea9a0ed531a63a59a3b8d" || a.plan.CPUMilli != 1000 || a.plan.MemoryMiB != 2048 || !a.plan.Valid() {
		t.Fatalf("testA approved build budget/source changed: %+v %v", a.plan, err)
	}
	b, err := projectFor("testB")
	if err != nil || b.source != "4ba10abcb742a445aea1d55b622fac1e4a0fcf4b7eee6928a3789fc819bde9c3" || b.plan.CPUMilli != 2000 || b.plan.MemoryMiB != 3072 || !b.plan.Valid() {
		t.Fatalf("testB approved build budget/source changed: %+v %v", b.plan, err)
	}
	if _, err := projectFor("testC"); err == nil {
		t.Fatal("unapproved tenant accepted by same-host Builder pilot")
	}
	for _, project := range []fixedProject{a, b} {
		if !strings.Contains(project.output, "-output-bounded-20261007/_data/bundle.tar.gz") {
			t.Fatalf("pilot must never sign an archive from a legacy unbounded volume: %s", project.output)
		}
	}
}

func TestSigningRequiresTheExactBoundedOutputMount(t *testing.T) {
	path := "/data/ewp-builder-20261007/docker/volumes/ewp-builder-a-output-bounded-20261007/_data"
	line := "101 25 0:110 / " + path + " rw,nosuid,nodev,noexec,relatime - tmpfs tmpfs rw,size=65536k,mode=700,uid=1000,gid=1000\n"
	for _, tc := range []struct {
		name string
		info string
		path string
		want bool
	}{
		{"bounded", line, path, true},
		{"missing mount", "", path, false},
		{"other output volume", line, path + "-other", false},
		{"oversize", strings.Replace(line, "size=65536k", "size=131072k", 1), path, false},
		{"disk backed", strings.Replace(line, "- tmpfs tmpfs", "- ext4 /dev/vdb1", 1), path, false},
		{"executable", strings.Replace(line, "noexec,", "exec,", 1), path, false},
		{"wrong owner", strings.Replace(line, "uid=1000", "uid=0", 1), path, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := boundedOutputMount([]byte(tc.info), tc.path); got != tc.want {
				t.Fatalf("boundedOutputMount=%t, want %t", got, tc.want)
			}
		})
	}
}
