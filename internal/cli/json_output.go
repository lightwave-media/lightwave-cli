package cli

import (
	"encoding/json"
	"os"
)

// emitJSON writes a value to stdout as indented JSON. Shared by every
// subcommand that supports a `--json` flag.
func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
