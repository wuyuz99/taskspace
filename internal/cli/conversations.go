package cli

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/workspace"
)

// conversationCarriers are the agents whose conversation history tspace can
// carry to a directory's new path, each an opt-in integration named after
// its agent. Core workspace only knows workspace.ConversationCarrier.
var conversationCarriers = map[string]workspace.ConversationCarrier{
	"claude": claudeConversations{},
}

// claudeConversations moves Claude Code's history for a directory: the
// folder under its projects directory named after the directory's path.
type claudeConversations struct{}

// claudeProjectUnsafe is what Claude Code replaces with "-" in a directory's
// path to name its project folder.
var claudeProjectUnsafe = regexp.MustCompile(`[^A-Za-z0-9]`)

// claudeProjectMax is the longest folder name Claude Code uses as is. It
// shortens longer ones its own way, which tspace does not guess at.
const claudeProjectMax = 200

// claudeProject is the name of Claude Code's project folder for dir, or ""
// for a path too long to name exactly.
func claudeProject(dir string) string {
	name := claudeProjectUnsafe.ReplaceAllString(dir, "-")
	if len(name) > claudeProjectMax {
		return ""
	}
	return name
}

// claudeProjectsDir is where Claude Code keeps its project folders.
func claudeProjectsDir() (string, error) {
	dir, err := claudeConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "projects"), nil
}

// Carry implements workspace.ConversationCarrier. It renames the folder,
// and refuses to merge into one the new path already has.
func (claudeConversations) Carry(from, to string) (bool, error) {
	dir, err := claudeProjectsDir()
	if err != nil {
		return false, err
	}
	oldName := claudeProject(from)
	if oldName == "" {
		return false, nil
	}
	src := filepath.Join(dir, oldName)
	if _, err := os.Lstat(src); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	newName := claudeProject(to)
	if newName == "" {
		return false, fmt.Errorf("%s is too long a path to name Claude Code's folder for it", to)
	}
	if newName == oldName {
		return true, nil
	}
	dst := filepath.Join(dir, newName)
	if _, err := os.Lstat(dst); err == nil {
		return false, fmt.Errorf("%s already exists; merge %s into it by hand", dst, src)
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.Rename(src, dst); err != nil {
		return false, err
	}
	return true, nil
}

func (a *App) newConfigCarryConversationsCmd() *cobra.Command {
	var unset bool
	names := slices.Sorted(maps.Keys(conversationCarriers))
	cmd := &cobra.Command{
		Use:   "carry-conversations [AGENT... | --unset]",
		Short: "Show or choose the agents whose conversations move with their directory",
		Long: "Many agents keep a conversation by the directory it ran in, so moving a\n" +
			"task root or worktree, as task rename does, leaves it behind. Naming an\n" +
			"agent here has tspace move that agent's history along, once the move is\n" +
			"done. Each agent stores it its own way, so each is an integration of its\n" +
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
			c, err := a.Store.LoadConfig()
			if err != nil {
				return err
			}
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
