//go:build linux

package zfs

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func ReadPoolIOSnapshots() map[string]PoolIO {
	snapshots := make(map[string]PoolIO)
	matches, err := filepath.Glob("/proc/spl/kstat/zpool/*/io")
	if err != nil {
		return snapshots
	}

	for _, path := range matches {
		pool := filepath.Base(filepath.Dir(path))
		io, err := parsePoolIO(path)
		if err != nil {
			continue
		}
		snapshots[pool] = io
	}
	return snapshots
}

func parsePoolIO(path string) (PoolIO, error) {
	file, err := os.Open(path)
	if err != nil {
		return PoolIO{}, err
	}
	defer file.Close()

	var io PoolIO
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "nread":
			io.Nread = parseUint64(fields[len(fields)-1])
		case "nwritten":
			io.Nwritten = parseUint64(fields[len(fields)-1])
		}
	}
	return io, scanner.Err()
}

func parseUint64(value string) uint64 {
	n, _ := strconv.ParseUint(value, 10, 64)
	return n
}
