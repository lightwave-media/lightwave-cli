package cli

// secret.go — `lw secret list|rotate` (lightwave-cli#547, slice 1): the
// agent-facing door to secrets that never shows a value (owner memo
// 2026-09-24, CLAUDE.md §24). list prints names and policy from the secret
// map; rotate replaces a generate-mode key's value without anyone seeing it.
// Hand-wired like `config exec`; declared in lightwave-core commands.yaml.

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/lightwave-media/lightwave-cli/internal/secrets"
)

const (
	secretRotateTimeout = 3 * time.Minute
	secretListPadding   = 2
)

var (
	secretRotateProfile string
	secretRotateDryRun  bool
)

// Seams for tests: the map location, the AWS clients and the host effects.
var (
	secretMapDir      = secrets.DefaultMapDir
	secretLedgerPath  = secrets.DefaultLedgerPath
	newSecretMeta     = secrets.NewMetaReader
	newOperatorWriter = secrets.NewOperatorWriter
	newPersonaWriter  = secrets.NewPersonaWriter
	secretKickstart   = secrets.Kickstart
	secretProber      = secrets.ProbeViaConfigExec
)

var secretCmd = &cobra.Command{
	Use:   "secret",
	Short: "List and rotate SSM /lightwave/prod keys by name, never by value",
}

var secretListCmd = &cobra.Command{
	Use:          "list", //nolint:goconst // a cobra Use string, literal like every other list verb here
	Short:        "List the secret map: name, status, rotation mode and consumer count",
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runSecretList,
}

var secretRotateCmd = &cobra.Command{
	Use:   "rotate NAME",
	Short: "Replace a generate-mode key's value, refresh its consumers and probe them",
	Long: `Generate a new value for NAME, write it to SSM as the rotator role, kickstart
each consumer's launchd job, and probe each consumer through lw config exec.
The value is never printed, logged or recorded: output and the ledger
(~/.lightwave/observability/secrets-exposure.jsonl) carry names and versions.

Agents rotate as their persona: LW_PERSONA=v_<name> assumes the rotator role
from lightwave-agent, and the persona must be in the key's rotators. An
operator passes --profile, a profile whose role_session_name is op_*.

Only generate-mode SSM keys with no aliases, mirrors or literal peers rotate
here; anything else is refused and points at the rotate-aws-secret runbook.

Exit codes: 0 rotated and verified; 1 failed (a follow-up failed after the
write, or the write failed); 3 rotated, a consumer still needs a refresh lw
cannot run; 4 refused, nothing written.`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE:         runSecretRotate,
}

func loadSecretMap() (*secrets.Map, error) {
	dir, err := secretMapDir()
	if err != nil {
		return nil, err
	}

	return secrets.LoadMap(dir)
}

func runSecretList(cmd *cobra.Command, _ []string) error {
	m, err := loadSecretMap()
	if err != nil {
		return fmt.Errorf("lw secret list: %w", err)
	}

	recs := slices.Clone(m.Records)
	slices.SortFunc(recs, func(a, b secrets.Record) int { return strings.Compare(a.Name, b.Name) })

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, secretListPadding, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tSTATUS\tMODE\tCONSUMERS")

	for i := range recs {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\n", recs[i].Name, recs[i].Status, recs[i].RotationMode, len(m.Consumers(&recs[i])))
	}

	return w.Flush()
}

func runSecretRotate(cmd *cobra.Command, args []string) error {
	res, err := rotateSecret(cmd.Context(), args[0])
	if err != nil {
		return exitCodeError{err: fmt.Errorf("lw secret rotate: %w", err), code: secrets.ErrorExitCode(err)}
	}

	_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Line())

	if code := res.ExitCode(); code != secrets.ExitDone {
		return exitCodeError{err: fmt.Errorf("lw secret rotate: %s: follow-up incomplete (see the line above)", res.Name), code: code}
	}

	return nil
}

// rotateSecret refuses what the map forbids before touching AWS, then builds
// the effects and rotates.
func rotateSecret(parent context.Context, name string) (*secrets.Result, error) {
	ctx, cancel := context.WithTimeout(parent, secretRotateTimeout)
	defer cancel()

	m, err := loadSecretMap()
	if err != nil {
		return nil, err
	}

	rec, err := m.Lookup(name)
	if err != nil {
		return nil, err
	}

	if err := secrets.CheckRecord(rec); err != nil {
		return nil, err
	}

	deps, err := rotateDeps(ctx, rec)
	if err != nil {
		return nil, err
	}

	return secrets.Rotate(ctx, deps, m, name)
}

func rotateDeps(ctx context.Context, rec *secrets.Record) (*secrets.RotateDeps, error) {
	var (
		writer secrets.ValueWriter
		err    error
	)

	if secretRotateProfile != "" {
		writer, err = newOperatorWriter(ctx, secretRotateProfile)
	} else {
		writer, err = newPersonaWriter(ctx, os.Getenv("LW_PERSONA"), rec.WriterIdentity)
	}

	if err != nil {
		return nil, err
	}

	meta, err := newSecretMeta(ctx)
	if err != nil {
		return nil, err
	}

	lw, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate lw for the probe: %w", err)
	}

	ledger, err := secretLedgerPath()
	if err != nil {
		return nil, err
	}

	return &secrets.RotateDeps{
		Meta:      meta,
		Writer:    writer,
		Random:    rand.Reader,
		Kickstart: secretKickstart,
		Probe:     secretProber(lw),
		Ledger:    secrets.AppendLedger(ledger),
		Now:       time.Now,
		Sleep:     time.Sleep,
		DryRun:    secretRotateDryRun,
	}, nil
}

func init() {
	secretRotateCmd.Flags().StringVar(&secretRotateProfile, "profile", "", "operator profile that assumes the rotator role as op_* (agents leave it unset and set LW_PERSONA)")
	secretRotateCmd.Flags().BoolVar(&secretRotateDryRun, "dry-run", false, "run every check, including the caller's identity, and write nothing")
	secretCmd.AddCommand(secretListCmd, secretRotateCmd)
	rootCmd.AddCommand(secretCmd)
}
