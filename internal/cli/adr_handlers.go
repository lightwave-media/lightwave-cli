package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/adr"
	"github.com/lightwave-media/lightwave-cli/internal/config"
)

func init() {
	RegisterHandler("adr.new", adrNewHandler)
}

// adrNewHandler reserves the next ADR id and writes its skeleton.
//
// Declared in lightwave-core's interfaces/cli/commands.yaml as
// `adr new <title> --tree --supersedes --dry-run --json`, `_status:
// in_development` with this issue as its tracking ref. Every flag read here is
// one the stamp declares — reading an undeclared flag is the #367 defect, where
// the dispatcher never registers it and the read silently returns the default,
// so the user is ignored with no error.
func adrNewHandler(_ context.Context, args []string, flags map[string]any) error {
	title := ""
	if len(args) > 0 {
		title = args[0]
	}

	if title == "" {
		return errors.New("usage: lw adr new \"<title>\" --tree core|host")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}

	cfg := config.Get()

	tree, err := adr.TreeFor(flagStr(flags, "tree"), cfg.Paths.LightwaveRoot, home)
	if err != nil {
		return err
	}

	// The area is the corpus's own repo. It is a required field of the ADR data
	// schema and there is exactly one right answer per tree, so it is derived
	// rather than asked for — an unstamped --area flag would be read and
	// silently dropped by the dispatcher.
	area := "lightwave-core"
	if tree.Name == "host" {
		area = "lightwave-harness"
	}

	res, err := tree.Reserve(
		title,
		area,
		flagStr(flags, "supersedes"),
		time.Now(),
		flagBool(flags, "dry-run"),
	)
	if err != nil {
		return err
	}

	if flagBool(flags, "json") {
		out, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return err
		}

		fmt.Println(string(out))

		return nil
	}

	if res.DryRun {
		fmt.Printf("would reserve %s\n", res.ID)
		fmt.Printf("  path:   %s\n", res.Path)
		fmt.Printf("  status: %s\n", res.Status)
		fmt.Println("  (dry run — no id consumed, no file written)")

		return nil
	}

	fmt.Printf("reserved %s\n", res.ID)
	fmt.Printf("  path:   %s\n", res.Path)
	fmt.Printf("  status: %s\n", res.Status)
	fmt.Println("  Fill Context, Decision and Consequences, then: lw docs spec-lint")

	return nil
}
