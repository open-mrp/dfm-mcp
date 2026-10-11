package mcpserver

import (
	"context"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/open-mrp/dfm-mcp/telemetry"
)

// A panic in a tool fails that call with a report the user can file, counts as an error, keeps a report on the machine, and leaves the server answering.
func TestCrashIsRecovered(t *testing.T) {
	ctx := context.Background()
	local := newLocal(t, nil)
	crashDir := t.TempDir()
	s := New(Options{Local: local, CrashDir: crashDir})
	mcp.AddTool(s, &mcp.Tool{Name: "boom"}, func(context.Context, *mcp.CallToolRequest, struct {
		Path string `json:"path"`
	}) (*mcp.CallToolResult, any, error) {
		panic("index out of range [3] with length 3")
	})
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	res := call(t, session, "boom", map[string]any{"path": "/Users/someone/secret-part.step"})
	msg := text(res)
	if !res.IsError || !strings.Contains(msg, "internal_error") || !strings.Contains(msg, "index out of range") || !strings.Contains(msg, "not a finding") {
		t.Fatalf("crash result: error=%v %s", res.IsError, msg)
	}
	i := strings.Index(msg, issuesURL+"?")
	if i < 0 {
		t.Fatalf("no issue link: %s", msg)
	}
	link, err := url.Parse(strings.Fields(msg[i:])[0])
	if err != nil {
		t.Fatal(err)
	}
	body := link.Query().Get("body")
	if !strings.HasPrefix(link.Query().Get("title"), "crash: boom: index out of range") || !strings.Contains(body, "client: test-client") || !strings.Contains(body, "TestCrashIsRecovered") {
		t.Errorf("issue: %s\n%s", link.Query().Get("title"), body)
	}
	wd, _ := os.Getwd()
	if strings.Contains(body, "secret-part") || strings.Contains(body, wd) {
		t.Errorf("the report carries the arguments or a local path:\n%s", body)
	}

	saved, _ := filepath.Glob(filepath.Join(crashDir, "*.txt"))
	if len(saved) != 1 || !strings.Contains(msg, saved[0]) {
		t.Errorf("crash reports %v, message %s", saved, msg)
	}
	if n := local.Counters.Snapshot()[telemetry.CounterErrors]; n != 1 {
		t.Errorf("errors counted: %d", n)
	}
	if res := call(t, session, "dfm_list_capability_presets", map[string]any{"process": "laser_cutting"}); res.IsError {
		t.Errorf("the server stopped answering after a crash: %s", text(res))
	}
}

// Only the newest crash reports are kept.
func TestCrashReportsArePruned(t *testing.T) {
	dir := t.TempDir()
	for range keptCrashReports + 5 {
		newCrash("tools/call", &mcp.CallToolRequest{}, "x").save(dir, slog.New(slog.DiscardHandler))
	}
	if entries, _ := os.ReadDir(dir); len(entries) != keptCrashReports {
		t.Errorf("kept %d reports, want %d", len(entries), keptCrashReports)
	}
}
