package cmd

import (
	"errors"

	"github.com/Framehood/framehood-cli/internal/auth"
	"github.com/Framehood/framehood-cli/internal/config"
	"github.com/Framehood/framehood-cli/internal/tui"
	"github.com/spf13/cobra"
)

// Version is set at build time via -ldflags "-X .../cmd.Version=...".
var Version = "dev"

// studioAuth must satisfy the studio's Authenticator contract so the `/login`
// and `/logout` palette commands work.
var _ tui.Authenticator = studioAuth{}

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
		// out (shows a "not signed in" state; /login signs in from inside).
		RunE: func(cmd *cobra.Command, args []string) error {
			authn := studioAuth{cfg: cfg}
			sess, err := NewSession(cfg)
			if err != nil {
				// Only a *missing/stale* credential means "launch signed out".
				// Anything else (corrupted creds, permission error) is a real
				// failure the user should see, not silently swallow.
				if errors.Is(err, auth.ErrNotLoggedIn) {
					return tui.Run(nil, "", authn, cfg, Version)
				}
				return err
			}
			return tui.Run(sess.Client(), sess.Email(), authn, cfg, Version)
		},
	}

	root.AddCommand(
		newLoginCmd(cfg),
		newLogoutCmd(cfg),
		newWhoamiCmd(cfg),
		newGenerateCmd(cfg),
		newBalanceCmd(cfg),
		newLibraryCmd(cfg),
		newProjectCmd(cfg),
		newTeamCmd(cfg),
		newConfigCmd(cfg),
		newUpgradeCmd(),
	)
	return root.Execute()
}
