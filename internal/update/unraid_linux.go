//go:build linux

package update

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	unraidStoredBin   = "/boot/config/plugins/monitor-agent/monitor-agent"
	unraidStartScript = "/usr/local/bin/monitor-agent-start.sh"
	unraidPIDFile     = "/var/run/monitor-agent.pid"
)

func isUnraid() bool {
	_, err := os.Stat("/etc/unraid-version")
	return err == nil
}

func syncUnraidStoredBinary() error {
	if !isUnraid() {
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(unraidStoredBin), 0o755); err != nil {
		return err
	}
	if err := copyFile(exe, unraidStoredBin); err != nil {
		return fmt.Errorf("sync binary to flash: %w", err)
	}

	slog.Info("synced binary to flash", "path", unraidStoredBin)
	return nil
}

func stopUnraidAgent() {
	data, err := os.ReadFile(unraidPIDFile)
	if err != nil {
		return
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return
	}

	_ = syscall.Kill(pid, syscall.SIGTERM)
}

func restartUnraidAgent() error {
	if err := syncUnraidStoredBinary(); err != nil {
		return err
	}

	stopUnraidAgent()
	time.Sleep(500 * time.Millisecond)

	if _, err := os.Stat(unraidStartScript); err != nil {
		return fmt.Errorf("start script not found: %w", err)
	}

	slog.Info("restarting agent", "script", unraidStartScript)
	return exec.Command(unraidStartScript).Run()
}
