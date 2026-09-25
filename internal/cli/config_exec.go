package cli

// config_exec.go — `lw config exec --only A,B -- cmd`, the harness loader for
// runtime secrets (owner memo 2026-09-24, CLAUDE.md §24; lightwave-cli#546).
//
// A process gets exactly the keys it names, read by name from SSM, and lw then
// replaces itself with the command, so the values live only in that command's
// environment: never on argv, stdout, stderr or disk, and no lw parent lingers
// holding them. Any other /lightwave/prod key lw inherited is stripped before
// exec, so a parent that still holds the old session dump cannot pass it on.
// There is deliberately no all-keys form; `lw config env` is the
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

const (
	configExecTimeout = 20 * time.Second
	// exitSecretsUnavailable is EX_CONFIG from sysexits.h: the environment
	// could not be assembled, so the command never started.
	exitSecretsUnavailable = 78
)

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

  lw config exec --only CLOUDFLARE_API_TOKEN -- terragrunt plan
  lw config exec --only NULLTICKETS_API_TOKEN,OPENROUTER_API_KEY -- ./worker --port 9400

--only is required, may be repeated, and there is no all-keys form. Keys are
read by name (GetParameters), so access can be scoped per key. The caller also
needs ssm:DescribeParameters, which returns names only and cannot be scoped
per key.

The command gets exactly the store keys it names. Any other key currently in
/lightwave/prod that is in lw's own environment is removed before exec, under the variable name
` + "`lw config env`" + ` would export it as, nested names included. Those names
come from DescribeParameters, which returns no values. Variables that are not
store keys pass through unchanged.

No value is ever printed. Errors name keys only, and a rejected --only item
is reported by its position and length, never its text.

Credentials come from AWS_PROFILE. When it is unset, lw deliberately uses the
read-only lightwave-agent profile rather than the SDK's default chain.

The command is exec'd directly with no /bin/sh fallback, so a script needs a
shebang, or run it as "-- bash script.sh". A script can give itself its keys
by re-exec'ing through lw once:

  [ -n "${LW_SECRETS_REEXEC:-}" ] || \
    LW_SECRETS_REEXEC=1 exec lw config exec --only KEY -- bash "$0" "$@"
  unset LW_SECRETS_REEXEC

The guard is the marker alone, so an inherited copy of KEY never skips the
strip, and unsetting it lets a nested script do the same for its own keys.

Exit status 78 (EX_CONFIG) means the environment could not be assembled and
the command never started: --only or the command missing, a bad key name, a
missing key, or an AWS read or listing error. A launchd KeepAlive job can tell that apart from
the command's own failures. After exec the status is the command's; a command
that cannot be found or exec'd exits 1.`,
	Args: func(_ *cobra.Command, args []string) error {
		if len(args) == 0 {
			return secretsUnavailable(errors.New("name the command to run after --"))
		}

		return nil
	},
	SilenceUsage: true,
	RunE:         runConfigExec,
}

func runConfigExec(cmd *cobra.Command, args []string) error {
	if len(configExecOnly) == 0 {
		return secretsUnavailable(errors.New("--only is required (name each key the command needs)"))
	}

	keys := splitOnly(configExecOnly)

	binary, err := exec.LookPath(args[0])
	if err != nil {
		return fmt.Errorf("lw config exec: %w", err)
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), configExecTimeout)
	defer cancel()

	client, err := newConfigExecClient(ctx)
	if err != nil {
		return secretsUnavailable(fmt.Errorf("%w (check AWS_PROFILE and `aws sts get-caller-identity`)", err))
	}

	pairs, err := secrets.FetchNamed(ctx, client, keys)
	if err != nil {
		return secretsUnavailable(err)
	}

	storeKeys, err := secrets.StoreEnvNames(ctx, client)
	if err != nil {
		return secretsUnavailable(err)
	}

	if err := execProcess(binary, args, childEnv(os.Environ(), pairs, storeKeys)); err != nil {
		return fmt.Errorf("lw config exec: exec %s: %w", args[0], err)
	}

	return nil
}

// splitOnly splits each --only value on commas. The flag is a plain string
// array because pflag's comma-separated slice parses values as CSV and echoes
// any value it cannot parse, which would print a secret pasted by mistake.
func splitOnly(values []string) []string {
	keys := make([]string, 0, len(values))
	for _, v := range values {
		keys = append(keys, strings.Split(v, ",")...)
	}

	return keys
}

// secretsUnavailable marks a failure before exec with EX_CONFIG.
func secretsUnavailable(err error) error {
	return exitCodeError{err: fmt.Errorf("lw config exec: %w", err), code: exitSecretsUnavailable}
}

// childEnv is the parent environment with every store key removed, then the
// named keys set to their fetched values. An inherited copy of a named key
// can never shadow the store, and an inherited store key the command did not
// name never reaches it.
func childEnv(parent []string, pairs []secrets.Pair, storeKeys map[string]bool) []string {
	named := make(map[string]bool, len(pairs))
	for _, p := range pairs {
		named[p.Key] = true
	}

	env := make([]string, 0, len(parent)+len(pairs))
	for _, kv := range parent {
		key, _, _ := strings.Cut(kv, "=")
		if !named[key] && !storeKeys[key] {
			env = append(env, kv)
		}
	}

	for _, p := range pairs {
		env = append(env, p.Key+"="+p.Value)
	}

	return env
}

// configExecFlags registers exec's flags on c. A test parses a fresh command
// with it, because a used pflag slice cannot be reset: later Sets append.
func configExecFlags(c *cobra.Command) {
	c.Flags().StringArrayVar(&configExecOnly, "only", nil, "comma-separated keys under /lightwave/prod/ to set in the command's environment (repeatable)")
	// Everything after the command name belongs to the command, not to lw.
	c.Flags().SetInterspersed(false)
}

// configExecFlagError replaces pflag's parse errors, which quote the whole
// offending token: `-only=<value>` would otherwise print a value pasted by
// mistake. Nothing ran, so it is EX_CONFIG like every other pre-exec failure.
func configExecFlagError(_ *cobra.Command, _ error) error {
	return secretsUnavailable(errors.New("could not parse the flags before -- (use --only KEY[,KEY] -- command)"))
}

func init() {
	configExecFlags(configExecCmd)
	configExecCmd.SetFlagErrorFunc(configExecFlagError)
	configCmd.AddCommand(configExecCmd)
}
