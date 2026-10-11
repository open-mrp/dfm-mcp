// Package crashreports is the user's choice about crash reports and the report that choice sends. A crash is always saved on the machine; with `dfm-mcp crash-reports on`, it is also sent to the service so the maintainers hear of it. Off by default, and DO_NOT_TRACK turns it off whatever the file says.
package crashreports

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Report is one crash as the service takes it at POST /v1/dfm/crash-reports: where it happened and in what build. It never carries a request's arguments, a path or a drawing.
type Report struct {
	CrashID       string    `json:"crash_id"`
	Program       string    `json:"program"`
	Version       string    `json:"version"`
	EngineVersion string    `json:"engine_version"`
	OS            string    `json:"os"`
	Arch          string    `json:"arch"`
	Client        *Client   `json:"client,omitempty"`
	Operation     string    `json:"operation"`
	Message       string    `json:"message"`
	Stack         string    `json:"stack"`
	OccurredAt    time.Time `json:"occurred_at"`
}

// Client names the MCP client the server was working for, as it named itself when it connected.
type Client struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// The limits the service enforces on a report's free text; a report is cut to fit rather than refused.
const (
	MaxMessage = 2000
	MaxStack   = 16000
)

// Fit cuts the free text to what the service takes.
func (r Report) Fit() Report {
	r.Message = cut(r.Message, MaxMessage)
	r.Stack = cut(r.Stack, MaxStack)
	r.Operation = cut(r.Operation, 100)
	return r
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n-len("…")]
	// Do not split a UTF-8 sequence.
	for len(s) > 0 && s[len(s)-1]&0xC0 == 0x80 {
		s = s[:len(s)-1]
	}
	if len(s) > 0 && s[len(s)-1] >= 0xC0 {
		s = s[:len(s)-1]
	}
	return s + "…"
}

// Setting is the user's choice, kept in a file under the config directory.
type Setting struct {
	file     string
	disabled bool
}

// Open reads the choice from configDir. DO_NOT_TRACK in the environment turns sending off.
func Open(configDir string, getenv func(string) string) *Setting {
	s := &Setting{file: filepath.Join(configDir, "crash-reports")}
	if v := strings.TrimSpace(getenv("DO_NOT_TRACK")); v != "" && v != "0" && !strings.EqualFold(v, "false") {
		s.disabled = true
	}
	return s
}

// On reports whether crashes are sent. It reads the file each time, so a change made while the server runs applies to the next crash.
func (s *Setting) On() bool {
	if s == nil || s.disabled {
		return false
	}
	data, err := os.ReadFile(s.file)
	return err == nil && strings.TrimSpace(string(data)) == "on"
}

// Set records the choice: on or off.
func (s *Setting) Set(on bool) error {
	v := "off"
	if on {
		v = "on"
	}
	if err := os.MkdirAll(filepath.Dir(s.file), 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.file, []byte(v+"\n"), 0o600)
}

// Status says what the choice is and why, for `dfm-mcp crash-reports`.
func (s *Setting) Status() string {
	switch {
	case s.disabled:
		return "crash reports: off (DO_NOT_TRACK is set)"
	case s.On():
		return "crash reports: on; a crash is sent to the dfm-mcp maintainers with the version, where it failed, the panic and the stack"
	default:
		return "crash reports: off; a crash is kept on this machine only (dfm-mcp crash-reports on sends them)"
	}
}

// ErrDisabled is returned when sending is asked for while DO_NOT_TRACK is set.
var ErrDisabled = errors.New("DO_NOT_TRACK is set in the environment, so crash reports are not sent while it is")

// Command runs `dfm-mcp crash-reports [on|off]` and returns what to print.
func (s *Setting) Command(args []string) (string, error) {
	if len(args) == 0 {
		return s.Status(), nil
	}
	switch args[0] {
	case "on", "off":
		if err := s.Set(args[0] == "on"); err != nil {
			return "", err
		}
		if args[0] == "on" && s.disabled {
			return s.Status(), ErrDisabled
		}
		return s.Status(), nil
	default:
		return "", fmt.Errorf("crash-reports: unknown argument %q (on or off)", args[0])
	}
}
