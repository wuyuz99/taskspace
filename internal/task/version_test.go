package task

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// legacyRecord is a task as tspace wrote it before records had a version:
// every field it could carry, the brief and handoff inline.
const legacyRecord = `{
  "name": "t",
  "repos": {
    "backend": {
      "path": "/src/backend",
      "branch": "agent/t",
      "base": "origin/main",
      "worktree": "/state/worktrees/t/backend",
      "post_create": "make setup",
      "agent": ["codex", "--model", "o4"],
      "resume": ["codex", "resume", "--last"]
    },
    "frontend": {
      "path": "/src/frontend",
      "branch": "feature/x",
      "worktree": "/state/worktrees/t/frontend"
    }
  },
  "title": "Expired cards",
  "ticket": "PAY-142",
  "ticket_url": "https://example.com/PAY-142",
  "agent": ["claude", "--add-dir", "{context_dir}"],
  "resume": ["claude", "--continue"],
  "agent_repo": "backend",
  "root_window": true,
  "brief": "Reject expired cards.\n",
  "handoff": "Frontend next.\n",
  "handoff_updated_at": "2026-09-01T10:30:00Z"
}
`

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

func checkLegacyFields(t *testing.T, got *Task) {
	t.Helper()
	want := Repo{Path: "/src/backend", Branch: "agent/t", Base: "origin/main", Worktree: "/state/worktrees/t/backend",
		PostCreate: "make setup", Agent: []string{"codex", "--model", "o4"}, Resume: []string{"codex", "resume", "--last"}}
	b := got.Repos["backend"]
	if b.Path != want.Path || b.Branch != want.Branch || b.Base != want.Base || b.Worktree != want.Worktree ||
		b.PostCreate != want.PostCreate || !slices.Equal(b.Agent, want.Agent) || !slices.Equal(b.Resume, want.Resume) {
		t.Errorf("backend = %+v", b)
	}
	if f := got.Repos["frontend"]; f.Branch != "feature/x" || f.Worktree != "/state/worktrees/t/frontend" {
		t.Errorf("frontend = %+v", f)
	}
	if got.Title != "Expired cards" || got.Ticket != "PAY-142" || got.TicketURL != "https://example.com/PAY-142" {
		t.Errorf("title/ticket = %q %q %q", got.Title, got.Ticket, got.TicketURL)
	}
	if !slices.Equal(got.Agent, []string{"claude", "--add-dir", "{context_dir}"}) || !slices.Equal(got.Resume, []string{"claude", "--continue"}) {
		t.Errorf("agent %v, resume %v", got.Agent, got.Resume)
	}
	if got.AgentRepo != "backend" || !got.RootWindow {
		t.Errorf("layout: agent_repo %q, root_window %v", got.AgentRepo, got.RootWindow)
	}
}

func TestLoadReadsALegacyRecordWithoutRewritingIt(t *testing.T) {
	s := newTestStore(t)
	writeRecord(t, s, "t", legacyRecord)
	got, err := s.Load("t")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 0 || !got.Outdated() || got.LegacyBrief == "" {
		t.Fatalf("legacy record read as %+v", got)
	}
	checkLegacyFields(t, got)
	if _, err := s.List(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rawRecord(t, s, "t"), []byte(legacyRecord)) {
		t.Fatal("reading rewrote the record")
	}
}

func TestUpgradeKeepsEverythingAndMovesTheNotes(t *testing.T) {
	s := newTestStore(t)
	writeRecord(t, s, "t", legacyRecord)
	got, err := upgradeRecord(s, "t")
	if err != nil {
		t.Fatal(err)
	}
	checkLegacyFields(t, got)
	reloaded, err := s.Load("t")
	if err != nil {
		t.Fatal(err)
	}
	checkLegacyFields(t, reloaded)
	if reloaded.Version != 1 || reloaded.Outdated() {
		t.Errorf("version %d after upgrade", reloaded.Version)
	}
	raw := string(rawRecord(t, s, "t"))
	for _, field := range []string{`"brief"`, `"handoff"`, `"handoff_updated_at"`} {
		if strings.Contains(raw, field) {
			t.Errorf("record still carries %s", field)
		}
	}
	brief, _ := s.ReadNote("t", BriefFile)
	handoff, _ := s.ReadNote("t", HandoffFile)
	if brief.Text != "Reject expired cards.\n" || handoff.Text != "Frontend next.\n" {
		t.Fatalf("notes %q / %q", brief.Text, handoff.Text)
	}
	if want := time.Date(2026, 9, 1, 10, 30, 0, 0, time.UTC); !handoff.UpdatedAt.Equal(want) {
		t.Errorf("handoff time %v, want %v", handoff.UpdatedAt, want)
	}

	// Again, nothing changes.
	before := rawRecord(t, s, "t")
	if _, err := upgradeRecord(s, "t"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, rawRecord(t, s, "t")) {
		t.Error("a second upgrade changed the record")
	}
	if again, _ := s.ReadNote("t", HandoffFile); again.Text != handoff.Text || !again.UpdatedAt.Equal(handoff.UpdatedAt) {
		t.Errorf("a second upgrade changed the handoff: %+v", again)
	}
}

// A note file with text is newer than the inline copy; an empty one holds no
// note and is filled.
func TestUpgradeKeepsExistingNoteFiles(t *testing.T) {
	s := newTestStore(t)
	writeRecord(t, s, "t", legacyRecord)
	if err := s.WriteNote("t", BriefFile, "current brief\n"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.NotePath("t", HandoffFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := upgradeRecord(s, "t"); err != nil {
		t.Fatal(err)
	}
	brief, _ := s.ReadNote("t", BriefFile)
	handoff, _ := s.ReadNote("t", HandoffFile)
	if brief.Text != "current brief\n" {
		t.Errorf("the inline brief replaced the file: %q", brief.Text)
	}
	if handoff.Text != "Frontend next.\n" {
		t.Errorf("an empty handoff file was not filled: %q", handoff.Text)
	}
}

// An upgrade that fails leaves the record as it was, and a retry finishes it.
func TestFailedUpgradeLeavesTheRecordForARetry(t *testing.T) {
	s := newTestStore(t)
	writeRecord(t, s, "t", legacyRecord)
	// A file where the notes directory goes: no note can be written.
	if err := os.MkdirAll(s.TaskDir("t"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.ContextDir("t"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := upgradeRecord(s, "t"); err == nil {
		t.Fatal("upgrade succeeded without writing the notes")
	}
	if !bytes.Equal(rawRecord(t, s, "t"), []byte(legacyRecord)) {
		t.Fatal("a failed upgrade changed the record")
	}
	if err := os.Remove(s.ContextDir("t")); err != nil {
		t.Fatal(err)
	}
	if _, err := upgradeRecord(s, "t"); err != nil {
		t.Fatal(err)
	}
	if brief, _ := s.ReadNote("t", BriefFile); brief.Text != "Reject expired cards.\n" {
		t.Fatalf("brief %q after retry", brief.Text)
	}
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
	if v := version(); v != CurrentVersion {
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

// An upgrade and an update of one task at once both land.
func TestUpgradeAndUpdateTogether(t *testing.T) {
	s := newTestStore(t)
	writeRecord(t, s, "t", legacyRecord)
	path, release := releaser(t)
	up := startHelper(t, s, "upgrade-hold", path)
	up.waitReady(t)
	upd := startHelper(t, s, "update-ticket", path)
	upd.waitBlocked(t)
	release()
	if code := up.exit(t); code != 0 {
		t.Fatalf("upgrade exited %d: %s", code, up.errText())
	}
	if code := upd.exit(t); code != 0 {
		t.Fatalf("update exited %d: %s", code, upd.errText())
	}
	got, err := s.Load("t")
	if err != nil {
		t.Fatal(err)
	}
	if got.Ticket != "from B" || got.Outdated() || got.LegacyBrief != "" || got.Title != "Expired cards" {
		t.Fatalf("after both: %+v", got)
	}
	if brief, _ := s.ReadNote("t", BriefFile); brief.Text != "Reject expired cards.\n" {
		t.Fatalf("brief %q", brief.Text)
	}
}

// Notes shows what an upgrade would leave, without writing anything.
func TestNotesOfALegacyRecord(t *testing.T) {
	s := newTestStore(t)
	writeRecord(t, s, "t", legacyRecord)
	if err := s.WriteNote("t", BriefFile, "file brief\n"); err != nil {
		t.Fatal(err)
	}
	tk, err := s.Load("t")
	if err != nil {
		t.Fatal(err)
	}
	brief, handoff, err := s.Notes(tk)
	if err != nil {
		t.Fatal(err)
	}
	if brief.Text != "file brief\n" || handoff.Text != "Frontend next.\n" ||
		!handoff.UpdatedAt.Equal(time.Date(2026, 9, 1, 10, 30, 0, 0, time.UTC)) {
		t.Fatalf("notes %+v / %+v", brief, handoff)
	}
	if _, err := os.Stat(s.NotePath("t", HandoffFile)); !os.IsNotExist(err) {
		t.Fatalf("Notes wrote the handoff: %v", err)
	}
}
