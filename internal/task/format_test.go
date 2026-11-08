package task

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func jsonFields(v any) []string {
	var out []string
	rt := reflect.TypeOf(v)
	for i := range rt.NumField() {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		out = append(out, rt.Name()+"."+name)
	}
	return out
}

// A new field an older tspace would drop on saving needs a format version, so
// that the older tspace refuses the record instead. Decide that in
// formatVersion, as for windows and base_assumed, then add the field here.
func TestRecordFieldsAreDecided(t *testing.T) {
	got := slices.Concat(jsonFields(Task{}), jsonFields(Repo{}), jsonFields(Window{}))
	want := []string{
		"Task.version", "Task.name", "Task.repos", "Task.title", "Task.ticket", "Task.ticket_url",
		"Task.agent", "Task.resume", "Task.agent_repo", "Task.root_window", "Task.windows",
		"Repo.path", "Repo.branch", "Repo.base", "Repo.base_assumed", "Repo.worktree",
		"Repo.post_create", "Repo.agent", "Repo.resume", "Repo.reference",
		"Window.name", "Window.repo", "Window.command",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("record fields changed; see docs/FORMATS.md\n got %q\nwant %q", got, want)
	}
}

// Every format ever written still loads, and comes back as written.
func TestRecordFixturesRoundTrip(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "record-v*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("fixtures: %v, %v", files, err)
	}
	for _, f := range files {
		want, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s := NewStore(t.TempDir())
		if err := os.MkdirAll(s.TasksDir(), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(s.path("PAY-142"), want, 0o600); err != nil {
			t.Fatal(err)
		}
		tk, err := s.Load("PAY-142")
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if err := s.Save(tk); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		got, err := os.ReadFile(s.path("PAY-142"))
		if err != nil {
			t.Fatal(err)
		}
		var a, b any
		if json.Unmarshal(want, &a) != nil || json.Unmarshal(got, &b) != nil {
			t.Fatalf("%s: not JSON", f)
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("%s changed on saving:\n%s", f, got)
		}
	}
}
