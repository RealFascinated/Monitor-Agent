//go:build linux

package linux

import (
	"testing"
)

func TestParseCPUSet(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want []string
	}{
		{"0-3", []string{"0", "1", "2", "3"}},
		{"0,2,4", []string{"0", "2", "4"}},
		{"3-1", []string{"1", "2", "3"}},
		{"0-1,8,10-11", []string{"0", "1", "8", "10", "11"}},
		{"", nil},
		{"  ", nil},
	}

	for _, tc := range cases {
		got := ParseCPUSet(tc.in)
		if len(tc.want) == 0 {
			if got != nil {
				t.Fatalf("ParseCPUSet(%q) = %#v, want nil", tc.in, got)
			}
			continue
		}
		if len(got) != len(tc.want) {
			t.Fatalf("ParseCPUSet(%q) len = %d, want %d (%#v)", tc.in, len(got), len(tc.want), got)
		}
		for _, id := range tc.want {
			if _, ok := got[id]; !ok {
				t.Fatalf("ParseCPUSet(%q) missing %q in %#v", tc.in, id, got)
			}
		}
	}
}

func TestFilterPerCPU(t *testing.T) {
	t.Parallel()

	stats := map[string]CPUStat{
		"0": {User: 1},
		"1": {User: 2},
		"2": {User: 3},
	}
	allowed := map[string]struct{}{"0": {}, "2": {}}

	got := FilterPerCPU(stats, allowed)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got["0"].User != 1 || got["2"].User != 3 {
		t.Fatalf("filtered = %#v", got)
	}
	if _, ok := got["1"]; ok {
		t.Fatalf("cpu1 should be filtered out")
	}
}
