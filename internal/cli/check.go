package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Run local validity / CI-parity checks for the current repo",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Prefer mise ci when present; else pytest on lightwave-core shape.
		if _, err := exec.LookPath("mise"); err == nil {
			c := exec.Command("mise", "run", "ci")
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			return c.Run()
		}
		return fmt.Errorf("lw check: mise not found; install mise or run repo CI directly")
	},
}
