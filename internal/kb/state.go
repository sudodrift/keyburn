package kb

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// State is the local state file. It holds no secrets and no key values:
// the first day each key id was seen, and which alerts were already sent.
type State struct {
	Tool      string            `json:"tool"`
	Updated   string            `json:"updated"`
	FirstSeen map[string]string `json:"first_seen"`
	Sent      map[string]string `json:"sent"`
}

func newState() *State {
	return &State{Tool: "keyburn", FirstSeen: map[string]string{}, Sent: map[string]string{}}
}

// LoadState reads the state file; a missing file gives an empty state.
func LoadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return newState(), nil
	}
	if err != nil {
		return nil, err
	}
	st := newState()
	if err := json.Unmarshal(data, st); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	if st.FirstSeen == nil {
		st.FirstSeen = map[string]string{}
	}
	if st.Sent == nil {
		st.Sent = map[string]string{}
	}
	return st, nil
}

// Save writes the state atomically with mode 0600.
func (st *State) Save(path string, now time.Time) error {
	// Forget sent alerts after 40 days; their day can no longer come back.
	cut := now.AddDate(0, 0, -40)
	for id, ts := range st.Sent {
		if t, err := time.Parse(time.RFC3339, ts); err == nil && t.Before(cut) {
			delete(st.Sent, id)
		}
	}
	st.Updated = now.UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
