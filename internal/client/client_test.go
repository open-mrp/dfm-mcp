package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A POST that fails transiently is retried with the same Idempotency-Key, so the service can answer the retry with the first response; a client error is not retried.
func TestRetriesTransientFailuresWithOneKey(t *testing.T) {
	var keys []string
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		switch {
		case strings.HasSuffix(r.URL.Path, "/bad"):
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","code":"validation_failed","message":"no","param":"x","is_transient":false,"errors":[],"doc_url":"https://docs.example/validation_failed"}}`))
		case calls == 1:
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"type":"api_error","code":"service_unavailable","message":"busy","param":null,"is_transient":true,"errors":[],"doc_url":null}}`))
		default:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"x"}`))
		}
	}))
	defer server.Close()
	c := &Client{BaseURL: server.URL}

	var out struct{ ID string }
	if err := c.Do(context.Background(), http.MethodPost, "/v1/dfm/things", nil, map[string]any{}, &out); err != nil || out.ID != "x" {
		t.Fatalf("Do = %v, %+v", err, out)
	}
	if calls != 2 || keys[0] == "" || keys[0] != keys[1] || len(keys[0]) < 16 {
		t.Fatalf("calls=%d keys=%q, want 2 calls with one generated key", calls, keys)
	}

	calls, keys = 0, nil
	err := c.Do(context.Background(), http.MethodPost, "/v1/dfm/bad", nil, map[string]any{}, nil)
	if calls != 1 {
		t.Fatalf("a 422 was retried: %d calls", calls)
	}
	if err == nil || !strings.Contains(err.Error(), "https://docs.example/validation_failed") {
		t.Fatalf("error = %v, want the doc_url in it", err)
	}
}

// A report refused only for counters names them, so the rest can be sent; any other refusal names none.
func TestRejectedCounters(t *testing.T) {
	for _, tc := range []struct {
		status int
		errors string
		want   string
	}{
		{422, `[{"param":"counters.crashes","code":"invalid_format","message":"x"}]`, "crashes"},
		{422, `[{"param":"counters.crashes","code":"invalid_format","message":"x"},{"param":"counters.new","code":"invalid_format","message":"x"}]`, "crashes,new"},
		{422, `[{"param":"counters.crashes","code":"invalid_format","message":"x"},{"param":"period_ended_at","code":"invalid_format","message":"x"}]`, ""},
		{422, `[{"param":null,"code":"invalid_format","message":"x"}]`, ""},
		{422, `[]`, ""},
		{400, `[{"param":"counters.crashes","code":"invalid_format","message":"x"}]`, ""},
	} {
		e := &Error{Status: tc.status, Errors: []byte(tc.errors)}
		if got := strings.Join(e.RejectedCounters(), ","); got != tc.want {
			t.Errorf("%d %s: got %q, want %q", tc.status, tc.errors, got, tc.want)
		}
	}
}
