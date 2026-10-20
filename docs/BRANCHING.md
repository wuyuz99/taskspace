# Branches

[Back to the README](../README.md)

## Choose branches

Each repository in a task is written `REPO[@BRANCH][:BASE]`, where `REPO` is a
name registered with `tspace config repo` or a path:

| You write | Branch | Created from |
| --- | --- | --- |
| `backend` | `agent/TASK` | `origin/HEAD` (else local `main`/`master`, then `HEAD`) |
| `backend:develop` | `agent/TASK` | `develop`, or `origin/develop` if there is no local one |
| `backend@feature/x` | `feature/x` | an existing branch is used as it is |
| `backend@fix-12:release/2.1` | `fix-12` | `release/2.1` |
| `api=~/src/backend@x` | `x`, in a repo named `api` | |

A named branch that exists locally is checked out as it is. One that exists
only on a remote — a colleague's, or your own from another machine — is
checked out as a local branch tracking it. Only when neither exists is it
created from the base. `repo add` says which of the three will happen, and
fetches first when you named a branch or base the clone has not seen.
`start` fetches again just before creating a branch, so a branch pushed in
between is still found; if the fetch fails you get a warning and a worktree
made from the refs you already have.

New branches start **with no upstream**. git would otherwise make
`origin/main` the upstream of a branch forked from it, and then `git push`
refuses — or, with `push.default=upstream`, pushes the task's work to main.
Push with `git push -u origin HEAD`. Until then `tspace status` counts the commits
that are not on the base as "not pushed".

The `agent/` prefix is configurable. Branches are recorded when a repository is
added, so changing it leaves existing tasks alone:

```bash
tspace config branch-prefix me/        # me/PAY-142
tspace config branch-prefix ''         # PAY-142
tspace config branch-prefix --unset    # back to agent/PAY-142
```
