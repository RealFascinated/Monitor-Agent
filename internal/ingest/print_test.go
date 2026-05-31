package ingest

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestPrint(t *testing.T) {
	var buf bytes.Buffer
	data := Data{
		AgentVersion: "2.0.0",
		ServerDetails: ServerDetails{
			Ip: "127.0.0.1",
		},
	}
	if err := printTo(&buf, data); err != nil {
		t.Fatalf("printTo: %v", err)
	}

	var decoded Data
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if decoded.AgentVersion != "2.0.0" {
		t.Fatalf("agentVersion: got %q", decoded.AgentVersion)
	}
	if decoded.ServerDetails.Ip != "127.0.0.1" {
		t.Fatalf("ip: got %q", decoded.ServerDetails.Ip)
	}
}

func TestLoadPrintConfigNoToken(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	unsetConfigEnv(t)
	os.Unsetenv(configFileEnvVar)

	cfg, err := LoadPrintConfig()
	if err != nil {
		t.Fatalf("LoadPrintConfig: %v", err)
	}
	if !cfg.PrintMode {
		t.Fatal("expected print mode")
	}
}

func TestLoadConfigPrintModeEnv(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	unsetConfigEnv(t)
	os.Unsetenv(configFileEnvVar)
	t.Setenv(ConfigEnvVar("print_mode"), "true")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.PrintMode {
		t.Fatal("expected print mode from env")
	}
}
