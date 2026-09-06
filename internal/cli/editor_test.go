package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEditReloadsMetadataAndDetectsNoteConflict(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix editor command")
	}
	for _, conflict := range []bool{false, true} {
		h := newHarness(t)
		tk, err := h.app.Store.Create("t")
		if err != nil {
			t.Fatal(err)
		}
		tk.Title = "Updated elsewhere"
		if conflict {
			tk.Brief = "Another writer's note"
		}
		b, err := json.Marshal(tk)
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		record := filepath.Join(dir, "replacement.json")
		if err := os.WriteFile(record, b, 0600); err != nil {
			t.Fatal(err)
		}
		script := filepath.Join(dir, "editor")
		if err := os.WriteFile(script, []byte("#!/bin/sh\ncp \"$AW_TEST_RECORD\" \"$AW_TEST_TARGET\"\nprintf 'My draft' > \"$1\"\n"), 0755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("AW_TEST_RECORD", record)
		t.Setenv("AW_TEST_TARGET", filepath.Join(h.app.Store.TasksDir(), "t.json"))
		t.Setenv("VISUAL", "'"+script+"'")
		err = h.exec(t, h.app.newTaskCmd(), "brief", "t", "--edit")
		if conflict {
			if err == nil || !strings.Contains(err.Error(), "changed while") {
				t.Fatalf("conflict: %v", err)
			}
			_, draft, _ := strings.Cut(err.Error(), "draft kept at ")
			t.Cleanup(func() { os.Remove(draft) })
		} else if err != nil {
			t.Fatal(err)
		}
		latest, err := h.app.Store.Load("t")
		if err != nil {
			t.Fatal(err)
		}
		want := "My draft"
		if conflict {
			want = "Another writer's note"
		}
		if latest.Title != "Updated elsewhere" || latest.Brief != want {
			t.Fatalf("concurrent changes lost: %+v", latest)
		}
	}
}

func TestEditNotes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix editor command")
	}
	h := newHarness(t)
	if _, err := h.app.Store.Create("PAY-142"); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "editor with spaces")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '# Updated\\nNext step\\n' > \"$1\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL", "'"+script+"'")
	t.Setenv("EDITOR", "false") // VISUAL takes precedence.
	for _, kind := range []string{"brief", "handoff"} {
		if err := h.exec(t, h.app.newTaskCmd(), kind, "PAY-142", "--edit"); err != nil {
			t.Fatal(err)
		}
	}
	tk, err := h.app.Store.Load("PAY-142")
	if err != nil {
		t.Fatal(err)
	}
	if tk.Brief != "# Updated\nNext step\n" || tk.Handoff != tk.Brief || tk.HandoffUpdatedAt.IsZero() {
		t.Fatalf("edited task: %+v", tk)
	}
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "true")
	if err := h.exec(t, h.app.newTaskCmd(), "handoff", "PAY-142", "--edit"); err != nil {
		t.Fatal(err)
	}
	latest, err := h.app.Store.Load("PAY-142")
	if err != nil {
		t.Fatal(err)
	}
	if !latest.HandoffUpdatedAt.Equal(tk.HandoffUpdatedAt) {
		t.Fatal("unchanged note changed timestamp")
	}
	t.Setenv("EDITOR", "false")
	err = h.exec(t, h.app.newTaskCmd(), "brief", "PAY-142", "--edit")
	if err == nil || !strings.Contains(err.Error(), "draft kept at ") {
		t.Fatalf("failed editor: %v", err)
	}
	_, draft, _ := strings.Cut(err.Error(), "draft kept at ")
	t.Cleanup(func() { os.Remove(draft) })
	if b, err := os.ReadFile(draft); err != nil || string(b) != tk.Brief {
		t.Fatal("failed edit lost draft")
	}
}

func TestEditRejectsOtherInputs(t *testing.T) {
	h := newHarness(t)
	if _, err := h.app.Store.Create("t"); err != nil {
		t.Fatal(err)
	}
	for _, extra := range [][]string{{"text"}, {"--clear"}, {"--file", "missing"}} {
		args := append([]string{"brief", "t", "--edit"}, extra...)
		if err := h.exec(t, h.app.newTaskCmd(), args...); err == nil || !strings.Contains(err.Error(), "choose only one") {
			t.Fatalf("conflict accepted: %v", err)
		}
	}
}
