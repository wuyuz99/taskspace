package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A kind is set, shown, listed by and cleared, and only a task with one is
// written in the format that holds it.
func TestTaskKind(t *testing.T) {
	h := newHarness(t)
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-1", "--kind", " Review "); err != nil {
		t.Fatalf("create: %v\n%s", err, h.errb)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-2"); err != nil {
		t.Fatal(err)
	}
	if tk := h.load(t, "PAY-1"); tk.Kind != "Review" || tk.Version != 4 {
		t.Fatalf("kind %q, version %d", tk.Kind, tk.Version)
	}
	if v := h.load(t, "PAY-2").Version; v != 1 {
		t.Fatalf("a task without a kind is version %d", v)
	}

	if err := h.exec(t, h.app.newTaskCmd(), "list"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(strings.Fields(h.out.String()), " "); !strings.Contains(got, "PAY-1 Review -") || !strings.Contains(got, "PAY-2 - -") {
		t.Fatalf("list:\n%s", h.out)
	}
	for _, c := range []struct{ kind, want, not string }{
		{"review", "PAY-1", "PAY-2"},
		{"", "PAY-2", "PAY-1"},
		{"bug", "no task is of kind bug", "PAY"},
	} {
		if err := h.exec(t, h.app.newTaskCmd(), "list", "--kind", c.kind); err != nil {
			t.Fatal(err)
		}
		if out := h.out.String(); !strings.Contains(out, c.want) || strings.Contains(out, c.not) {
			t.Fatalf("list --kind %q:\n%s", c.kind, out)
		}
	}
	if err := h.exec(t, h.app.newTaskCmd(), "show", "PAY-1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "kind: Review") {
		t.Fatalf("show:\n%s", h.out)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "show", "PAY-1", "--json"); err != nil {
		t.Fatal(err)
	}
	var shown taskJSON
	if err := json.Unmarshal(h.out.Bytes(), &shown); err != nil || shown.Kind != "Review" {
		t.Fatalf("show --json: %v %+v", err, shown)
	}
	if err := h.exec(t, h.app.newStatusCmd(), "--json"); err != nil {
		t.Fatal(err)
	}
	var st []statusJSON
	if err := json.Unmarshal(h.out.Bytes(), &st); err != nil || len(st) != 2 || st[0].Kind != "Review" || st[1].Kind != "" {
		t.Fatalf("status --json: %v %+v", err, st)
	}

	if err := h.exec(t, h.app.newTaskCmd(), "update", "PAY-1", "--kind", "a\nb"); err == nil {
		t.Fatal("a kind of two lines was taken")
	}
	if err := h.exec(t, h.app.newTaskCmd(), "update", "PAY-1", "--kind", ""); err != nil {
		t.Fatal(err)
	}
	if tk := h.load(t, "PAY-1"); tk.Kind != "" || tk.Version != 1 {
		t.Fatalf("cleared: kind %q, version %d", tk.Kind, tk.Version)
	}
}

// dash keeps the tasks of a kind together, kinds in order and those without
// one last, and shows the kind before the title.
func TestDashGroupsByKind(t *testing.T) {
	h := newHarness(t)
	for _, c := range [][]string{
		{"A-1"},
		{"B-1", "--kind", "review", "--title", "Fix totals"},
		{"C-1", "--kind", "bug"},
		{"D-1", "--kind", "Review"},
	} {
		if err := h.exec(t, h.app.newTaskCmd(), append([]string{"create"}, c...)...); err != nil {
			t.Fatal(err)
		}
	}
	d := h.app.dashLoad(t.Context())
	var order []string
	for _, r := range d.Rows {
		if len(order) == 0 || order[len(order)-1] != r.Task {
			order = append(order, r.Task)
		}
	}
	if got := strings.Join(order, " "); got != "C-1 B-1 D-1 A-1" {
		t.Fatalf("order %s", got)
	}
	m := newDashModel(t.Context(), h.app, time.Second)
	m = drive(t, m, dashDataMsg(d))
	if v := m.View(); !strings.Contains(v, "[review] Fix totals") {
		t.Fatalf("view:\n%s", v)
	}
}
