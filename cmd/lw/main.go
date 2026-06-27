package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/lightwave-media/lightwave-cli/internal/cli"
	"github.com/lightwave-media/lightwave-cli/internal/db"
)

const exitDBUnavailable = 3

func main() {
	if err := cli.Execute(); err != nil {
		if errors.Is(err, db.ErrDBUnavailable) {
			// Exit exitDBUnavailable: platform DB is down — SOPs should treat
			// this as "skip and retry later", not a hard failure.
			fmt.Fprintf(os.Stderr, "lw: platform database unavailable — skipping\n")
			os.Exit(exitDBUnavailable)
		}
		fmt.Fprintf(os.Stderr, "lw: %v\n", err)
		os.Exit(1)
	}
}
