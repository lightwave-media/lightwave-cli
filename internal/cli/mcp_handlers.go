package cli

import (
	"context"
	"os"

	"github.com/lightwave-media/lightwave-cli/internal/mcp"
)

func init() {
	RegisterHandler("mcp.serve", mcpServeHandler)
}

func mcpServeHandler(ctx context.Context, _ []string, flags map[string]any) error {
	home, _ := os.UserHomeDir()

	return mcp.Serve(ctx, os.Stdin, os.Stdout, mcp.Server{
		Persona: flagStr(flags, "persona"),
		HomeDir: home,
	})
}
