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

	"github.com/open-mrp/dfm-mcp/telemetry"
)

// issuesURL is where a user files a crash. The report is prefilled from what crashed and the user reads it before sending, so nothing leaves the machine unless they choose to.
const issuesURL = "https://github.com/open-mrp/dfm-mcp/issues/new"

// keptCrashReports bounds the crash reports kept on the machine; the oldest go first.
const keptCrashReports = 20

// recoverPanics turns a panic in any request into a failed request, so one bad drawing cannot take the server down with it. A failed tool call says what happened and how to report it, the crash is counted with the other errors, its report is saved under crashDir when one is set, and the stack goes to the log.
func recoverPanics(local *Local, logger *slog.Logger, crashDir string) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (result mcp.Result, err error) {
			defer func() {
				v := recover()
				if v == nil {
					return
				}
				c := newCrash(method, req, v)
				logger.Error("recovered from a crash", "id", c.id, "method", method, "tool", c.tool, "panic", c.panic, "stack", c.stack)
				if local != nil {
					local.Counters.Inc(telemetry.CounterErrors)
				}
				saved := c.save(crashDir, logger)
				if method == "tools/call" {
					result, err = &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: c.message(saved)}}}, nil
					return
				}
				result, err = nil, fmt.Errorf("dfm-mcp crashed handling %s (crash %s)", method, c.id)
			}()
			return next(ctx, method, req)
		}
	}
}

// crash is what is known about one recovered panic: where it happened and in what build, never the request's arguments, which can name the user's files.
type crash struct {
	id, method, tool, client, panic, stack string
	at                                     time.Time
}

func newCrash(method string, req mcp.Request, v any) crash {
	c := crash{id: crashID(), method: method, panic: fmt.Sprint(v), stack: stack(), at: time.Now().UTC()}
	if r, ok := req.(*mcp.CallToolRequest); ok && r.Params != nil {
		c.tool = r.Params.Name
	}
	if ss, ok := req.GetSession().(*mcp.ServerSession); ok && ss != nil {
		if p := ss.InitializeParams(); p != nil && p.ClientInfo != nil {
			c.client = p.ClientInfo.Name
		}
	}
	return c
}

func crashID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// stack lists the panicking goroutine's frames as function and file base name, leaving out directories: a build made with go install carries the user's home directory in its file paths.
func stack() string {
	pcs := make([]uintptr, 64)
	n := runtime.Callers(4, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	var b strings.Builder
	for {
		f, more := frames.Next()
		fmt.Fprintf(&b, "%s\n\t%s:%d\n", f.Function, path.Base(f.File), f.Line)
		if !more {
			break
		}
	}
	return b.String()
}

func (c crash) where() string {
	if c.tool != "" {
		return c.tool
	}
	return c.method
}

// report is the crash as a user would file it.
func (c crash) report() string {
	client := c.client
	if client == "" {
		client = "unknown"
	}
	return fmt.Sprintf("dfm-mcp %s crashed in %s.\n\n- crash: %s at %s\n- engine: %s\n- os/arch: %s/%s\n- client: %s\n\npanic: %s\n\n%s",
		Version, c.where(), c.id, c.at.Format(time.RFC3339), dfm.EngineVersion, runtime.GOOS, runtime.GOARCH, client, c.panic, c.stack)
}

// issueURL opens a new issue prefilled with the report, cut short to stay within what a browser takes in a URL.
func (c crash) issueURL() string {
	body := c.report()
	if len(body) > 4000 {
		body = body[:4000] + "\n…"
	}
	body = "<!-- Add what you were doing if you can. Read this before submitting: it is everything that will be sent. -->\n\n```\n" + body + "\n```\n"
	q := url.Values{"title": {"crash: " + c.where() + ": " + firstLine(c.panic, 80)}, "body": {body}}
	return issuesURL + "?" + q.Encode()
}

func firstLine(s string, max int) string {
	s, _, _ = strings.Cut(s, "\n")
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// message is the failed tool call: what happened and how to report it.
func (c crash) message(saved string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "dfm-mcp hit a bug and could not finish %s (500 internal_error, crash %s): %s.\n\n", c.where(), c.id, firstLine(c.panic, 300))
	b.WriteString("This is a fault in dfm-mcp, not a finding about the drawing, and retrying the same call will most likely fail again.\n\n")
	if saved != "" {
		fmt.Fprintf(&b, "The full crash report is at %s.\n", saved)
	}
	fmt.Fprintf(&b, "Ask the user whether to report it: this link opens a GitHub issue prefilled with the version, where it failed, the panic and the stack, never the call's arguments, for them to read before submitting:\n%s\n", c.issueURL())
	return b.String()
}

// save writes the report under dir and keeps only the newest few. It returns the file's path, or "" when there is nowhere to save it.
func (c crash) save(dir string, logger *slog.Logger) string {
	if dir == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		logger.Warn("could not save the crash report", "err", err)
		return ""
	}
	name := filepath.Join(dir, c.at.Format("20060102T150405.000000000Z")+"-"+c.id+".txt")
	if err := os.WriteFile(name, []byte(c.report()), 0o600); err != nil {
		logger.Warn("could not save the crash report", "err", err)
		return ""
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > keptCrashReports {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		slices.Sort(names)
		for _, old := range names[:len(names)-keptCrashReports] {
			_ = os.Remove(filepath.Join(dir, old))
		}
	}
	return name
}
