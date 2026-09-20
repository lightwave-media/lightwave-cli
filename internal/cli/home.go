package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

var homeCmd = &cobra.Command{
	Use:   "home",
	Short: "Render or validate ~/.lightwave from the lightwave-home blueprint",
	RunE: func(cmd *cobra.Command, args []string) error {
		out, _ := cmd.Flags().GetString("out")
		if out == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			out = filepath.Join(home, ".lightwave")
		}
		coreRoot := os.Getenv("LIGHTWAVE_CORE")
		if coreRoot == "" {
			coreRoot = filepath.Join(os.Getenv("HOME"), "dev", "lightwave-core")
		}
		tpl := filepath.Join(coreRoot, "src", "boilerplate", "blueprints", "lightwave-home")
		// Prefer mise from the core checkout so boilerplate is activated via mise.toml.
		c := exec.Command("mise", "exec", "--", "boilerplate",
			"--template-url", tpl,
			"--output-folder", out,
			"--non-interactive",
			"--var", "home_dir="+out,
			"--var", "dev_root="+filepath.Join(os.Getenv("HOME"), "dev"),
			"--var", "lightwave_ai_root="+filepath.Join(os.Getenv("HOME"), "dev", "lightwave-ai"),
			"--var", "owner=Joel",
		)
		c.Dir = coreRoot
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			return fmt.Errorf("lw home: %w", err)
		}
		fmt.Println("lightwave-home rendered →", out)
		return nil
	},
}

func init() {
	homeCmd.Flags().String("out", "", "Output directory (default ~/.lightwave)")
}
