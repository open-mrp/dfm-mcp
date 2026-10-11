package mcpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/open-mrp/dfm"

	"github.com/open-mrp/dfm-mcp/internal/crashreports"
	"github.com/open-mrp/dfm-mcp/telemetry"
)

// issuesURL is where a user files a crash. The report is prefilled from what crashed and the user reads it before sending, so nothing leaves the machine unless they choose to.
const issuesURL = "https://github.com/open-mrp/dfm-mcp/issues/new"

// keptCrashReports bounds the crash reports kept on the machine; the oldest go first.
const keptCrashReports = 20

// sendTimeout bounds how long a crash waits for its report to be sent, so a crashed request still answers promptly when the service is slow or unreachable.
const sendTimeout = 10 * time.Second

// Crashes handles a recovered panic, in a request or in a background task, so one fault cannot take the server down with it. Each crash is counted, its report saved on the machine and its stack logged; with the user's consent (crash-reports on) the report is also sent to the service so the maintainers hear of it. Every field is optional: the zero value only recovers.
type Crashes struct {
	// Logger (optional; default: discards) gets the stack of each crash.
	Logger *slog.Logger
	// Dir (optional; default: none) keeps a report of each crash for the user to read or file.
	Dir string
	// Counters (optional; default: the server's) counts crashes.
	Counters *telemetry.Counters
	// Setting (optional; default: nil, never send) is the user's choice about sending crash reports.
	Setting *crashreports.Setting
	// API (optional; default: nil, never send) takes the reports the user agreed to send.
	API *API
}

// Guard runs fn, recovering from a panic in it. It is for background tasks: what fn was doing stops, the crash is handled like any other, and the server keeps serving.
func (h *Crashes) Guard(operation string, fn func()) {
	defer func() {
		if v := recover(); v != nil {
			c := h.handle(context.Background(), newCrash(operation, nil, v))
			h.logger().Error("a background task stopped after a crash", "operation", operation, "crash", c.id)
		}
	}()
	fn()
}

// middleware fails the one request that panicked. A failed tool call says what happened and how the crash was or can be reported; any other request gets an error.
func (h *Crashes) middleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (result mcp.Result, err error) {
			defer func() {
				v := recover()
				if v == nil {
					return
				}
				operation := method
				if r, ok := req.(*mcp.CallToolRequest); ok && r.Params != nil && r.Params.Name != "" {
					operation = r.Params.Name
				}
				c := h.handle(ctx, newCrash(operation, clientOf(req), v))
				if method == "tools/call" {
					result, err = &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: c.message()}}}, nil
					return
				}
				result, err = nil, fmt.Errorf("dfm-mcp crashed handling %s (crash %s)", method, c.id)
			}()
			return next(ctx, method, req)
		}
	}
}

func clientOf(req mcp.Request) *crashreports.Client {
	ss, ok := req.GetSession().(*mcp.ServerSession)
	if !ok || ss == nil {
		return nil
	}
	if p := ss.InitializeParams(); p != nil && p.ClientInfo != nil {
		return &crashreports.Client{Name: p.ClientInfo.Name, Version: p.ClientInfo.Version}
	}
	return nil
}

func (h *Crashes) logger() *slog.Logger {
	if h.Logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return h.Logger
}

// handle logs, counts, saves and, if the user agreed, sends one crash, and returns it with what became of it.
func (h *Crashes) handle(ctx context.Context, c crash) crash {
	h.logger().Error("recovered from a crash", "crash", c.id, "operation", c.Operation, "panic", c.Message, "stack", c.Stack)
	h.Counters.Inc(telemetry.CounterCrashes)
	c.saved = h.save(c)
	if h.Setting.On() {
		if h.API == nil {
			c.sendErr = fmt.Errorf("no API is configured")
		} else {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendTimeout)
			c.sendErr = h.API.UploadCrashReport(ctx, c.Report.Fit())
			cancel()
			c.sent = c.sendErr == nil
		}
		if c.sendErr != nil {
			h.logger().Warn("could not send the crash report", "crash", c.id, "err", c.sendErr)
		}
	} else {
		c.off = true
	}
	return c
}

// crash is one recovered panic: the report as the service takes it, and what became of it here.
type crash struct {
	crashreports.Report
	id      string
	saved   string
	sent    bool
	off     bool
	sendErr error
}

func newCrash(operation string, client *crashreports.Client, v any) crash {
	id := crashID()
	return crash{id: id, Report: crashreports.Report{
		CrashID: id, Program: "dfm-mcp", Version: Version, EngineVersion: dfm.EngineVersion, OS: runtime.GOOS, Arch: runtime.GOARCH,
		Client: client, Operation: operation, Message: fmt.Sprint(v), Stack: stack(), OccurredAt: time.Now().UTC(),
	}}
}

func crashID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// stack lists the panicking goroutine's frames from where it panicked, as function and file base name: a build made with go install carries the user's home directory in its file paths.
func stack() string {
	pcs := make([]uintptr, 64)
	n := runtime.Callers(1, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	var all []runtime.Frame
	start := 0
	for {
		f, more := frames.Next()
		all = append(all, f)
		if f.Function == "runtime.gopanic" {
			start = len(all)
		}
		if !more {
			break
		}
	}
	var b strings.Builder
	for _, f := range all[start:] {
		fmt.Fprintf(&b, "%s\n\t%s:%d\n", f.Function, path.Base(f.File), f.Line)
	}
	return b.String()
}

// text is the crash as a user would read or file it.
func (c crash) text() string {
	client := "unknown"
	if c.Client != nil {
		client = c.Client.Name
	}
	return fmt.Sprintf("dfm-mcp %s crashed in %s.\n\n- crash: %s at %s\n- engine: %s\n- os/arch: %s/%s\n- client: %s\n\npanic: %s\n\n%s",
		c.Version, c.Operation, c.id, c.OccurredAt.Format(time.RFC3339), c.EngineVersion, c.OS, c.Arch, client, c.Message, c.Stack)
}

// issueURL opens a new issue prefilled with the report, cut short to stay within what a browser takes in a URL.
func (c crash) issueURL() string {
	body := c.text()
	if len(body) > 4000 {
		body = body[:4000] + "\n…"
	}
	body = "<!-- Add what you were doing if you can. Read this before submitting: it is everything that will be sent. -->\n\n```\n" + body + "\n```\n"
	q := url.Values{"title": {"crash: " + c.Operation + ": " + firstLine(c.Message, 80)}, "body": {body}}
	return issuesURL + "?" + q.Encode()
}

func firstLine(s string, max int) string {
	s, _, _ = strings.Cut(s, "\n")
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// message is the failed tool call: what happened and how the crash was or can be reported.
func (c crash) message() string {
	var b strings.Builder
	fmt.Fprintf(&b, "dfm-mcp hit a bug and could not finish %s (500 internal_error, crash %s): %s.\n\n", c.Operation, c.id, firstLine(c.Message, 300))
	b.WriteString("This is a fault in dfm-mcp, not a finding about the drawing, and retrying the same call will most likely fail again.\n\n")
	if c.saved != "" {
		fmt.Fprintf(&b, "The full crash report is at %s.\n", c.saved)
	}
	if c.sent {
		b.WriteString("The user turned crash reports on, so it was sent to the dfm-mcp maintainers: the version, where it failed, the panic and the stack, never the call's arguments.\n")
		return b.String()
	}
	if c.sendErr != nil {
		fmt.Fprintf(&b, "Crash reports are on, but sending this one failed: %v.\n", c.sendErr)
	}
	fmt.Fprintf(&b, "Ask the user whether to report it: this link opens a GitHub issue prefilled with the version, where it failed, the panic and the stack, never the call's arguments, for them to read before submitting:\n%s\n", c.issueURL())
	if c.off {
		b.WriteString("Running `dfm-mcp crash-reports on` sends crash reports to the maintainers as they happen.\n")
	}
	return b.String()
}

// save writes the report under the crash directory and keeps only the newest few. It returns the file's path, or "" when there is nowhere to save it.
func (h *Crashes) save(c crash) string {
	if h.Dir == "" {
		return ""
	}
	if err := os.MkdirAll(h.Dir, 0o700); err != nil {
		h.logger().Warn("could not save the crash report", "err", err)
		return ""
	}
	name := filepath.Join(h.Dir, c.OccurredAt.Format("20060102T150405.000000000Z")+"-"+c.id+".txt")
	if err := os.WriteFile(name, []byte(c.text()), 0o600); err != nil {
		h.logger().Warn("could not save the crash report", "err", err)
		return ""
	}
	if entries, err := os.ReadDir(h.Dir); err == nil && len(entries) > keptCrashReports {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		slices.Sort(names)
		for _, old := range names[:len(names)-keptCrashReports] {
			_ = os.Remove(filepath.Join(h.Dir, old))
		}
	}
	return name
}
