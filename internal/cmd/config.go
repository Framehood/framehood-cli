package cmd

import (
	"fmt"

	"github.com/Framehood/framehood-cli/internal/config"
	"github.com/spf13/cobra"
)

// newConfigCmd wires `framehood config` with `get` and `set` subcommands for
// one-shot inspection/management of CLI preferences (currently the output dir).
func newConfigCmd(cfg config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "View or change CLI settings (output directory, …)",
	}
	cmd.AddCommand(newConfigGetCmd(cfg), newConfigSetCmd(cfg))
	return cmd
}

// newConfigGetCmd prints the resolved settings: output dir, config dir, MCP base.
func newConfigGetCmd(cfg config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Print the current settings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cfg.OutputDir()
			if out == "" {
				out = "(current working directory)"
			}
			cmd.Printf("output dir: %s\n", out)
			cmd.Printf("config dir: %s\n", cfg.ConfigDir)
			cmd.Printf("MCP base:   %s\n", cfg.MCPBase)
			return nil
		},
	}
}

// newConfigSetCmd handles `framehood config set output-dir <path>`. Other keys
// are rejected so typos surface instead of silently doing nothing.
func newConfigSetCmd(cfg config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a setting, e.g. `config set output-dir ~/Downloads`",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], args[1]
			switch key {
			case "output-dir":
				abs, err := cfg.SetOutputDir(value)
				if err != nil {
					return err
				}
				if abs == "" {
					cmd.Println("output dir cleared → current working directory")
				} else {
					cmd.Printf("output dir → %s\n", abs)
				}
				return nil
			default:
				return fmt.Errorf("unknown setting %q (supported: output-dir)", key)
			}
		},
	}
}
