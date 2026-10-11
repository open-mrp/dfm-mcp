package telemetry

import (
	"context"
	"errors"
	"testing"
	"time"
)

type sent struct {
	report Report
	key    string
}

type fakeUploader struct {
	sent []sent
	err  error
}

func (f *fakeUploader) UploadTelemetry(_ context.Context, r Report, key string) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, sent{r, key})
	return nil
}

// refused is an error the service would return for a report it will never take.
type refused struct{}

func (refused) Error() string   { return "refused" }
func (refused) Permanent() bool { return true }

func open(t *testing.T, env map[string]string) *Counters {
	t.Helper()
	c, err := Open(t.TempDir(), t.TempDir(), "dfm-mcp", "test", "0.0.0", func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLocalByDefaultCountsAndSendsNothing(t *testing.T) {
	c := open(t, nil)
	if c.Mode() != ModeLocal {
		t.Errorf("mode = %s", c.Mode())
	}
	c.Inc(CounterChecks, PrefixVerdict+"manufacturable", "not_a_counter")
	week := c.Snapshot()
	if week[CounterChecks] != 1 || week["verdict:manufacturable"] != 1 || len(week) != 2 {
		t.Errorf("counters = %v", week)
	}
	up := &fakeUploader{}
	c.Flush(context.Background(), up)
	if len(up.sent) != 0 {
		t.Error("local mode must not upload")
	}
	// Turning uploading on later does not send what was counted before.
	if err := c.SetMode(ModeOn); err != nil {
		t.Fatal(err)
	}
	c.Flush(context.Background(), up)
	if len(up.sent) != 0 {
		t.Error("counts from before the user turned uploading on were sent")
	}
}

func TestDoNotTrackTurnsEverythingOff(t *testing.T) {
	c := open(t, map[string]string{"DO_NOT_TRACK": "1"})
	if err := c.SetMode(ModeOn); err != nil {
		t.Fatal(err)
	}
	if c.Mode() != ModeOff {
		t.Errorf("mode = %s", c.Mode())
	}
	c.Inc(CounterChecks)
	if len(c.Snapshot()) != 0 {
		t.Error("off must count nothing")
	}
}

// With uploading on, each flush sends what was counted since the last one, once, with the installation id and the client.
func TestSendsEachBatchOnceWhenOn(t *testing.T) {
	c := open(t, nil)
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	if err := c.SetMode(ModeOn); err != nil {
		t.Fatal(err)
	}
	c.SetClient("claude-code", "2.1.0")
	c.Inc(CounterChecks, PrefixFormat+"dxf")
	now = now.Add(40 * time.Second)
	c.Inc(CounterChecks)
	up := &fakeUploader{}
	c.Flush(context.Background(), up)
	if len(up.sent) != 1 {
		t.Fatalf("sent %d reports, want 1", len(up.sent))
	}
	r := up.sent[0].report
	id, _ := c.InstallationID()
	if r.Counters[CounterChecks] != 2 || r.Counters["format:dxf"] != 1 || r.InstallationID != id || len(id) != 32 {
		t.Errorf("report = %+v", r)
	}
	if r.Client == nil || r.Client.Name != "claude-code" || r.Client.Version != "2.1.0" {
		t.Errorf("client = %+v", r.Client)
	}
	if r.PeriodEndedAt.Sub(r.PeriodStartedAt) != 40*time.Second {
		t.Errorf("period = %v to %v", r.PeriodStartedAt, r.PeriodEndedAt)
	}
	c.Flush(context.Background(), up)
	if len(up.sent) != 1 {
		t.Error("a flush with nothing new sent a report")
	}
	if c.Snapshot()[CounterChecks] != 2 {
		t.Error("the week's local counts are kept for the user to see")
	}
}

// A batch the service did not take is sent again with the same key, while new counts form the next batch; a refused one is dropped.
func TestRetriesWithTheSameKeyAndDropsRefused(t *testing.T) {
	c := open(t, nil)
	if err := c.SetMode(ModeOn); err != nil {
		t.Fatal(err)
	}
	c.Inc(CounterChecks)
	up := &fakeUploader{err: errors.New("no connection")}
	c.Flush(context.Background(), up)
	if c.LastUploadError() == nil {
		t.Error("a failed upload is reported in status")
	}
	c.Inc(CounterFeedback) // counted while the first batch waits
	up.err = nil
	c.Flush(context.Background(), up)
	c.Flush(context.Background(), up)
	if len(up.sent) != 2 || up.sent[0].report.Counters[CounterChecks] != 1 || up.sent[1].report.Counters[CounterFeedback] != 1 || up.sent[0].key == up.sent[1].key {
		t.Fatalf("sent = %+v", up.sent)
	}

	c.Inc(CounterChecks)
	up.err = refused{}
	c.Flush(context.Background(), up)
	up.err = nil
	c.Flush(context.Background(), up)
	if len(up.sent) != 2 {
		t.Error("a refused batch was sent again")
	}
}

func TestTurningOffDropsWhatWaits(t *testing.T) {
	c := open(t, nil)
	if err := c.SetMode(ModeOn); err != nil {
		t.Fatal(err)
	}
	c.Inc(CounterChecks)
	if err := c.SetMode(ModeLocal); err != nil {
		t.Fatal(err)
	}
	if err := c.SetMode(ModeOn); err != nil {
		t.Fatal(err)
	}
	up := &fakeUploader{}
	c.Flush(context.Background(), up)
	if len(up.sent) != 0 {
		t.Error("counts queued before uploading was turned off were sent")
	}
}

func TestResetInstallationID(t *testing.T) {
	c := open(t, nil)
	first, err := c.InstallationID()
	if err != nil || len(first) != 32 {
		t.Fatalf("id = %q, %v", first, err)
	}
	if again, _ := c.InstallationID(); again != first {
		t.Error("the id changed without a reset")
	}
	second, err := c.ResetInstallationID()
	if err != nil || second == first || len(second) != 32 {
		t.Errorf("reset = %q, %v", second, err)
	}
}

// rejectedCounters is the service refusing a report only for counters it does not know.
type rejectedCounters []string

func (r rejectedCounters) Error() string              { return "unknown counters" }
func (rejectedCounters) Permanent() bool              { return true }
func (r rejectedCounters) RejectedCounters() []string { return r }

// A service older than this program refuses a counter added since; the rest of the batch still goes, under a new key, and a batch with nothing else in it is dropped.
func TestSendsTheRestWhenACounterIsRefused(t *testing.T) {
	c := open(t, nil)
	if err := c.SetMode(ModeOn); err != nil {
		t.Fatal(err)
	}
	c.Inc(CounterChecks, CounterCrashes)
	up := &fakeUploader{err: rejectedCounters{CounterCrashes}}
	c.Flush(context.Background(), up)
	up.err = nil
	c.Flush(context.Background(), up)
	if len(up.sent) != 1 {
		t.Fatalf("sent = %+v", up.sent)
	}
	if got := up.sent[0].report.Counters; len(got) != 1 || got[CounterChecks] != 1 {
		t.Errorf("counters sent = %v", got)
	}

	c.Inc(CounterCrashes)
	up.err = rejectedCounters{CounterCrashes}
	c.Flush(context.Background(), up)
	up.err = nil
	c.Flush(context.Background(), up)
	if len(up.sent) != 1 {
		t.Errorf("a batch of only refused counters was sent again: %+v", up.sent)
	}
}

// The first key the refused batch carried is not reused: the service keeps its answer to it.
func TestRefusedCountersGetANewKey(t *testing.T) {
	c := open(t, nil)
	if err := c.SetMode(ModeOn); err != nil {
		t.Fatal(err)
	}
	c.Inc(CounterChecks, CounterCrashes)
	var keys []string
	up := uploaderFunc(func(_ Report, key string) error {
		keys = append(keys, key)
		if len(keys) == 1 {
			return rejectedCounters{CounterCrashes}
		}
		return nil
	})
	c.Flush(context.Background(), up)
	c.Flush(context.Background(), up)
	if len(keys) != 2 || keys[0] == keys[1] {
		t.Errorf("keys = %v", keys)
	}
}

type uploaderFunc func(Report, string) error

func (f uploaderFunc) UploadTelemetry(_ context.Context, r Report, key string) error {
	return f(r, key)
}
