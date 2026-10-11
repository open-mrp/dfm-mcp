package main

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestTelemetryCommand(t *testing.T) {
	env := map[string]string{"XDG_CONFIG_HOME": t.TempDir(), "XDG_STATE_HOME": t.TempDir(), "HOME": t.TempDir()}
	getenv := func(k string) string { return env[k] }
	run := func(args ...string) string {
		var out bytes.Buffer
		if err := Run(context.Background(), args, getenv, nil, &out, &out); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out.String()
	}
	if s := run("telemetry"); !strings.HasPrefix(s, "mode: local") {
		t.Errorf("default: %s", s)
	}
	if s := run("telemetry", "on"); !strings.Contains(s, "telemetry on") {
		t.Errorf("on: %s", s)
	}
	if s := run("telemetry"); !strings.HasPrefix(s, "mode: on") {
		t.Errorf("after on: %s", s)
	}
	env["DO_NOT_TRACK"] = "1"
	if s := run("telemetry"); !strings.Contains(s, "mode: off (DO_NOT_TRACK is set)") {
		t.Errorf("do not track: %s", s)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"bogus"}, getenv, nil, &out, &out); err == nil {
		t.Error("an unknown command must fail")
	}
}

// A mint attempt keeps its idempotency key until the secret is saved, then starts fresh, so a retry reuses it and a later mint never replays a key revoked since.
func TestMintAttemptIsKeptUntilSaved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	first, err := mintAttempt(path)
	if err != nil || len(first) != 32 {
		t.Fatalf("first attempt: %q %v", first, err)
	}
	if again, _ := mintAttempt(path); again != first {
		t.Errorf("a retry got a new attempt: %q != %q", again, first)
	}
	if err := saveKey(path, "https://dfm.example", "dfm_sk_x"); err != nil {
		t.Fatal(err)
	}
	if loadKey(path, "https://dfm.example") != "dfm_sk_x" {
		t.Error("saved key not read back")
	}
	if next, _ := mintAttempt(path); next == first || len(next) != 32 {
		t.Errorf("after saving, the next mint reused %q", next)
	}
	if loadKey(path, "https://dfm.example") != "dfm_sk_x" {
		t.Error("starting a new attempt lost the saved key")
	}
}

// Asking for help prints the usage and succeeds.
func TestHelpIsNotAnError(t *testing.T) {
	var stderr strings.Builder
	dir := t.TempDir()
	env := map[string]string{"HOME": dir, "XDG_CONFIG_HOME": dir, "XDG_STATE_HOME": dir}
	if err := Run(context.Background(), []string{"--help"}, func(k string) string { return env[k] }, strings.NewReader(""), io.Discard, &stderr); err != nil {
		t.Fatalf("--help returned %v", err)
	}
	if !strings.Contains(stderr.String(), "dfm-mcp telemetry") {
		t.Errorf("usage not printed: %q", stderr.String())
	}
}

func TestCrashReportsCommand(t *testing.T) {
	env := map[string]string{"XDG_CONFIG_HOME": t.TempDir(), "XDG_STATE_HOME": t.TempDir(), "HOME": t.TempDir()}
	getenv := func(k string) string { return env[k] }
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := Run(context.Background(), args, getenv, nil, &out, &out)
		return out.String(), err
	}
	if s, err := run("crash-reports"); err != nil || !strings.HasPrefix(s, "crash reports: off") {
		t.Errorf("default: %q %v", s, err)
	}
	if s, err := run("crash-reports", "on"); err != nil || !strings.HasPrefix(s, "crash reports: on") {
		t.Errorf("on: %q %v", s, err)
	}
	if s, _ := run("crash-reports"); !strings.HasPrefix(s, "crash reports: on") {
		t.Errorf("after on: %q", s)
	}
	env["DO_NOT_TRACK"] = "1"
	if s, _ := run("crash-reports"); !strings.Contains(s, "DO_NOT_TRACK") {
		t.Errorf("do not track: %q", s)
	}
	if _, err := run("crash-reports", "sometimes"); err == nil {
		t.Error("an unknown argument must fail")
	}
}
