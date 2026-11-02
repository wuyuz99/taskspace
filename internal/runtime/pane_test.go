package runtime

import "testing"

// Callers hold the name tspace knows a window by; the escaping is Pane's.
func TestPaneTakesTheDisplayName(t *testing.T) {
	st := SessionState{Panes: map[string]PaneState{WindowName("api.v1"): {Command: "go"}}}
	if p, ok := st.Pane("api.v1"); !ok || p.Command != "go" {
		t.Fatalf("Pane = %+v, %v", p, ok)
	}
	if _, ok := st.Pane("api-v1"); ok {
		t.Fatal("found a window that is not there")
	}
}
