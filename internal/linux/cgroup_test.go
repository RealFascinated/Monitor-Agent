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

func TestCgroupMemorySearchDirs(t *testing.T) {
	t.Parallel()

	got := cgroupMemorySearchDirs()
	if len(got) == 0 {
		t.Fatal("expected at least one cgroup dir")
	}
	if got[0] != cgroupV2Dir() {
		t.Fatalf("first dir = %q, want %q", got[0], cgroupV2Dir())
	}
	foundRoot := false
	for _, dir := range got {
		if dir == "/sys/fs/cgroup" {
			foundRoot = true
			break
		}
	}
	if !foundRoot {
		t.Fatalf("dirs = %#v, missing /sys/fs/cgroup", got)
	}
}

func TestCgroupMemoryUsage(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		mem  CgroupMemory
		want uint64
	}{
		{
			name: "cgroup current",
			mem: CgroupMemory{
				Max: 8e9, Current: 628408320, OK: true,
			},
			want: 628408320,
		},
		{
			name: "not ok",
			mem:  CgroupMemory{OK: false},
			want: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.mem.Usage(); got != tc.want {
				t.Fatalf("Usage() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestCgroupMemoryAvailable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		mem  CgroupMemory
		want uint64
	}{
		{
			name: "cgroup current",
			mem: CgroupMemory{
				Max: 8e9, Current: 628408320, OK: true,
			},
			want: 8e9 - 628408320,
		},
		{
			name: "not ok",
			mem:  CgroupMemory{OK: false},
			want: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.mem.Available(); got != tc.want {
				t.Fatalf("Available() = %d, want %d", got, tc.want)
			}
		})
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
