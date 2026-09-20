package cli

import (
	"encoding/json"
	"os"

	lightwavecore "github.com/lightwave-media/lightwave-core"
	"github.com/spf13/cobra"
)

var schemaCmd = &cobra.Command{
	Use:   "schema [name]",
	Short: "Load schemas from the lightwave-core GitHub module",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		list, _ := cmd.Flags().GetBool("list")
		if list || len(args) == 0 {
			names, err := lightwavecore.ListSchemas()
			if err != nil {
				return err
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(names)
		}
		doc, err := lightwavecore.LoadSchema(args[0])
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	},
}

func init() {
	schemaCmd.Flags().Bool("list", false, "List registered schema paths")
}
