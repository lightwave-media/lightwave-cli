// Package main is the v2 empty-tree `lw` entrypoint.
// Verbs: home, schema, check, mcp. Schemas load from lightwave-core (GitHub module).
package main

import (
	"fmt"
	"os"

	"github.com/lightwave-media/lightwave-cli/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
