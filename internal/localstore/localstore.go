// Package localstore keeps the checks a local MCP server ran, one JSON file each, so a result can be read back, linked to its recorded copy on the server, and reported on with the drawing it came from.
package localstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/open-mrp/dfm-mcp/internal/resource"
)

// Entry is one local check: the result as the API would present it, the engine document it ran with, where the drawing was, and the server's id once the check has been recorded there.
type Entry struct {
	Check resource.DFMCheck `json:"check"`
	// Request is the engine's check-request document, what the server needs to record the check.
	Request json.RawMessage `json:"request"`
	// DesignPath is where the drawing was read from, so a feedback report can offer it again; it may have moved since.
	DesignPath string `json:"design_path,omitempty"`
	// RemoteID is the check's id on the server once recorded there, or empty.
	RemoteID string `json:"remote_id,omitempty"`
}

// Store is a directory of entries.
type Store struct {
	dir string
}

// idPattern is what an id may look like before it becomes a file name.
var idPattern = regexp.MustCompile(`^[a-z]+_[a-z0-9]+$`)

// DefaultDir is where a user's checks live: $XDG_STATE_HOME/dfm/checks, else ~/.local/state/dfm/checks.
func DefaultDir(getenv func(string) string) string {
	if base := getenv("XDG_STATE_HOME"); base != "" {
		return filepath.Join(base, "dfm", "checks")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "dfm", "checks")
	}
	return filepath.Join(home, ".local", "state", "dfm", "checks")
}

// Open makes the directory, readable by the user alone, and returns the store.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("localstore: %w", err)
	}
	return &Store{dir: dir}, nil
}

func (s *Store) path(id string) (string, error) {
	if !idPattern.MatchString(id) {
		return "", fmt.Errorf("localstore: %q is not a check id", id)
	}
	return filepath.Join(s.dir, id+".json"), nil
}

// Save writes an entry under its check's id, and under its remote id too once it has one, so either id finds it.
func (s *Store) Save(e *Entry) error {
	ids := []string{e.Check.ID}
	if e.RemoteID != "" && e.RemoteID != e.Check.ID {
		ids = append(ids, e.RemoteID)
	}
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("localstore: %w", err)
	}
	for _, id := range ids {
		p, err := s.path(id)
		if err != nil {
			return err
		}
		if err := os.WriteFile(p, data, 0o600); err != nil {
			return fmt.Errorf("localstore: %w", err)
		}
	}
	return nil
}

// Load reads an entry by either of its ids; ok is false when there is none.
func (s *Store) Load(id string) (*Entry, bool, error) {
	p, err := s.path(id)
	if err != nil {
		return nil, false, nil
	}
	data, err := os.ReadFile(p) // #nosec G304 - the path is the store's directory plus a validated id
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("localstore: %w", err)
	}
	var e Entry
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, false, fmt.Errorf("localstore: %s: %w", p, err)
	}
	return &e, true, nil
}
