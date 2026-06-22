package cmd

import (
	"fmt"
	"strings"

	"github.com/Framehood/framehood-cli/internal/config"
	"github.com/Framehood/framehood-cli/internal/render"
	"github.com/spf13/cobra"
)

// callTool invokes an MCP tool and prints its payload as human-readable text
// (per-tool/action formatter), falling back to pretty JSON for unknown shapes.
func callTool(cmd *cobra.Command, cfg config.Config, tool string, args map[string]any) error {
	sess, err := NewSession(cfg)
	if err != nil {
		return err
	}
	raw, err := sess.Client().CallTool(cmd.Context(), tool, args)
	if err != nil {
		return err
	}
	action, _ := args["action"].(string)
	if out, ok := render.Readable(tool, action, raw); ok {
		fmt.Println(out)
	} else {
		fmt.Println(render.PrettyJSON(raw))
	}
	return nil
}

// newLibraryCmd — search past generations and manage the trash (MCP `library`).
func newLibraryCmd(cfg config.Config) *cobra.Command {
	var typ, project string
	var limit int
	cmd := &cobra.Command{
		Use:   "library [query]",
		Short: "Search your generated assets; manage the trash",
		Example: "  framehood library \"red fox\"\n" +
			"  framehood library --type video\n" +
			"  framehood library trash <asset-id>",
		RunE: func(cmd *cobra.Command, args []string) error {
			a := map[string]any{"action": "list"}
			if len(args) > 0 {
				a["query"] = strings.Join(args, " ")
			}
			if typ != "" {
				a["type"] = typ
			}
			if project != "" {
				a["project"] = project
			}
			if limit > 0 {
				a["limit"] = limit
			}
			return callTool(cmd, cfg, "library", a)
		},
	}
	cmd.Flags().StringVarP(&typ, "type", "t", "", "Filter by media type: image | video | audio")
	cmd.Flags().StringVar(&project, "project", "", "Filter to a project id")
	cmd.Flags().IntVarP(&limit, "limit", "n", 24, "Max results")
	cmd.AddCommand(
		&cobra.Command{Use: "trashed", Short: "List trashed assets", RunE: func(cmd *cobra.Command, _ []string) error {
			return callTool(cmd, cfg, "library", map[string]any{"action": "trashed"})
		}},
		&cobra.Command{Use: "trash <asset-id>", Short: "Move an asset to trash (recoverable for 10 days)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			return callTool(cmd, cfg, "library", map[string]any{"action": "trash", "id": args[0]})
		}},
		&cobra.Command{Use: "restore <asset-id>", Short: "Restore an asset from trash", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			return callTool(cmd, cfg, "library", map[string]any{"action": "restore", "id": args[0]})
		}},
	)
	return cmd
}

// newProjectCmd — personal/shared groupings of generations (MCP `project`).
func newProjectCmd(cfg config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Group generations into projects",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return callTool(cmd, cfg, "project", map[string]any{"action": "list"})
		},
	}
	var shared bool
	var desc string
	create := &cobra.Command{
		Use: "create <name>", Short: "Create a project", Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			vis := "personal"
			if shared {
				vis = "shared"
			}
			return callTool(cmd, cfg, "project", map[string]any{"action": "create", "name": strings.Join(args, " "), "visibility": vis, "description": desc})
		},
	}
	create.Flags().BoolVar(&shared, "shared", false, "Visible to the whole org (default: personal)")
	create.Flags().StringVar(&desc, "desc", "", "Project description")
	cmd.AddCommand(
		create,
		&cobra.Command{Use: "delete <project-id>", Short: "Delete a project (its assets stay in the library)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			return callTool(cmd, cfg, "project", map[string]any{"action": "delete", "id": args[0]})
		}},
		&cobra.Command{Use: "assign <asset-id> [project-id]", Short: "Put an asset in a project (omit the project id to unassign)", Args: cobra.RangeArgs(1, 2), RunE: func(cmd *cobra.Command, args []string) error {
			a := map[string]any{"action": "assign", "asset_id": args[0]}
			if len(args) > 1 {
				a["id"] = args[1]
			}
			return callTool(cmd, cfg, "project", a)
		}},
		&cobra.Command{Use: "use [project-id]", Short: "Set your active/default project — new generations land there (omit the id to clear)", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			a := map[string]any{"action": "use"}
			if len(args) > 0 {
				a["id"] = args[0]
			}
			return callTool(cmd, cfg, "project", a)
		}},
		&cobra.Command{Use: "current", Short: "Show your active/default project", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			return callTool(cmd, cfg, "project", map[string]any{"action": "current"})
		}},
	)
	return cmd
}

// newTeamCmd — organization members, spend and management (MCP `org`).
func newTeamCmd(cfg config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "team",
		Short: "Your organization: members, spend, roles",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return callTool(cmd, cfg, "org", map[string]any{"action": "members"})
		},
	}
	var days int
	trend := &cobra.Command{
		Use: "trend", Short: "Daily org credit spend",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if days < 7 {
				days = 7
			} else if days > 90 {
				days = 90
			}
			return callTool(cmd, cfg, "org", map[string]any{"action": "trend", "days": days})
		},
	}
	trend.Flags().IntVar(&days, "days", 30, "Window in days (7–90)")
	cmd.AddCommand(
		&cobra.Command{Use: "spend", Short: "Per-member credit spend", RunE: func(cmd *cobra.Command, _ []string) error {
			return callTool(cmd, cfg, "org", map[string]any{"action": "spend"})
		}},
		trend,
		&cobra.Command{Use: "role <email> <member|admin>", Short: "Change a member's role (owner only)", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
			return callTool(cmd, cfg, "org", map[string]any{"action": "set_role", "email": args[0], "role": args[1]})
		}},
		&cobra.Command{Use: "suspend <email>", Short: "Suspend a member (owner|admin)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			return callTool(cmd, cfg, "org", map[string]any{"action": "suspend", "email": args[0]})
		}},
		&cobra.Command{Use: "enable <email>", Short: "Re-enable a suspended member (owner|admin)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			return callTool(cmd, cfg, "org", map[string]any{"action": "enable", "email": args[0]})
		}},
		&cobra.Command{Use: "invite <email>", Short: "Invite a member by email (owner only)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			return callTool(cmd, cfg, "org", map[string]any{"action": "invite", "email": args[0]})
		}},
		&cobra.Command{Use: "remove <email>", Short: "Remove a member (owner only)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			return callTool(cmd, cfg, "org", map[string]any{"action": "remove", "email": args[0]})
		}},
	)
	return cmd
}
