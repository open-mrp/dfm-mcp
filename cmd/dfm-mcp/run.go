package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/open-mrp/dfm"

	"github.com/open-mrp/dfm-mcp/internal/client"
	"github.com/open-mrp/dfm-mcp/internal/localstore"
	"github.com/open-mrp/dfm-mcp/internal/mcpserver"
	"github.com/open-mrp/dfm-mcp/telemetry"
)

const defaultAPIURL = "https://dfm.openmrp.ai"

const usage = `dfm-mcp: the DFM check engine as an MCP server, over stdio, on this machine.

  dfm-mcp [--api-url URL]          serve MCP on stdin/stdout
  dfm-mcp telemetry                show the telemetry mode and this week's counts
  dfm-mcp telemetry on|local|off   send counts about once a minute while in use | count locally only (default) | count nothing
  dfm-mcp telemetry reset-id       replace the random installation id sent with counts
  dfm-mcp telemetry clear          forget this week's counts and anything waiting to be sent

Drawings never leave this machine unless the user shares one with a feedback report. Telemetry sends only the counts listed in the README, and only with "telemetry on"; DO_NOT_TRACK=1 turns it off.
`

// Run is the entry point, separate from main so a test can drive it.
func Run(ctx context.Context, args []string, getenv func(string) string, _ io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("dfm-mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	apiURL := fs.String("api-url", cmp.Or(getenv("DFM_API_URL"), defaultAPIURL), "the DFM API that records checks and takes feedback")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			// The usage was printed; asking for it is not a failure.
			return nil
		}
		return err
	}
	paths := newPaths(getenv)
	counters, err := telemetry.Open(paths.state, paths.config, "dfm-mcp", mcpserver.Version, dfm.EngineVersion, getenv)
	if err != nil {
		return err
	}

	if fs.NArg() > 0 {
		if fs.Arg(0) != "telemetry" {
			fmt.Fprint(stderr, usage)
			return fmt.Errorf("unknown command %q", fs.Arg(0))
		}
		return runTelemetry(fs.Args()[1:], counters, stdout)
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	// stdout is the protocol channel; logs go to stderr.
	logger := slog.New(slog.NewTextHandler(stderr, nil))

	store, err := localstore.Open(filepath.Join(paths.state, "checks"))
	if err != nil {
		return err
	}
	api := newAPI(*apiURL, paths.credentials, getenv, logger)
	local := &mcpserver.Local{Store: store, API: api, Counters: counters}

	// With uploading on, counts go out about once a minute while the server runs, and once more as it stops.
	flushed := make(chan struct{})
	flushCtx, stopFlush := context.WithCancel(context.WithoutCancel(ctx))
	go func() {
		defer close(flushed)
		counters.Run(flushCtx, api)
	}()
	err = mcpserver.New(mcpserver.Options{Local: local}).Run(ctx, &mcp.StdioTransport{})
	stopFlush()
	<-flushed
	return err
}

func runTelemetry(args []string, counters *telemetry.Counters, stdout io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stdout, counters.Status())
		return nil
	}
	switch args[0] {
	case "on", "local", "off":
		if err := counters.SetMode(telemetry.Mode(args[0])); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "telemetry %s\n", counters.Mode())
		if args[0] == "on" && counters.Mode() != telemetry.ModeOn {
			fmt.Fprintln(stdout, "DO_NOT_TRACK is set in the environment, so nothing will be uploaded while it is.")
		}
		return nil
	case "reset-id":
		id, err := counters.ResetInstallationID()
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "new installation id %s; later reports cannot be linked to earlier ones\n", id)
		return nil
	case "clear":
		if err := counters.Clear(); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "cleared")
		return nil
	default:
		return fmt.Errorf("telemetry: unknown argument %q (on, local, off, reset-id or clear)", args[0])
	}
}

// newAPI makes the API client. The key comes from the environment, as the authorization specification suggests for stdio: DFM_API_KEY, else the credentials file, else a key minted the first time one is needed and saved there.
func newAPI(apiURL, credentials string, getenv func(string) string, logger *slog.Logger) *mcpserver.API {
	c := &client.Client{BaseURL: apiURL, APIKey: getenv("DFM_API_KEY"), UserAgent: "dfm-mcp/" + mcpserver.Version}
	if c.APIKey == "" {
		c.APIKey = loadKey(credentials, apiURL)
	}
	api := &mcpserver.API{Client: c}
	api.Provision = func(ctx context.Context, c *client.Client) error {
		attempt, err := mintAttempt(credentials)
		if err != nil {
			return fmt.Errorf("preparing to mint a key in %s: %w", credentials, err)
		}
		key, err := api.CreateAPIKey(ctx, "dfm-mcp on "+hostname(), attempt)
		if err != nil {
			return fmt.Errorf("creating an API key: %w", err)
		}
		c.APIKey = key.Secret
		if err := saveKey(credentials, apiURL, key.Secret); err != nil {
			return fmt.Errorf("saving the API key to %s: %w", credentials, err)
		}
		logger.Info("created an API key for this machine", "id", key.APIKey.ID, "saved_to", credentials)
		return nil
	}
	return api
}

// paths are where the server keeps things, following the XDG base directories.
type paths struct {
	// config holds the credentials file and the telemetry mode.
	config string
	// state holds checks and telemetry counts.
	state       string
	credentials string
}

func newPaths(getenv func(string) string) paths {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	config := cmp.Or(getenv("XDG_CONFIG_HOME"), filepath.Join(home, ".config"))
	state := cmp.Or(getenv("XDG_STATE_HOME"), filepath.Join(home, ".local", "state"))
	p := paths{config: filepath.Join(config, "dfm"), state: filepath.Join(state, "dfm")}
	if d := getenv("DFM_STATE_DIR"); d != "" {
		p.state = d
	}
	p.credentials = cmp.Or(getenv("DFM_CREDENTIALS_FILE"), filepath.Join(p.config, "credentials.json"))
	return p
}

// credentials is the saved-key file: one key per API origin.
type credentials struct {
	Keys map[string]string `json:"keys"`
	// PendingMint is the idempotency key of a mint that was started and not yet saved: a mint interrupted by a crash or a timeout retries with the same one and gets the same key back, while a mint after a saved key starts fresh, so a key revoked or rotated in the meantime is never replayed.
	PendingMint string `json:"pending_mint,omitempty"`
}

// mintAttempt returns the idempotency key for the next mint: the one a previous attempt left unfinished, or a new random one, persisted before the request goes out so a retry after a lost response reuses it.
func mintAttempt(path string) (string, error) {
	var c credentials
	if data, err := os.ReadFile(path); err == nil { // #nosec G304 - the user's own credentials file
		_ = json.Unmarshal(data, &c)
	}
	if c.PendingMint != "" {
		return c.PendingMint, nil
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	c.PendingMint = hex.EncodeToString(random)
	if err := writeCredentials(path, c); err != nil {
		return "", err
	}
	return c.PendingMint, nil
}

func loadKey(path, apiURL string) string {
	data, err := os.ReadFile(path) // #nosec G304 - the user's own credentials file
	if err != nil {
		return ""
	}
	var c credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return ""
	}
	return c.Keys[apiURL]
}

func saveKey(path, apiURL, secret string) error {
	var c credentials
	if data, err := os.ReadFile(path); err == nil { // #nosec G304 - the user's own credentials file
		_ = json.Unmarshal(data, &c)
	}
	if c.Keys == nil {
		c.Keys = map[string]string{}
	}
	c.Keys[apiURL] = secret
	c.PendingMint = ""
	return writeCredentials(path, c)
}

func writeCredentials(path string, c credentials) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown host"
	}
	return h
}
