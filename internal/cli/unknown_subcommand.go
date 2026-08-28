package cli

import (
	"fmt"

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
