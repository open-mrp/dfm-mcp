package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/open-mrp/dfm-mcp/internal/client"
	"github.com/open-mrp/dfm-mcp/internal/crashreports"
	"github.com/open-mrp/dfm-mcp/telemetry"
)

// boomServer is a server with one extra tool that panics, connected to a client.
func boomServer(t *testing.T, local *Local, crashes *Crashes) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	s := New(Options{Local: local, Crashes: crashes})
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
	return session
}

// crashService is a fake of the service's crash-report route.
type crashService struct {
	mu      sync.Mutex
	reports []map[string]any
	keys    []string
	status  int
}

func (f *crashService) api(t *testing.T) *API {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.URL.Path != "/v1/dfm/crash-reports" || r.Header.Get(client.VersionHeader) != client.Version {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.status != 0 {
			w.WriteHeader(f.status)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"type": "invalid_request_error", "code": "validation_failed", "message": "no", "param": nil, "is_transient": false, "errors": []any{}}})
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.reports = append(f.reports, body)
		f.keys = append(f.keys, r.Header.Get("Idempotency-Key"))
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "dfmcr_fake", "object": "dfm_crash_report"})
	}))
	t.Cleanup(server.Close)
	return &API{Client: &client.Client{BaseURL: server.URL}}
}

func setting(t *testing.T, on bool, env map[string]string) *crashreports.Setting {
	t.Helper()
	s := crashreports.Open(t.TempDir(), func(k string) string { return env[k] })
	if err := s.Set(on); err != nil {
		t.Fatal(err)
	}
	return s
}

// With crash reports off, a panic in a tool fails that call with a report the user can file, is counted, keeps a report on the machine, sends nothing, and leaves the server answering.
func TestCrashIsRecovered(t *testing.T) {
	local := newLocal(t, nil)
	service := &crashService{}
	crashDir := t.TempDir()
	session := boomServer(t, local, &Crashes{Dir: crashDir, Setting: setting(t, false, nil), API: service.api(t)})

	res := call(t, session, "boom", map[string]any{"path": "/Users/someone/secret-part.step"})
	msg := text(res)
	if !res.IsError || !strings.Contains(msg, "internal_error") || !strings.Contains(msg, "index out of range") || !strings.Contains(msg, "not a finding") || !strings.Contains(msg, "dfm-mcp crash-reports on") {
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
	if !strings.HasPrefix(link.Query().Get("title"), "crash: boom: index out of range") || !strings.Contains(body, "client: test-client") || !strings.Contains(body, "boomServer") {
		t.Errorf("issue: %s\n%s", link.Query().Get("title"), body)
	}
	if _, after, _ := strings.Cut(body, "length 3\n\n"); !strings.HasPrefix(after, "github.com/open-mrp/dfm-mcp/internal/mcpserver.boomServer.func1\n\trecover_test.go:") {
		t.Errorf("the stack does not start where the panic was:\n%s", body)
	}
	wd, _ := os.Getwd()
	if strings.Contains(body, "secret-part") || strings.Contains(body, wd) {
		t.Errorf("the report carries the arguments or a local path:\n%s", body)
	}

	saved, _ := filepath.Glob(filepath.Join(crashDir, "*.txt"))
	if len(saved) != 1 || !strings.Contains(msg, saved[0]) {
		t.Errorf("crash reports %v, message %s", saved, msg)
	}
	if n := local.Counters.Snapshot()[telemetry.CounterCrashes]; n != 1 {
		t.Errorf("crashes counted: %d", n)
	}
	if len(service.reports) != 0 {
		t.Errorf("a report was sent with crash reports off: %v", service.reports)
	}
	if res := call(t, session, "dfm_list_capability_presets", map[string]any{"process": "laser_cutting"}); res.IsError {
		t.Errorf("the server stopped answering after a crash: %s", text(res))
	}
}

// With crash reports on, the crash goes to the service in the shape it takes, keyed by its crash id, and the call says so instead of asking for an issue.
func TestCrashIsSentWhenOn(t *testing.T) {
	service := &crashService{}
	session := boomServer(t, newLocal(t, nil), &Crashes{Setting: setting(t, true, nil), API: service.api(t)})

	res := call(t, session, "boom", map[string]any{"path": "/Users/someone/secret-part.step"})
	msg := text(res)
	if !res.IsError || !strings.Contains(msg, "sent to the dfm-mcp maintainers") || strings.Contains(msg, issuesURL) {
		t.Fatalf("crash result: %s", msg)
	}
	if len(service.reports) != 1 {
		t.Fatalf("reports sent: %d", len(service.reports))
	}
	r := service.reports[0]
	for _, k := range []string{"crash_id", "program", "version", "engine_version", "os", "arch", "operation", "message", "stack", "occurred_at"} {
		if s, _ := r[k].(string); s == "" {
			t.Errorf("report field %s is empty: %v", k, r)
		}
	}
	if r["operation"] != "boom" || r["program"] != "dfm-mcp" || r["client"].(map[string]any)["name"] != "test-client" || len(r["crash_id"].(string)) != 16 {
		t.Errorf("report = %v", r)
	}
	if service.keys[0] != "crash-report:"+r["crash_id"].(string) {
		t.Errorf("idempotency key = %q", service.keys[0])
	}
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), "secret-part") {
		t.Errorf("the report carries the call's arguments: %s", raw)
	}
}

// When the service does not take the report, the call says so and offers the issue link instead.
func TestCrashReportThatFailsFallsBackToTheLink(t *testing.T) {
	service := &crashService{status: http.StatusUnprocessableEntity}
	session := boomServer(t, newLocal(t, nil), &Crashes{Setting: setting(t, true, nil), API: service.api(t)})
	msg := text(call(t, session, "boom", map[string]any{"path": "x"}))
	if !strings.Contains(msg, "sending this one failed") || !strings.Contains(msg, issuesURL) {
		t.Errorf("crash result: %s", msg)
	}
}

// DO_NOT_TRACK keeps crash reports on the machine whatever the setting says.
func TestDoNotTrackSendsNoCrashReport(t *testing.T) {
	service := &crashService{}
	session := boomServer(t, newLocal(t, nil), &Crashes{Setting: setting(t, true, map[string]string{"DO_NOT_TRACK": "1"}), API: service.api(t)})
	call(t, session, "boom", map[string]any{"path": "x"})
	if len(service.reports) != 0 {
		t.Errorf("a report was sent with DO_NOT_TRACK set: %v", service.reports)
	}
}

// A background task that panics stops on its own; the crash is handled like a request's, and the caller carries on.
func TestGuardRecoversABackgroundTask(t *testing.T) {
	local := newLocal(t, nil)
	service := &crashService{}
	crashDir := t.TempDir()
	h := &Crashes{Dir: crashDir, Counters: local.Counters, Setting: setting(t, true, nil), API: service.api(t)}
	h.Guard("telemetry upload", func() { panic("assignment to entry in nil map") })
	if n := local.Counters.Snapshot()[telemetry.CounterCrashes]; n != 1 {
		t.Errorf("crashes counted: %d", n)
	}
	if saved, _ := filepath.Glob(filepath.Join(crashDir, "*.txt")); len(saved) != 1 {
		t.Errorf("crash reports saved: %v", saved)
	}
	if len(service.reports) != 1 || service.reports[0]["operation"] != "telemetry upload" || !strings.Contains(service.reports[0]["message"].(string), "nil map") {
		t.Errorf("reports sent: %v", service.reports)
	}
}

// Only the newest crash reports are kept.
func TestCrashReportsArePruned(t *testing.T) {
	h := &Crashes{Dir: t.TempDir()}
	for range keptCrashReports + 5 {
		h.save(newCrash("tools/call", nil, "x"))
	}
	if entries, _ := os.ReadDir(h.Dir); len(entries) != keptCrashReports {
		t.Errorf("kept %d reports, want %d", len(entries), keptCrashReports)
	}
}
