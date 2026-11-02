package cli

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/agents"
	"github.com/wuyuz99/taskspace/internal/workspace"
)

// conversationCarriers are the agents whose conversation history tspace can
// carry to a directory's new path, each an opt-in integration named after
// its agent. Core workspace only knows workspace.ConversationCarrier.
var conversationCarriers = map[string]workspace.ConversationCarrier{
	"claude": agents.ClaudeConversations{},
}

func (a *App) newConfigCarryConversationsCmd() *cobra.Command {
	var unset bool
	names := slices.Sorted(maps.Keys(conversationCarriers))
	cmd := &cobra.Command{
		Use:   "carry-conversations [AGENT... | --unset]",
		Short: "Show or choose the agents whose conversations move with their directory",
		Long: "Many agents keep a conversation by the directory it ran in, so moving a\n" +
			"task root or worktree, as task rename and repo move do, leaves it behind.\n" +
			"Naming an agent here has tspace move that agent's history along, once the\n" +
			"move is done. Each agent stores it its own way, so each is an integration of its\n" +
			"own, off unless chosen. Known: " + strings.Join(names, ", ") + ".\n\n" +
			"  claude  renames the folder under ~/.claude/projects (under\n" +
			"          $CLAUDE_CONFIG_DIR if that is set) named after the directory;\n" +
			"          one the new directory already has is left for you to merge;\n" +
			"          conversations started in a subdirectory stay where they are\n\n" +
			"A conversation that is not carried is named, with the directory it\n" +
			"stays in, so you can move it by hand.\n\n" +
			"  tspace config carry-conversations claude\n" +
			"  tspace config carry-conversations --unset",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if unset && len(args) > 0 {
				return fmt.Errorf("choose either agents or --unset")
			}
			for _, n := range args {
				if _, ok := conversationCarriers[n]; !ok {
					return fmt.Errorf("tspace cannot carry %q's conversations (can: %s)", n, strings.Join(names, ", "))
				}
			}
			c, unlock, err := a.configFor(cmd, unset || len(args) > 0)
			if err != nil {
				return err
			}
			defer unlock()
			switch {
			case unset:
				c.CarryConversations = nil
			case len(args) == 0:
				a.printf("%s\n", carryConversationsDisplay(c.CarryConversations))
				return nil
			default:
				c.CarryConversations = slices.Compact(slices.Sorted(slices.Values(args)))
			}
			if err := a.Store.SaveConfig(c); err != nil {
				return err
			}
			a.printf("conversations carried for: %s\n", carryConversationsDisplay(c.CarryConversations))
			return nil
		},
	}
	cmd.Flags().BoolVar(&unset, "unset", false, "carry no agent's conversations")
	return cmd
}

func carryConversationsDisplay(names []string) string {
	if len(names) == 0 {
		return "none; a moved directory's conversations stay at the old path"
	}
	return strings.Join(names, ", ")
}
