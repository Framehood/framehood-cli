package cmd

import (
	"github.com/Framehood/framehood-cli/internal/config"
	"github.com/Framehood/framehood-cli/internal/tui"
	"github.com/spf13/cobra"
)

// Version is set at build time via -ldflags "-X .../cmd.Version=...".
var Version = "dev"

// Execute builds the command tree and runs it.
func Execute() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	root := &cobra.Command{
		Use:           "framehood",
		Short:         "Framehood — generate images, video and audio from the terminal",
		Long:          "Framehood CLI. Run without arguments to open the interactive studio, or use a subcommand for one-shot generation.",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		// No subcommand → launch the interactive TUI. It opens even when signed
		// out (shows a "not signed in" state and prompts for `framehood login`).
		RunE: func(cmd *cobra.Command, args []string) error {
			sess, err := NewSession(cfg)
			if err != nil {
				return tui.Run(nil, "")
			}
			return tui.Run(sess.Client(), sess.Email())
		},
	}

	root.AddCommand(
		newLoginCmd(cfg),
		newLogoutCmd(cfg),
		newWhoamiCmd(cfg),
		newGenerateCmd(cfg),
		newBalanceCmd(cfg),
	)
	return root.Execute()
}
