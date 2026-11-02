package exec

import (
	"os"
	"slices"
	"testing"
)

func TestRepositoryEnvDropsHookExecPath(t *testing.T) {
	sep := string(os.PathListSeparator)
	core, bin := "/opt/git/2.47/libexec/git-core", "/usr/bin"
	for _, c := range []struct {
		name     string
		env, out []string
	}{
		{"as git sets it for a hook",
			[]string{"GIT_EXEC_PATH=" + core, "PATH=" + core + sep + bin, "HOME=/h"},
			[]string{"PATH=" + bin, "HOME=/h"}},
		{"set by the user alone",
			[]string{"GIT_EXEC_PATH=" + core, "PATH=" + bin},
			[]string{"GIT_EXEC_PATH=" + core, "PATH=" + bin}},
		{"set by the user, later in PATH",
			[]string{"PATH=" + bin + sep + core, "GIT_EXEC_PATH=" + core},
			[]string{"PATH=" + bin + sep + core, "GIT_EXEC_PATH=" + core}},
		{"without PATH",
			[]string{"GIT_EXEC_PATH=" + core},
			[]string{"GIT_EXEC_PATH=" + core}},
	} {
		if got := RepositoryEnv(c.env); !slices.Equal(got, c.out) {
			t.Errorf("%s: %q, want %q", c.name, got, c.out)
		}
	}
}
