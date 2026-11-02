package exec

import (
	"context"
	"os"
	"runtime"
	"slices"
	"strings"
)

// RepositoryEnv clears Git's repository-local context (git rev-parse
// --local-env-vars), as required when a hook operates on another repository.
// SSH/askpass settings and global/system config file locations survive.
// GIT_CONFIG_COUNT and GIT_CONFIG_PARAMETERS are dropped, including any
// authentication supplied through them; see issue #132. Hook editor, author
// and quarantine context are also cleared, even when the user exported them:
// nothing distinguishes the two, and a persistent server would keep them.
func RepositoryEnv(env []string) []string {
	out := make([]string, 0, len(env))
	keys := RepositoryEnvVars()
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if runtime.GOOS == "windows" {
			key = strings.ToUpper(key)
		}
		if slices.Contains(keys, key) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// RepositoryEnvVars returns Git's repository-local and hook-context variables.
// Launchers must unset these even when only a persistent server has them.
func RepositoryEnvVars() []string {
	return []string{
		"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS",
		"GIT_CONFIG_COUNT", "GIT_OBJECT_DIRECTORY", "GIT_DIR", "GIT_WORK_TREE",
		"GIT_IMPLICIT_WORK_TREE", "GIT_GRAFT_FILE", "GIT_INDEX_FILE",
		"GIT_NO_REPLACE_OBJECTS", "GIT_REPLACE_REF_BASE", "GIT_PREFIX",
		"GIT_SHALLOW_FILE", "GIT_COMMON_DIR", "GIT_EDITOR", "GIT_SEQUENCE_EDITOR",
		"GIT_QUARANTINE_PATH", "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_AUTHOR_DATE",
	}
}

// WithoutRepositoryEnv runs a subprocess with this process's environment,
// less RepositoryEnvVars.
func WithoutRepositoryEnv(ctx context.Context) context.Context {
	return WithEnv(ctx, RepositoryEnv(os.Environ()))
}
