package main

import (
	"fmt"
	"os"

	"github.com/lightwave-media/lightwave-cli/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "lw: %v\n", err)
		os.Exit(1)
	}
}
