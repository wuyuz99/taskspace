package task

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// upgradeRecord brings a record to the current format, as any change does.
func upgradeRecord(s *Store, name string) (*Task, error) {
	return s.Update(context.Background(), name, "test", func(*Task) error { return nil })
}

func writeRecord(t *testing.T, s *Store, name, raw string) {
	t.Helper()
	if err := os.MkdirAll(s.TasksDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.path(name), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}

func rawRecord(t *testing.T, s *Store, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(s.path(name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A record is written in the oldest format that holds it: version 2 only
// with saved windows, which a version 1 tspace would drop.
func TestRecordsCarryTheVersionTheyNeed(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create("t"); err != nil {
		t.Fatal(err)
	}
	version := func() int {
		var raw struct {
			Version int `json:"version"`
		}
		if err := json.Unmarshal(rawRecord(t, s, "t"), &raw); err != nil {
			t.Fatal(err)
		}
		return raw.Version
	}
	if v := version(); v != 1 {
		t.Fatalf("new record has version %d", v)
	}
	tk, err := s.Load("t")
	if err != nil {
		t.Fatal(err)
	}
	tk.Title, tk.Agent = "kept", []string{"a", "b"}
	tk.SetRepo("r", Repo{Path: "/p", Branch: "b", Worktree: "/w", Resume: []string{"a", "-c"}})
	tk.Windows = []Window{{Name: "git", Repo: "r", Command: []string{"lazygit"}}}
	if err := s.Save(tk); err != nil {
		t.Fatal(err)
	}
	if v := version(); v != 2 {
		t.Fatalf("record with windows has version %d", v)
	}
	back, err := s.Load("t")
	if err != nil {
		t.Fatal(err)
	}
	if back.Title != "kept" || !slices.Equal(back.Agent, tk.Agent) || !slices.Equal(back.Repos["r"].Resume, []string{"a", "-c"}) ||
		len(back.Windows) != 1 || back.Windows[0].Repo != "r" {
		t.Fatalf("round trip: %+v", back)
	}
	// Without its windows again, older tspace can read it again.
	back.Windows = nil
	if err := s.Save(back); err != nil {
		t.Fatal(err)
	}
	if v := version(); v != 1 {
		t.Fatalf("record without windows has version %d", v)
	}
}

// A record from a newer tspace is refused, untouched, and listing still
// shows the rest.
func TestNewerAndMalformedRecordsAreLeftAlone(t *testing.T) {
	s := newTestStore(t)
	future := `{"version": 99, "name": "new", "shiny": true}` + "\n"
	writeRecord(t, s, "new", future)
	writeRecord(t, s, "negative", `{"version": -1, "name": "negative"}`)
	writeRecord(t, s, "broken", "{not json\n")
	if _, err := s.Create("ok"); err != nil {
		t.Fatal(err)
	}

	_, err := s.Load("new")
	if !errors.Is(err, ErrUnsupportedVersion) || !strings.Contains(err.Error(), "upgrade tspace") {
		t.Fatalf("Load of a newer record = %v", err)
	}
	if _, err := upgradeRecord(s, "new"); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("Upgrade of a newer record = %v", err)
	}
	if _, err := upgradeRecord(s, "negative"); err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Fatalf("Upgrade of a negative version = %v", err)
	}
	if _, err := s.Load("broken"); err == nil || !strings.Contains(err.Error(), filepath.Base(s.path("broken"))) {
		t.Fatalf("Load of a malformed record = %v", err)
	}
	tasks, err := s.List()
	var skipped *SkippedError
	if !errors.As(err, &skipped) || len(skipped.Errs) != 3 || len(tasks) != 1 || tasks[0].Name != "ok" {
		t.Fatalf("List = %v, %v", tasks, err)
	}
	if string(rawRecord(t, s, "new")) != future || string(rawRecord(t, s, "broken")) != "{not json\n" {
		t.Fatal("an unreadable record was changed")
	}
}

// A record from before versions holds its notes inline. Reading it as it is
// would lose them on the next save, so it is refused, saying what to do.
func TestRecordWithoutAVersionIsRefused(t *testing.T) {
	s := newTestStore(t)
	raw := `{"name": "old", "brief": "kept"}` + "\n"
	writeRecord(t, s, "old", raw)
	_, err := s.Load("old")
	if !errors.Is(err, ErrObsoleteRecord) || !strings.Contains(err.Error(), "v0.3") {
		t.Fatalf("Load = %v", err)
	}
	if _, err := upgradeRecord(s, "old"); !errors.Is(err, ErrObsoleteRecord) {
		t.Fatalf("Update = %v", err)
	}
	if string(rawRecord(t, s, "old")) != raw {
		t.Fatal("the record was changed")
	}
}
