package status

// Rollup is where a task stands across its repositories: the question dash
// answers per task. It comes from what status already read, so it costs no
// more git.
type Rollup struct {
	Repos int
	// Missing counts repositories with no worktree yet, and Errors those git
	// could not read.
	Missing, Errors int
	// Holding are the repositories with uncommitted changes or stashes, which
	// clean refuses to remove. Clean can refuse for other reasons, a lock or a
	// detached HEAD, so an empty list does not promise it would go ahead.
	Holding []string
	// Unpushed are the repositories with commits that are only on this machine.
	Unpushed []string
}

// Rollup sums up the task's repositories.
func (t TaskStatus) Rollup() Rollup {
	r := Rollup{Repos: len(t.Repos)}
	for _, repo := range t.Repos {
		switch repo.Git {
		case GitMissing:
			r.Missing++
		case GitError:
			r.Errors++
		default:
			if repo.Detail.Dirty() {
				r.Holding = append(r.Holding, repo.Repo)
			}
			if repo.Detail.Ahead > 0 || repo.Unpushed > 0 {
				r.Unpushed = append(r.Unpushed, repo.Repo)
			}
		}
	}
	return r
}
