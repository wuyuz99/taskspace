# Safe cleanup

[Back to the README](../README.md)

## Safety

The rule is: **refusing beats losing code.**

`clean TASK REPO...` applies the same checks to only those repositories and
closes their primary and saved windows. Unsaved extra windows have no recorded
repository, so stop the task first if any are present. `--rm` is only available
for whole-task cleanup. `repo rm TASK REPO` cleans first and then drops the
repository record; `--keep-worktree` explicitly leaves its worktree/windows
behind, where later clean cannot reach them.

`tspace clean` refuses outright if any worktree has uncommitted changes, untracked
files, a stash on its branch, or a lock. It tells you which files, in which
repo, and how to proceed.

For a detached HEAD, cleanup also verifies that its commits are reachable from
a surviving branch, tag, or remote-tracking ref. Otherwise it refuses: create a
branch at HEAD to preserve the work before cleaning. `--force` overrides this
protection, as it does for uncommitted changes.

Branches are **never deleted by default**. With `--delete-branch`, a branch is
removed only when its work is *merged into the base branch* or *pushed to an
upstream*. Both halves matter: without the upstream test a pushed-but-unmerged
branch looks unique forever, and without the merge test a merged local-only
branch could never be cleaned.

The reference protecting that work must survive deletion. A branch used as its
own base or upstream, including through a symbolic alias, is not protection by
itself; cleanup keeps the branch unless another surviving reference protects it.

A **squash merge** counts as merged: none of the branch's commits reach the
base, and the hosting service has often deleted the remote branch too, so
`clean` also looks for a single commit on the base that makes exactly the
branch's combined change. A squash that needed conflict resolution changes
that patch and is not recognised, so the branch is kept. The base is compared
as your clone last fetched it; `git fetch` first if the merge just happened.

`tspace clean` and `tspace task rm` are deliberately different. `clean` tears down
worktrees and keeps the record; `task rm` deletes the record and refuses while
worktrees exist. `clean --rm` does both for a finished task, deleting the record
only once every worktree is gone: safely, unless `--force` discarded their work.
