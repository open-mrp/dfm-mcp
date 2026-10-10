package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/open-mrp/dfm-mcp/internal/app"
	"github.com/open-mrp/dfm-mcp/internal/client"
	"github.com/open-mrp/dfm-mcp/internal/localstore"
	"github.com/open-mrp/dfm-mcp/telemetry"
)

// connect serves the given backend over an in-memory MCP transport and returns a client session.
func connect(t *testing.T, local *Local, clientOpts *mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := New(Options{Local: local}).Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, clientOpts).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func newLocal(t *testing.T, api *API) *Local {
	t.Helper()
	store, err := localstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	counters, err := telemetry.Open(t.TempDir(), t.TempDir(), "dfm-mcp", "test", "0", func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	return &Local{Store: store, API: api, Counters: counters}
}

func fixturePath(t *testing.T) string {
	t.Helper()
	design, err := os.ReadFile("../engine/testdata/valid_rectangle.dxf")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rectangle.dxf")
	if err := os.WriteFile(path, design, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func toolNames(t *testing.T, s *mcp.ClientSession) []string {
	t.Helper()
	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func call(t *testing.T, s *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", name, err)
	}
	return res
}

func text(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Everything but recording and feedback works with no server at all: the engine, the catalogs, the resources and the prompt are in the binary.
func TestWorksWithoutAServer(t *testing.T) {
	app.Register()
	local := newLocal(t, nil)
	s := connect(t, local, nil)

	want := []string{"dfm_check_design", "dfm_get_capability_preset", "dfm_get_check", "dfm_list_capability_presets", "dfm_list_finding_codes", "dfm_report_feedback"}
	if names := toolNames(t, s); strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("tools = %v, want %v", names, want)
	}

	res := call(t, s, "dfm_list_capability_presets", map[string]any{"process": "laser_cutting"})
	if res.IsError || !strings.Contains(text(res), "fiber-3kw-3015") {
		t.Errorf("presets: error=%v %s", res.IsError, text(res))
	}
	if res := call(t, s, "dfm_list_capability_presets", map[string]any{"process": "welding"}); !res.IsError {
		t.Error("an unknown process should be refused by the input schema")
	}
	res = call(t, s, "dfm_list_finding_codes", map[string]any{"codes": []string{"hole_too_small", "no_such_code"}})
	if res.IsError || !strings.Contains(text(res), "hole_too_small") || !strings.Contains(text(res), "no_such_code: not a finding code") {
		t.Errorf("finding codes: %s", text(res))
	}
	if rr, err := s.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "dfm://capability-presets/fiber-3kw-3015"}); err != nil || !strings.Contains(rr.Contents[0].Text, `"schema_version"`) {
		t.Errorf("preset resource: %v", err)
	}
	if _, err := s.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "dfm://capability-presets/nope"}); err == nil {
		t.Error("an unknown preset should be a resource-not-found error")
	}
	prompt, err := s.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: "dfm_review_drawing", Arguments: map[string]string{"drawing": "bracket.dxf", "material": "mild_steel", "thickness_mm": "1.5"}})
	if err != nil || len(prompt.Messages) != 1 || !strings.Contains(prompt.Messages[0].Content.(*mcp.TextContent).Text, "bracket.dxf") {
		t.Errorf("prompt: %v %+v", err, prompt)
	}

	path := fixturePath(t)
	res = call(t, s, "dfm_check_design", map[string]any{"file_path": path, "material_code": "mild_steel", "thickness": "1.5", "capability_preset": "fiber-3kw-3015"})
	if res.IsError {
		t.Fatalf("check: %s", text(res))
	}
	var out CheckOutput
	if err := json.Unmarshal(mustJSON(t, res.StructuredContent), &out); err != nil || out.Origin != "client" || out.Verdict == "" || out.Result != nil {
		t.Fatalf("check output: %v %+v", err, out)
	}
	if !strings.Contains(text(res), out.Verdict) || !strings.Contains(text(res), "dfm_get_check") {
		t.Errorf("check text: %s", text(res))
	}
	foundLink := false
	for _, content := range res.Content {
		if link, ok := content.(*mcp.ResourceLink); ok && link.URI == "dfm://checks/"+out.CheckID {
			foundLink = true
		}
	}
	if !foundLink {
		t.Error("the check result should link to its resource")
	}
	if e, ok, _ := local.Store.Load(out.CheckID); !ok || e.DesignPath != path || e.RemoteID != "" {
		t.Errorf("stored entry: ok=%v %+v", ok, e)
	}
	counts := local.Counters.Snapshot()
	if counts[telemetry.CounterChecks] != 1 || counts["verdict:"+out.Verdict] != 1 || counts["format:dxf"] != 1 {
		t.Errorf("counters = %v", counts)
	}

	res = call(t, s, "dfm_get_check", map[string]any{"check_id": out.CheckID, "response_format": "detailed"})
	var detailed CheckOutput
	if err := json.Unmarshal(mustJSON(t, res.StructuredContent), &detailed); err != nil || detailed.Result == nil || detailed.Result.ID != out.CheckID {
		t.Errorf("detailed: %v %+v", err, detailed)
	}
	if rr, err := s.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "dfm://checks/" + out.CheckID}); err != nil || !strings.Contains(rr.Contents[0].Text, `"origin":"client"`) {
		t.Errorf("check resource: %v", err)
	}
	if res := call(t, s, "dfm_get_check", map[string]any{"check_id": "dfck_nope"}); !res.IsError || !strings.Contains(text(res), "resource_not_found") {
		t.Errorf("unknown check: %s", text(res))
	}
	// Engine errors reach the agent as tool errors with the API's shape.
	if res := call(t, s, "dfm_check_design", map[string]any{"file_path": path, "material_code": "mild_steel", "thickness": "1.5"}); !res.IsError || !strings.Contains(text(res), "validation_failed") || !strings.Contains(text(res), "capability") {
		t.Errorf("missing capability: error=%v %s", res.IsError, text(res))
	}
	// Recording and feedback need the API.
	if res := call(t, s, "dfm_check_design", map[string]any{"file_path": path, "material_code": "mild_steel", "thickness": "1.5", "capability_preset": "fiber-3kw-3015", "record": true}); !res.IsError || !strings.Contains(text(res), "no API configured") {
		t.Errorf("record without an API: %s", text(res))
	}
	if res := call(t, s, "dfm_report_feedback", map[string]any{"check_id": out.CheckID, "type": "false_negative", "expected": map[string]any{"code": "hole_too_small", "location": map[string]string{"x": "1", "y": "1"}}, "reason": "r", "evidence_type": "user_report"}); !res.IsError || !strings.Contains(text(res), "no API configured") {
		t.Errorf("feedback without an API: %s", text(res))
	}
}

// With an API: a key is minted the first time it is needed; record puts a check on the server at once; a report records a local check first and shares the drawing from its original path; consent is asked for through elicitation; telemetry uploads land.
// fakeAPI is the little of the DFM API the server talks to, enough to exercise recording, feedback and telemetry without the API's own code, which lives in a private repository. It checks what the real API checks on these paths: a key on every call but key creation and telemetry, a feedback report's design matching the recorded check's hash, and the telemetry counter allowlist.
type fakeAPI struct {
	t       *testing.T
	checks  map[string]map[string]any
	designs map[string]map[string]any
	minted  int
	reports int
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fail := func(status int, code, message, param string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"type": "invalid_request_error", "code": code, "message": message, "param": param, "is_transient": false, "errors": []any{}}})
	}
	if r.Header.Get(client.VersionHeader) != client.Version {
		fail(http.StatusBadRequest, "api_version_required", "Send the API version.", "")
		return
	}
	var body map[string]any
	var raw []byte
	if r.Body != nil {
		raw, _ = io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
	}
	needsKey := !(r.Method == http.MethodPost && (r.URL.Path == "/v1/dfm/api-keys" || r.URL.Path == "/v1/dfm/telemetry-reports"))
	if needsKey && !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer dfm_sk_") {
		fail(http.StatusUnauthorized, "invalid_credentials", "The API key is not valid.", "")
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/dfm/api-keys":
		f.minted++
		w.WriteHeader(http.StatusCreated)
		if len(r.Header.Get("Idempotency-Key")) < 16 {
			fail(http.StatusUnprocessableEntity, "validation_failed", "A mint must carry an idempotency key.", "")
			return
		}
		if c, _ := body["client"].(map[string]any); c["name"] != "dfm-mcp" {
			fail(http.StatusUnprocessableEntity, "validation_failed", "The MCP must name itself as the client.", "client.name")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "dfm_created_api_key", "secret": "dfm_sk_" + strings.Repeat("x", 36), "api_key": map[string]any{"id": "apke_fake", "object": "dfm_api_key"}})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/dfm/checks/actions/record":
		result, _ := body["result"].(map[string]any)
		design, _ := body["design"].(map[string]any)
		id := fmt.Sprintf("dfck_r%011d", len(f.checks))
		design["retention"] = "none"
		check := map[string]any{"id": id, "object": "dfm_check", "origin": "client", "design": design, "stock": map[string]any{}, "capability_preset": body["capability_preset"], "preview": nil, "created_at": "2026-10-10T00:00:00Z"}
		for k, v := range result {
			if _, taken := check[k]; !taken {
				check[k] = v
			}
		}
		f.checks[id] = check
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(check)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/dfm/checks/"):
		check, ok := f.checks[strings.TrimPrefix(r.URL.Path, "/v1/dfm/checks/")]
		if !ok {
			fail(http.StatusNotFound, "resource_not_found", "No check with this ID.", "")
			return
		}
		_ = json.NewEncoder(w).Encode(check)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/dfm/designs":
		sha, _ := body["sha256"].(string)
		if len(sha) != 64 {
			fail(http.StatusUnprocessableEntity, "validation_failed", "sha256 is required.", "sha256")
			return
		}
		id := fmt.Sprintf("dfde_%011d", len(f.designs))
		f.designs[id] = map[string]any{"sha256": sha, "uploaded": false}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "object": "dfm_design", "format": body["format"], "status": "awaiting_upload", "upload": map[string]any{"url": "/v1/dfm/designs/" + id + "/content", "method": "PUT", "headers": map[string]string{"Content-Type": "application/dxf"}, "expires_at": "2026-10-11T00:00:00Z"}, "expires_at": "2026-10-11T00:00:00Z", "created_at": "2026-10-10T00:00:00Z", "updated_at": "2026-10-10T00:00:00Z"})
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1/dfm/designs/") && strings.HasSuffix(r.URL.Path, "/content"):
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/dfm/designs/"), "/content")
		design, ok := f.designs[id]
		if !ok {
			fail(http.StatusNotFound, "resource_not_found", "No design with this ID.", "")
			return
		}
		if r.Header.Get("Content-Type") != "application/dxf" {
			fail(http.StatusBadRequest, "upload_headers", "Send the headers the upload target was issued with.", "")
			return
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != design["sha256"] {
			fail(http.StatusUnprocessableEntity, "design_mismatch", "The file is not the one the design was created for.", "")
			return
		}
		design["uploaded"] = true
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "object": "dfm_design", "status": "uploaded"})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/dfm/feedback-reports":
		checkID, _ := body["check_id"].(string)
		check, ok := f.checks[checkID]
		if !ok {
			fail(http.StatusUnprocessableEntity, "validation_failed", "No check with this ID.", "check_id")
			return
		}
		retention := "none"
		checkDesign := check["design"].(map[string]any)
		if design, ok := body["design"].(map[string]any); ok && design["retention"] == "retained" && checkDesign["retention"] == "retained" && design["design_id"] == nil {
			// As the API does: a check that already kept its drawing needs no upload.
			retention = "retained"
		} else if ok && design["retention"] == "retained" {
			id, _ := design["design_id"].(string)
			uploaded, ok := f.designs[id]
			if !ok || uploaded["uploaded"] != true {
				fail(http.StatusUnprocessableEntity, "design_not_uploaded", "The design's file has not been uploaded.", "design.design_id")
				return
			}
			if uploaded["sha256"] != check["design"].(map[string]any)["sha256"] {
				fail(http.StatusUnprocessableEntity, "design_mismatch", "The design is not the file the check ran on.", "design.design_id")
				return
			}
			retention = "retained"
			check["design"].(map[string]any)["retention"] = "retained"
		}
		f.reports++
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": fmt.Sprintf("dffb_%012d", f.reports), "object": "dfm_feedback_report", "check": nil, "type": body["type"], "occurrence_id": body["occurrence_id"], "expected": body["expected"], "reason": body["reason"], "evidence": body["evidence"], "design": map[string]any{"retention": retention}, "created_at": "2026-10-10T00:00:00Z"})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/dfm/telemetry-reports":
		counters, _ := body["counters"].(map[string]any)
		for name := range counters {
			if !telemetry.Allowed(name) {
				fail(http.StatusUnprocessableEntity, "validation_failed", "Not a counter this service accepts.", "counters."+name)
				return
			}
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "dftm_fake", "object": "dfm_telemetry_report"})
	default:
		fail(http.StatusNotFound, "resource_not_found", "No such route.", "")
	}
}

func TestWithTheAPI(t *testing.T) {
	app.Register()
	ctx := context.Background()
	fake := &fakeAPI{t: t, checks: map[string]map[string]any{}, designs: map[string]map[string]any{}}
	server := httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(server.Close)

	api := &API{Client: &client.Client{BaseURL: server.URL}}
	api.Provision = func(ctx context.Context, c *client.Client) error {
		key, err := api.CreateAPIKey(ctx, "test", "test-installation")
		if err != nil {
			return err
		}
		c.APIKey = key.Secret
		return nil
	}
	local := newLocal(t, api)
	asked := 0
	s := connect(t, local, &mcp.ClientOptions{
		ElicitationHandler: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			asked++
			if !strings.Contains(req.Params.Message, "keep this drawing") {
				t.Errorf("elicitation message: %s", req.Params.Message)
			}
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"share_design": true}}, nil
		},
	})
	path := fixturePath(t)

	// A plain check contacts nothing.
	res := call(t, s, "dfm_check_design", map[string]any{"file_path": path, "material_code": "mild_steel", "thickness": "1.5", "capability_preset": "fiber-3kw-3015"})
	var out CheckOutput
	if err := json.Unmarshal(mustJSON(t, res.StructuredContent), &out); err != nil {
		t.Fatal(err)
	}
	if fake.minted != 0 {
		t.Error("a local check must not contact the API")
	}

	// A recorded check is on the server at once, under the first key minted.
	res = call(t, s, "dfm_check_design", map[string]any{"file_path": path, "material_code": "mild_steel", "thickness": "1.5", "capability_preset": "fiber-3kw-3015", "record": true})
	if res.IsError {
		t.Fatalf("recorded check: %s", text(res))
	}
	var recordedOut CheckOutput
	if err := json.Unmarshal(mustJSON(t, res.StructuredContent), &recordedOut); err != nil {
		t.Fatal(err)
	}
	e, _, _ := local.Store.Load(recordedOut.CheckID)
	if e.RemoteID == "" || fake.minted != 1 {
		t.Errorf("record: entry=%+v minted=%d", e, fake.minted)
	}
	if recorded, err := api.GetCheck(ctx, e.RemoteID); err != nil || recorded.Origin != "client" || recorded.Design.SHA256 != recordedOut.Design.SHA256 {
		t.Errorf("recorded check: %v %+v", err, recorded)
	}

	// A report on the unrecorded check: the user is asked, agrees, the check is recorded, the drawing is re-read from its path and kept.
	res = call(t, s, "dfm_report_feedback", map[string]any{
		"check_id": out.CheckID, "type": "false_negative",
		"expected": map[string]any{"code": "hole_too_small", "location": map[string]string{"x": "10", "y": "10"}},
		"reason":   "test", "evidence_type": "user_report",
	})
	if res.IsError || !strings.Contains(text(res), "Design kept with the report: retained") {
		t.Fatalf("report: error=%v %s", res.IsError, text(res))
	}
	if asked != 1 || fake.minted != 1 {
		t.Errorf("asked=%d minted=%d, want 1 and 1", asked, fake.minted)
	}
	e, _, _ = local.Store.Load(out.CheckID)
	if e.RemoteID == "" || e.RemoteID == out.CheckID {
		t.Fatalf("entry after report: %+v", e)
	}
	if recorded, err := api.GetCheck(ctx, e.RemoteID); err != nil || recorded.Design.Retention != "retained" {
		t.Errorf("recorded check after sharing: %v %+v", err, recorded)
	}
	if res := call(t, s, "dfm_get_check", map[string]any{"check_id": e.RemoteID}); res.IsError {
		t.Errorf("get by remote id: %s", text(res))
	}
	// An explicit share_design does not ask, a second report does not record again, and a check that already kept its drawing needs no second upload.
	designs := len(fake.designs)
	res = call(t, s, "dfm_report_feedback", map[string]any{"check_id": out.CheckID, "type": "false_negative", "expected": map[string]any{"code": "hole_too_small", "location": map[string]string{"x": "1", "y": "1"}}, "reason": "again", "evidence_type": "user_report", "share_design": true})
	if res.IsError || !strings.Contains(text(res), "Design kept with the report: retained") || asked != 1 || len(fake.designs) != designs {
		t.Errorf("share on a kept check: error=%v asked=%d uploads=%d->%d %s", res.IsError, asked, designs, len(fake.designs), text(res))
	}
	// Sharing a file that is not the one the check ran on is refused, on the recorded check that kept nothing.
	res = call(t, s, "dfm_report_feedback", map[string]any{"check_id": recordedOut.CheckID, "type": "false_negative", "expected": map[string]any{"code": "hole_too_small", "location": map[string]string{"x": "1", "y": "1"}}, "reason": "other file", "evidence_type": "user_report", "share_design": true, "content_base64": base64.StdEncoding.EncodeToString([]byte("other"))})
	if !res.IsError || !strings.Contains(text(res), "design_mismatch") || asked != 1 {
		t.Errorf("mismatched share: error=%v asked=%d %s", res.IsError, asked, text(res))
	}
	counts := local.Counters.Snapshot()
	if counts[telemetry.CounterChecks] != 2 || counts[telemetry.CounterChecksRecorded] != 1 || counts[telemetry.CounterFeedback] != 2 {
		t.Errorf("counters = %v", counts)
	}

	// Telemetry: an upload needs no key and accepts only the documented counters.
	report := telemetry.Report{InstallationID: "0123456789abcdef0123456789abcdef", Program: "dfm-mcp", Version: "test", EngineVersion: "test", OS: "darwin", Arch: "arm64",
		PeriodStartedAt: time.Now().Add(-time.Minute), PeriodEndedAt: time.Now(), Counters: local.Counters.Snapshot()}
	if err := api.UploadTelemetry(ctx, report, "telemetry:0123456789abcdef"); err != nil {
		t.Errorf("upload: %v", err)
	}
	report.Counters["drawing_name"] = 1
	if err := (&API{Client: &client.Client{BaseURL: server.URL}}).UploadTelemetry(ctx, report, ""); err == nil || !strings.Contains(err.Error(), "counters.drawing_name") {
		t.Errorf("bad counter: %v", err)
	}
}
