package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var (
	contentApplyDryRun     bool
	contentApplyForce      bool
	contentDiffJSON        bool
	contentPromoteTo       string
	contentPromoteFrom     string
	contentPromoteCommit   bool
	contentPromoteActor    string
	contentPromoteEmergncy bool
	contentPromoteNotes    string
)

var contentCmd = &cobra.Command{
	Use:   "content",
	Short: "DB-driven CMS content migrations",
	Long: `Apply and diff git-tracked content migration YAMLs against tenant DB state.

Content migrations live under content/migrations/ and describe canonical
Page/PageSection state per tenant. The DB is a materialized view of these
artifacts (see plans/abstract-launching-melody.md).

Examples:
  lw content apply content/migrations/0001_lightwave_media_canonical.yaml --dry-run
  lw content apply content/migrations/0001_lightwave_media_canonical.yaml
  lw content diff content/migrations/0001_lightwave_media_canonical.yaml
  lw content diff content/migrations/0001_lightwave_media_canonical.yaml --json`,
}

var contentApplyCmd = &cobra.Command{
	Use:   "apply <path>",
	Short: "Apply a content migration YAML to tenant DB",
	Long: `Apply a content migration to the tenant referenced in the YAML.

Idempotent: running twice writes nothing the second time (content_hash gate).
Writes flow through PageSection (relational source of truth), then rebuild
the Page.sections JSON cache.

Path must be reachable inside the backend container — the repo's
content/ directory mounts at /content in dev compose.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}

		parts := []string{"apply_content_migration", args[0]}
		if contentApplyDryRun {
			parts = append(parts, "--dry-run")
		}
		if contentApplyForce {
			parts = append(parts, "--force")
		}

		return runMake(dir, "dj-manage", fmt.Sprintf("CMD=%s", strings.Join(parts, " ")))
	},
}

var contentDiffCmd = &cobra.Command{
	Use:   "diff <path>",
	Short: "Diff a content migration YAML against live tenant DB",
	Long: `Read-only structural diff between a content migration YAML and the
live tenant state. Output is page-by-page, section-by-section, keyed by the
(type, variant, order) triple.

Use this before 'apply' to preview what would change. No DB writes.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}

		parts := []string{"diff_content_migration", args[0]}
		if contentDiffJSON {
			parts = append(parts, "--json")
		}

		return runMake(dir, "dj-manage", fmt.Sprintf("CMD=%s", strings.Join(parts, " ")))
	},
}

func init() {
	contentApplyCmd.Flags().BoolVar(&contentApplyDryRun, "dry-run", false, "preview changes without writing")
	contentApplyCmd.Flags().BoolVar(&contentApplyForce, "force", false, "apply even if content_hash unchanged")

	contentDiffCmd.Flags().BoolVar(&contentDiffJSON, "json", false, "emit JSON instead of YAML")

	contentPromoteCmd.Flags().StringVar(&contentPromoteTo, "to", "", "destination env: local|staging|production (required)")
	_ = contentPromoteCmd.MarkFlagRequired("to")
	contentPromoteCmd.Flags().StringVar(&contentPromoteFrom, "from", "", "source env (defaults to current ENVIRONMENT)")
	contentPromoteCmd.Flags().BoolVar(&contentPromoteCommit, "commit", false, "stage OutboxItems + audit row (default: dry-run)")
	contentPromoteCmd.Flags().StringVar(&contentPromoteActor, "initiated-by", "", "username/email of the actor")
	contentPromoteCmd.Flags().BoolVar(&contentPromoteEmergncy, "emergency-bypass", false, "mark as emergency (requires --review-notes)")
	contentPromoteCmd.Flags().StringVar(&contentPromoteNotes, "review-notes", "", "rationale (required when --emergency-bypass)")

	contentCmd.AddCommand(contentApplyCmd)
	contentCmd.AddCommand(contentDiffCmd)
	contentCmd.AddCommand(contentPromoteCmd)
}

var contentPromoteCmd = &cobra.Command{
	Use:   "promote <path>",
	Short: "Stage a content migration to a destination env via OutboxItems",
	Long: `Promote a git-tracked content migration YAML to a destination environment.

Plan-by-default — emits the diff between artifact and destination tenant
state without writing anything. Pass --commit to create the
ContentPromotion audit row + one OutboxItem per affected page (pending
approval; existing OutboxItem._apply_change() applies on approval).

--emergency-bypass marks the promotion for the two-person bypass path and
requires --review-notes. The bypass is recorded on the audit row but does
not auto-apply.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}

		parts := []string{"promote_content_migration", args[0], "--to", contentPromoteTo}
		if contentPromoteFrom != "" {
			parts = append(parts, "--from", contentPromoteFrom)
		}
		if contentPromoteCommit {
			parts = append(parts, "--commit")
		}
		if contentPromoteActor != "" {
			parts = append(parts, "--initiated-by", contentPromoteActor)
		}
		if contentPromoteEmergncy {
			parts = append(parts, "--emergency-bypass")
		}
		if contentPromoteNotes != "" {
			parts = append(parts, "--review-notes", fmt.Sprintf("%q", contentPromoteNotes))
		}

		return runMake(dir, "dj-manage", fmt.Sprintf("CMD=%s", strings.Join(parts, " ")))
	},
}
