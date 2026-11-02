package task

import "testing"

func TestKindOf(t *testing.T) {
	for name, want := range map[string]WindowKind{
		RootWindowName: RootWindow,
		"api.v1":       RepoWindow,
		"_mon":         ExtraWindow,
		"_":            ExtraWindow,
	} {
		if got := KindOf(name); got != want {
			t.Errorf("KindOf(%q) = %v, want %v", name, got, want)
		}
	}
}
