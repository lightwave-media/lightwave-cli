package cli

// config_exec.go — `lw config exec --only A,B -- cmd`, the harness loader for
// runtime secrets (owner memo 2026-09-24, CLAUDE.md §24; lightwave-cli#546).
//
// A process gets exactly the keys it names, read by name from SSM, and lw then
// replaces itself with the command, so the values live only in that command's
// environment: never on argv, stdout, stderr or disk, and no lw parent lingers
// holding them. There is deliberately no all-keys form; `lw config env` is the
// human-terminal verb. Hand-wired beside `config env`; declared in
// commands.yaml v1.10.0 for drift parity.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/secrets"
	"github.com/spf13/cobra"
)

const configExecTimeout = 20 * time.Second

var configExecOnly []string

// Seams for tests: a fake SSM client, and an exec that records instead of
// replacing the test process.
var (
	newConfigExecClient = secrets.NewNamedClient
	execProcess         = syscall.Exec
)

var configExecCmd = &cobra.Command{
	Use:   "exec --only KEY[,KEY...] -- command [args...]",
	Short: "Run a command with only the named SSM /lightwave/prod keys in its environment",
	Long: `Read the named keys from SSM /lightwave/prod/ and replace lw with the
command, those keys set in its environment:

  lw config exec --only NULLTICKETS_API_TOKEN,LW_WEBHOOK_SECRET -- lw-webhook --port 9400
  lw config exec --only NAME -- sh -c 'probe reading "$NAME" from its env'

--only is required and there is no all-keys form. Keys are read by name
(GetParameters), so access can be scoped per key. Any key that is missing or
cannot be decrypted stops the run before the command starts, and the error
names keys only. No value is ever printed. Credentials come from AWS_PROFILE,
or the lightwave-agent profile when it is unset.`,
	Args:         cobra.MinimumNArgs(1),
	SilenceUsage: true,
	RunE:         runConfigExec,
}

func runConfigExec(cmd *cobra.Command, args []string) error {
	if len(configExecOnly) == 0 {
		return errors.New("lw config exec: --only is required (name each key the command needs)")
	}

	binary, err := exec.LookPath(args[0])
	if err != nil {
		return fmt.Errorf("lw config exec: %w", err)
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), configExecTimeout)
	defer cancel()

	client, err := newConfigExecClient(ctx)
	if err != nil {
		return fmt.Errorf("lw config exec: %w (check AWS_PROFILE and `aws sts get-caller-identity`)", err)
	}

	pairs, err := secrets.FetchNamed(ctx, client, configExecOnly)
	if err != nil {
		return fmt.Errorf("lw config exec: %w", err)
	}

	if err := execProcess(binary, args, childEnv(os.Environ(), pairs)); err != nil {
		return fmt.Errorf("lw config exec: exec %s: %w", args[0], err)
	}

	return nil
}

// childEnv is the parent environment with every named key replaced by its
// fetched value, so an inherited copy can never shadow the store.
func childEnv(parent []string, pairs []secrets.Pair) []string {
	named := make(map[string]bool, len(pairs))
	for _, p := range pairs {
		named[p.Key] = true
	}

	env := make([]string, 0, len(parent)+len(pairs))
	for _, kv := range parent {
		key, _, _ := strings.Cut(kv, "=")
		if !named[key] {
			env = append(env, kv)
		}
	}

	for _, p := range pairs {
		env = append(env, p.Key+"="+p.Value)
	}

	return env
}

func init() {
	configExecCmd.Flags().StringSliceVar(&configExecOnly, "only", nil, "comma-separated keys under /lightwave/prod/ to set in the command's environment")
	// Everything after the command name belongs to the command, not to lw.
	configExecCmd.Flags().SetInterspersed(false)
	configCmd.AddCommand(configExecCmd)
}
