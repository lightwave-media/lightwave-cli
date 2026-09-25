package cli

// config_env.go — `lw config env`, SSM /lightwave/prod/* as shell exports for
// a human at a terminal.
//
// It is never a loader for a harness, session, hook, service wrapper or
// script: those use `lw config exec --only`, which gives a process only the
// keys it names (owner memo 2026-09-24, CLAUDE.md §24). Hand-wired beside
// `config harness`; declared in commands.yaml v1.7.0 for drift parity.

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/secrets"
	"github.com/spf13/cobra"
)

const configEnvTimeout = 20 * time.Second

var configEnvJSON bool

var configEnvCmd = &cobra.Command{
	Use:   "env",
	Short: "Print SSM /lightwave/prod/* as shell exports (or JSON) for a human terminal",
	Long: `Print every SSM parameter under /lightwave/prod/ as shell export lines,
decrypted, for a human at a terminal:

  eval "$(lw config env)"          # this interactive shell
  lw config env --json             # one flat object

Credentials come from the default AWS chain (AWS_PROFILE when set). Values go to stdout and nowhere else — no file is
written and nothing is logged. Two parameter names that map to the same
variable are reported on stderr; the flat name wins.

This is for a human at a terminal, never a loader for a harness, session,
hook, service wrapper or script. A process gets only the keys it names via
` + "`lw config exec --only KEY,... -- cmd`" + ` (CLAUDE.md §24).`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runConfigEnv,
}

func runConfigEnv(cmd *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), configEnvTimeout)
	defer cancel()

	client, err := secrets.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("lw config env: %w (check AWS_PROFILE and `aws sts get-caller-identity`)", err)
	}

	params, err := secrets.Fetch(ctx, client)
	if err != nil {
		return fmt.Errorf("lw config env: cannot read %s: %w (check AWS_PROFILE and `aws sts get-caller-identity`)", secrets.Path, err)
	}

	pairs, notes := secrets.Resolve(params)
	for _, note := range notes {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "lw config env:", note)
	}

	if configEnvJSON {
		out, err := secrets.RenderJSON(pairs)
		if err != nil {
			return fmt.Errorf("lw config env: %w", err)
		}

		if _, err := cmd.OutOrStdout().Write(out); err != nil {
			return fmt.Errorf("lw config env: write: %w", err)
		}

		return nil
	}

	if _, err := io.WriteString(cmd.OutOrStdout(), secrets.RenderShell(pairs)); err != nil {
		return fmt.Errorf("lw config env: write: %w", err)
	}

	return nil
}

func init() {
	configEnvCmd.Flags().BoolVar(&configEnvJSON, "json", false, "print one flat JSON object instead of export lines")
	configCmd.AddCommand(configEnvCmd)
}
