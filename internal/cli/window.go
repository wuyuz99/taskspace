package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/workspace"
)

func (a *App) newWindowCmd() *cobra.Command {
	var root, useAgent, detach, save bool
	var name, forget, rename string
	cmd := &cobra.Command{
		Use:   "window TASK [REPO | --root] [--agent | -- COMMAND [ARGS...]] | TASK --save NAME | TASK --forget NAME | TASK --rename OLD NEW",
		Short: "Open another window in a task's running session",
		Long: "window adds a window to the task's session, in a repository's worktree or\n" +
			"at the task root, without touching the windows already there:\n\n" +
			"  tspace window PAY-142 backend --agent     the task's agent, in backend only\n" +
			"  tspace window PAY-142 --name staging      a spare shell at the task root\n" +
			"  tspace window PAY-142 infra -- kubectl port-forward svc/api 8080:80\n\n" +
			"Without a command the window runs your shell and closes when it exits.\n" +
			"A command's window stays open after it exits, so its output can be read.\n" +
			"Without REPO, a task with one repository opens that worktree; any other\n" +
			"task opens its root. Inside the multiplexer you are switched to it.\n\n" +
			"Extra windows are named with a leading '_', and stop ends them with the\n" +
			"rest of the session. --save, with --name, keeps the window with the task,\n" +
			"and in project recipes saved from it: start and open recreate it whenever\n" +
			"it is not there. Without a running session, --save only saves it.\n\n" +
			"tspace remembers what an unsaved window runs while its session lasts, so\n" +
			"restart can relaunch it and --save NAME can keep it once it is open.\n\n" +
			"  tspace window PAY-142 backend --name git --save -- lazygit\n" +
			"  tspace window PAY-142 --save _main-lazygit  keep a window already open\n" +
			"  tspace window PAY-142 --forget git         start no longer opens it\n" +
			"  tspace window PAY-142 --rename git lg      open and saved alike",
		Args: func(cmd *cobra.Command, args []string) error {
			n := len(args)
			if d := cmd.ArgsLenAtDash(); d >= 0 {
				n = d
			}
			switch {
			case cmd.Flags().Changed("forget") && (n != 1 || len(args) != 1):
				return fmt.Errorf("expected TASK --forget NAME")
			case cmd.Flags().Changed("rename") && (n != 2 || len(args) != 2):
				return fmt.Errorf("expected TASK --rename OLD NEW")
			case n < 1 || n > 2:
				return fmt.Errorf("expected TASK [REPO], optionally followed by -- COMMAND [ARGS...]")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			pos, argv := args, []string(nil)
			if d := cmd.ArgsLenAtDash(); d >= 0 {
				pos, argv = args[:d], args[d:]
				if len(argv) == 0 {
					return fmt.Errorf("no command after --")
				}
			}
			if useAgent && len(argv) > 0 {
				return fmt.Errorf("choose either --agent or a command after --")
			}
			managing := cmd.Flags().Changed("forget") || cmd.Flags().Changed("rename")
			if (cmd.Flags().Changed("forget") && strings.TrimSpace(forget) == "") || (cmd.Flags().Changed("rename") && strings.TrimSpace(rename) == "") {
				return fmt.Errorf("--forget and --rename need a window name")
			}
			if managing && (root || useAgent || save || detach || name != "") {
				return fmt.Errorf("--forget and --rename take only the task and window names")
			}
			if forget != "" && rename != "" {
				return fmt.Errorf("choose either --forget or --rename")
			}
			// With nothing to open, the name after TASK is a window that is.
			saveOpen := save && name == "" && len(pos) == 2 && cmd.ArgsLenAtDash() < 0 && !root && !useAgent && !detach
			if save && name == "" && !saveOpen {
				return fmt.Errorf("--save needs --name, which the window is saved and recreated by; to keep a window already open: tspace window TASK --save NAME")
			}
			taskName, err := a.resolveTask(pos[0])
			if err != nil {
				return err
			}
			ws, ctx := a.workspaceFor(cmd), cmd.Context()
			switch {
			case forget != "":
				return ws.ForgetWindow(ctx, taskName, forget)
			case rename != "":
				return ws.RenameWindow(ctx, taskName, rename, pos[1])
			case saveOpen:
				return ws.SaveOpenWindow(ctx, taskName, pos[1])
			}
			opts := workspace.OpenWindowOptions{Root: root, Name: name, Command: argv, Agent: useAgent, Save: save, Detach: detach}
			if len(pos) == 2 {
				opts.Repo = pos[1]
			}
			res, err := ws.OpenWindow(ctx, taskName, opts)
			if err != nil {
				return err
			}
			if res.Unfocused {
				a.printf("attach with: tspace attach %s\n", res.Task.Name)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&root, "root", false, "open the window at the task root")
	cmd.Flags().BoolVar(&useAgent, "agent", false, "run the task's agent (its root agent, else the default from tspace config)")
	cmd.Flags().StringVar(&name, "name", "", "window name, shown with a leading '_' (default: from the place and command)")
	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "open the window without switching to it")
	cmd.Flags().BoolVar(&save, "save", false, "keep the window with the task, for start and open to recreate")
	cmd.Flags().StringVar(&forget, "forget", "", "stop recreating the saved window NAME")
	cmd.Flags().StringVar(&rename, "rename", "", "rename the extra window OLD, open or saved, to the name after TASK")
	return cmd
}
