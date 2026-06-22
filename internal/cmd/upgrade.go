package cmd

import (
	"context"
	"time"

	"github.com/Framehood/framehood-cli/internal/selfupdate"
	"github.com/spf13/cobra"
)

// newUpgradeCmd wires `framehood upgrade` (alias `update`): it self-replaces the
// binary with the latest GitHub release, or — for package-manager-managed
// installs — prints the right command instead.
func newUpgradeCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "upgrade",
		Aliases: []string{"update"},
		Short:   "Update framehood to the latest release",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
			defer cancel()

			cmd.Println("Checking for updates…")
			res, err := selfupdate.Upgrade(ctx, Version)
			if err != nil {
				return err
			}
			switch res.Outcome {
			case selfupdate.OutcomeUpToDate:
				cmd.Printf("Already on the latest (%s)\n", displayVersion(res.To))
			case selfupdate.OutcomeUpgraded:
				cmd.Printf("Upgraded %s → %s\n", displayVersion(res.From), displayVersion(res.To))
			case selfupdate.OutcomeManagedRan:
				cmd.Printf("Upgraded to %s via %s\n", displayVersion(res.To), res.Manager)
			case selfupdate.OutcomeManaged:
				cmd.Printf("A newer version (%s) is available, but %s\n", displayVersion(res.To), res.Advice)
			}
			return nil
		},
	}
}

// displayVersion ensures a leading "v" for presentation (the build-time Version
// and release tags are normally already "vX.Y.Z", but "dev" stays as-is).
func displayVersion(v string) string {
	if v == "" || v == "dev" {
		return v
	}
	if v[0] == 'v' {
		return v
	}
	return "v" + v
}
