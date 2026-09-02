// Command aw manages multi-repo, multi-worktree coding-agent tasks.
//
// Worktrees are per-repository. Features aren't.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/aw-tool/aw/internal/cli"
)

func main() {
	// Ctrl-C should cancel in-flight git and tmux calls rather than leaving
	// half-finished work behind.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(cli.Execute(ctx))
}
