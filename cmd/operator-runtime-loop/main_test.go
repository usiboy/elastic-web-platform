package main

import "testing"

func TestOperatorRuntimePilotRestrictsActionsAndVersions(t *testing.T) {
	valid := "dep_11111111111111111111111111111111"
	for _, tc := range []struct {
		action     string
		expected   int64
		deployment string
		want       bool
	}{
		{"register", -1, "", true}, {"baseline", -1, "", true},
		{"register-v3", -1, "", true},
		{"register-xianlixiu", -1, "", true},
		{"promote", 1, valid, true}, {"rollback", 2, baseline, true},
		{"promote", 3, valid, true}, {"promote", 4, valid, true}, {"rollback", 4, baseline, true}, {"rollback", 5, baseline, true},
		{"promote", 0, valid, false}, {"promote", 1, "dep_invalid", false},
		{"rollback", 2, valid, false}, {"register", -1, valid, false},
	} {
		if got := validateAction(tc.action, tc.expected, tc.deployment) == nil; got != tc.want {
			t.Fatalf("action=%s version=%d id=%s accepted=%t", tc.action, tc.expected, tc.deployment, got)
		}
	}
}
