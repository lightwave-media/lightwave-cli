package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// RejectUnknownSubcommands makes every command group fail on a verb it does not
// have, instead of printing its own help and exiting 0.
//
// Cobra's default for a group is to be non-runnable, and a non-runnable command
// returns flag.ErrHelp — which ExecuteC turns into "print help, exit 0". So
// `lw release nosuchverb` printed the release help and reported success. Any CI
// step invoking a mistyped or not-yet-built verb went green; `lw check schema`
// on a runner without the schema did exactly that (#303).
//
// Note a group must be made runnable to fix this: cobra returns ErrHelp before
// it ever calls ValidateArgs, so setting Args alone would never fire. RunE here
// preserves the useful half of the old behaviour — bare `lw release` still
// prints help and exits 0.
//
// Groups that already declare their own Args or Run are left alone; they have
// made a deliberate choice.
func RejectUnknownSubcommands(cmd *cobra.Command) {
	for _, child := range cmd.Commands() {
		RejectUnknownSubcommands(child)
	}

	if !cmd.HasSubCommands() || cmd.Runnable() || cmd.Args != nil {
		return
	}

	cmd.Args = func(c *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}

		return fmt.Errorf("unknown command %q for %q", args[0], c.CommandPath())
	}

	cmd.RunE = func(c *cobra.Command, _ []string) error {
		return c.Help()
	}
}

// UnknownVerbError reports an unknown subcommand in argv, including when --help
// is present.
//
// RejectUnknownSubcommands above cannot cover that case, and the gap is in
// cobra rather than in it. Command.execute() reads the help flag and returns
// flag.ErrHelp BEFORE it calls ValidateArgs, and ExecuteC turns ErrHelp into
// "print help, exit 0" — so the Args validator never runs when --help is on the
// line. #342 closed the bare form and left this one:
//
//	lw runbook zzzznope           exit 1   ← Args validator fires
//	lw runbook zzzznope --help    exit 0   ← help short-circuit, validator skipped
//	lw runbook apply --help       exit 0   ← a REAL verb, byte-identical outcome
//
// That last line is why this matters more than a wrong exit code. `--help` is
// how a caller asks "does this command exist?", and the answer was the same
// whether it did or not. lightwave-cli#417 hit exactly this: probing six
// candidate verbs reported all six present, and only a nonsense control showed
// the probe was measuring nothing.
//
// Only the FIRST leftover token is examined, on purpose. Command.Find returns
// the remaining args with flags AND their values intact, so scanning all of
// them would read `lw runbook --config /tmp/x.yaml` as an unknown verb named
// "/tmp/x.yaml". Recovering the true positional set means re-parsing flags,
// and a second ParseFlags would re-append every repeatable flag (--label a
// --label b would land as four). A missed verb behind a value-taking flag is a
// false negative; inventing one out of a flag value is a false positive that
// breaks working commands. This takes the false negative.
func UnknownVerbError(root *cobra.Command, argv []string) error {
	target, rest, err := root.Find(argv)
	if err != nil {
		// cobra's own legacyArgs already rejects an unknown TOP-LEVEL command;
		// it just never reaches the caller when --help is set.
		return err
	}

	// A leaf's positional args are its own business — `lw runbook search rds`
	// is a query, not a verb.
	if !target.HasSubCommands() || len(rest) == 0 {
		return nil
	}

	if strings.HasPrefix(rest[0], "-") {
		return nil
	}

	return fmt.Errorf("unknown command %q for %q", rest[0], target.CommandPath())
}
