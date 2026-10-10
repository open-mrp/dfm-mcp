package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Mode is what the user allowed.
type Mode string

const (
	// ModeLocal counts on this machine and uploads nothing. The default.
	ModeLocal Mode = "local"
	// ModeOn counts and uploads what it counted about once a minute while the server is in use.
	ModeOn Mode = "on"
	// ModeOff counts nothing.
	ModeOff Mode = "off"
)

// FlushInterval is how often counts are sent while uploading is on: the batching that keeps usage live without a request per event.
const FlushInterval = time.Minute

// maxBatchAge bounds how long counts that could not be sent are kept: the service takes a period of at most seven days, and a batch older than that is dropped rather than accumulated.
const maxBatchAge = 7 * 24 * time.Hour

// Client names the MCP client (the agent) the server was working for, as it named itself when it connected.
type Client struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// Report is one upload: the counts of one period.
type Report struct {
	InstallationID  string           `json:"installation_id"`
	Program         string           `json:"program"`
	Version         string           `json:"version"`
	EngineVersion   string           `json:"engine_version"`
	OS              string           `json:"os"`
	Arch            string           `json:"arch"`
	Client          *Client          `json:"client,omitempty"`
	PeriodStartedAt time.Time        `json:"period_started_at"`
	PeriodEndedAt   time.Time        `json:"period_ended_at"`
	Counters        map[string]int64 `json:"counters"`
}

// Uploader sends a report. idempotencyKey is the batch's own, the same on every retry of it, so the service counts a batch once however often it is sent.
type Uploader interface {
	UploadTelemetry(ctx context.Context, r Report, idempotencyKey string) error
}

// Permanent is implemented by an upload error that will not go away by retrying (the service refused the report), so the batch is dropped instead of sent again forever.
type Permanent interface {
	Permanent() bool
}

// batch is counts waiting to be sent, with the key their upload carries.
type batch struct {
	Key       string           `json:"key"`
	StartedAt time.Time        `json:"started_at"`
	EndedAt   time.Time        `json:"ended_at,omitzero"`
	Client    *Client          `json:"client,omitempty"`
	Counters  map[string]int64 `json:"counters"`
}

// state is the counters file. Week is this week's counts for the user to see; Pending fills while uploading is on; Inflight is a batch taken from Pending to send, kept until the service has it.
type state struct {
	WeekStartsOn string           `json:"week_starts_on"`
	Week         map[string]int64 `json:"counters"`
	Pending      *batch           `json:"pending,omitempty"`
	Inflight     *batch           `json:"inflight,omitempty"`
}

// Counters is the local store of counts for one program.
type Counters struct {
	mu        sync.Mutex
	dir       string
	modeFile  string
	idFile    string
	program   string
	version   string
	engine    string
	disabled  bool
	now       func() time.Time
	st        state
	client    *Client
	uploadErr error
}

// Open loads the counters under stateDir, reading the mode and the installation id from configDir. DO_NOT_TRACK in the environment turns everything off, whatever the mode file says.
func Open(stateDir, configDir, program, version, engineVersion string, getenv func(string) string) (*Counters, error) {
	c := &Counters{
		dir: filepath.Join(stateDir, "telemetry"), modeFile: filepath.Join(configDir, "telemetry"), idFile: filepath.Join(configDir, "telemetry-id"),
		program: program, version: version, engine: engineVersion, now: time.Now,
	}
	if v := strings.TrimSpace(getenv("DO_NOT_TRACK")); v != "" && v != "0" && !strings.EqualFold(v, "false") {
		c.disabled = true
	}
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return nil, fmt.Errorf("telemetry: %w", err)
	}
	if err := c.load(); err != nil {
		return nil, err
	}
	return c, nil
}

// Mode reads the user's choice; local when none was made.
func (c *Counters) Mode() Mode {
	if c.disabled {
		return ModeOff
	}
	data, err := os.ReadFile(c.modeFile) // #nosec G304 - the user's own config file
	if err != nil {
		return ModeLocal
	}
	switch m := Mode(strings.TrimSpace(string(data))); m {
	case ModeOn, ModeOff, ModeLocal:
		return m
	}
	return ModeLocal
}

// SetMode records the user's choice. Turning uploading on makes the installation id if there is none; leaving it drops whatever was waiting to be sent, so nothing counted before the choice is uploaded after it.
func (c *Counters) SetMode(m Mode) error {
	switch m {
	case ModeOn, ModeOff, ModeLocal:
	default:
		return fmt.Errorf("telemetry: mode must be on, local or off")
	}
	if err := os.MkdirAll(filepath.Dir(c.modeFile), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(c.modeFile, []byte(string(m)+"\n"), 0o600); err != nil {
		return err
	}
	if m == ModeOn {
		_, err := c.InstallationID()
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.st.Pending, c.st.Inflight = nil, nil
	return c.save()
}

// InstallationID is this installation's random id, made the first time it is needed. It is 32 hex characters from the system's random source, unrelated to any account, API key, user or machine name.
func (c *Counters) InstallationID() (string, error) {
	if data, err := os.ReadFile(c.idFile); err == nil { // #nosec G304 - the user's own config file
		if id := strings.TrimSpace(string(data)); len(id) == 32 {
			return id, nil
		}
	}
	return c.ResetInstallationID()
}

// ResetInstallationID replaces the installation id with a new random one, so later reports cannot be linked to earlier ones.
func (c *Counters) ResetInstallationID() (string, error) {
	id := newKey()
	if err := os.MkdirAll(filepath.Dir(c.idFile), 0o700); err != nil {
		return "", err
	}
	return id, os.WriteFile(c.idFile, []byte(id+"\n"), 0o600)
}

// SetClient records the MCP client this process serves, as it named itself when it connected; reports carry it so usage can be split by agent.
func (c *Counters) SetClient(name, version string) {
	if c == nil || name == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.client = &Client{Name: truncate(name, 100), Version: truncate(version, 60)}
}

// Inc adds one to each counter: to this week's counts always, and to the batch waiting to be sent when uploading is on. Nothing is recorded when the mode is off. A name the service would not accept is a programming error and is dropped.
func (c *Counters) Inc(names ...string) {
	if c == nil {
		return
	}
	mode := c.Mode()
	if mode == ModeOff {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rollWeek()
	for _, name := range names {
		if !Allowed(name) {
			continue
		}
		c.st.Week[name]++
		if mode == ModeOn {
			if c.st.Pending == nil {
				c.st.Pending = &batch{Key: newKey(), StartedAt: c.now().UTC(), Client: c.client, Counters: map[string]int64{}}
			}
			c.st.Pending.Counters[name]++
		}
	}
	_ = c.save()
}

// Flush sends what has been counted since the last upload, when uploading is on. A batch the service did not take (no connection, an outage) is kept and sent again with the same key on the next flush, while new counts collect in the next batch; a batch the service refused, or one older than seven days, is dropped. It never fails the caller.
func (c *Counters) Flush(ctx context.Context, up Uploader) {
	if c == nil || up == nil || c.Mode() != ModeOn {
		return
	}
	id, err := c.InstallationID()
	if err != nil {
		c.setErr(err)
		return
	}
	c.mu.Lock()
	if c.st.Inflight == nil && c.st.Pending != nil && len(c.st.Pending.Counters) > 0 {
		c.st.Inflight, c.st.Pending = c.st.Pending, nil
		c.st.Inflight.EndedAt = c.now().UTC()
		_ = c.save()
	}
	b := c.st.Inflight
	if b != nil && c.now().Sub(b.StartedAt) > maxBatchAge {
		c.st.Inflight, b = nil, nil
		_ = c.save()
	}
	c.mu.Unlock()
	if b == nil {
		return
	}
	report := Report{
		InstallationID: id, Program: c.program, Version: c.version, EngineVersion: c.engine, OS: runtime.GOOS, Arch: runtime.GOARCH,
		Client: b.Client, PeriodStartedAt: b.StartedAt, PeriodEndedAt: b.EndedAt, Counters: b.Counters,
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	err = up.UploadTelemetry(ctx, report, "telemetry:"+b.Key)
	c.mu.Lock()
	defer c.mu.Unlock()
	var permanent Permanent
	switch {
	case err == nil:
		c.uploadErr = nil
		c.st.Inflight = nil
	case errors.As(err, &permanent) && permanent.Permanent():
		c.uploadErr = fmt.Errorf("the service refused a report, which was dropped: %w", err)
		c.st.Inflight = nil
	default:
		c.uploadErr = err
	}
	_ = c.save()
}

// Run flushes every FlushInterval until ctx ends, and once more as it ends, so the last minute's counts are not lost when the server stops.
func (c *Counters) Run(ctx context.Context, up Uploader) {
	ticker := time.NewTicker(FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			final, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			// Twice: a batch that waited behind an unsent one goes too.
			c.Flush(final, up)
			c.Flush(final, up)
			cancel()
			return
		case <-ticker.C:
			c.Flush(ctx, up)
		}
	}
}

// Snapshot is this week's counts, for the user to look at.
func (c *Counters) Snapshot() map[string]int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rollWeek()
	out := make(map[string]int64, len(c.st.Week))
	for k, v := range c.st.Week {
		out[k] = v
	}
	return out
}

// LastUploadError is why the last upload failed, or nil.
func (c *Counters) LastUploadError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.uploadErr
}

func (c *Counters) setErr(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.uploadErr = err
}

// Clear forgets the local counts and anything waiting to be sent.
func (c *Counters) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.st = state{WeekStartsOn: weekStart(c.now()), Week: map[string]int64{}}
	return c.save()
}

// rollWeek starts a new week's local counts when the week has turned.
func (c *Counters) rollWeek() {
	ws := weekStart(c.now())
	if c.st.WeekStartsOn != ws {
		c.st.WeekStartsOn = ws
		c.st.Week = map[string]int64{}
	}
}

func weekStart(t time.Time) string {
	t = t.UTC()
	t = t.AddDate(0, 0, -int(t.Weekday()))
	return t.Format("2006-01-02")
}

// newKey is 32 hex characters from the system's random source.
func newKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (c *Counters) path() string { return filepath.Join(c.dir, "counters.json") }

func (c *Counters) load() error {
	data, err := os.ReadFile(c.path()) // #nosec G304 - the store's own file
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c.st = state{WeekStartsOn: weekStart(c.now()), Week: map[string]int64{}}
			return nil
		}
		return fmt.Errorf("telemetry: %w", err)
	}
	if err := json.Unmarshal(data, &c.st); err != nil {
		// A damaged file is dropped rather than wedging the server.
		c.st = state{WeekStartsOn: weekStart(c.now()), Week: map[string]int64{}}
	}
	if c.st.Week == nil {
		c.st.Week = map[string]int64{}
	}
	return nil
}

func (c *Counters) save() error {
	data, err := json.MarshalIndent(c.st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.path(), data, 0o600)
}

// Status is what `dfm-mcp telemetry` prints.
func (c *Counters) Status() string {
	var b strings.Builder
	mode := c.Mode()
	fmt.Fprintf(&b, "mode: %s", mode)
	if c.disabled {
		b.WriteString(" (DO_NOT_TRACK is set)")
	}
	if mode == ModeOn {
		if id, err := c.InstallationID(); err == nil {
			fmt.Fprintf(&b, "\ninstallation id: %s (dfm-mcp telemetry reset-id replaces it)", id)
		}
		fmt.Fprintf(&b, "\nsends counts about every %s while in use", FlushInterval)
	}
	week := c.Snapshot()
	c.mu.Lock()
	weekStarts := c.st.WeekStartsOn
	waiting := int64(0)
	for _, bt := range []*batch{c.st.Pending, c.st.Inflight} {
		if bt != nil {
			for _, n := range bt.Counters {
				waiting += n
			}
		}
	}
	c.mu.Unlock()
	fmt.Fprintf(&b, "\nweek starting %s, %s %s, engine %s, %s/%s\n", weekStarts, c.program, c.version, c.engine, runtime.GOOS, runtime.GOARCH)
	if len(week) == 0 {
		b.WriteString("no counts yet\n")
	}
	names := make([]string, 0, len(week))
	for name := range week {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, "  %-28s %d\n", name, week[name])
	}
	if waiting > 0 {
		fmt.Fprintf(&b, "%d counts waiting to be sent\n", waiting)
	}
	if err := c.LastUploadError(); err != nil {
		fmt.Fprintf(&b, "last upload failed: %v\n", err)
	}
	return b.String()
}
