package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/mcp"
)

func init() {
	RegisterHandler("mcp.serve", mcpServeHandler)
	RegisterHandler("mcp.start", mcpStartHandler)
	RegisterHandler("mcp.status", mcpStatusHandler)
	RegisterHandler("mcp.logs", mcpLogsHandler)
	RegisterHandler("mcp.stop", mcpStopHandler)
	RegisterHandler("mcp.restart", mcpRestartHandler)
}

func mcpServeHandler(ctx context.Context, _ []string, flags map[string]any) error {
	return mcp.Serve(ctx, os.Stdin, os.Stdout, mcpServerFromFlags(flags))
}

func mcpStartHandler(ctx context.Context, _ []string, flags map[string]any) error {
	rt := mcpRuntimeFromFlags(flags)

	st, err := rt.Start(ctx, mcpServerFromFlags(flags), mcp.StartOpts{
		Foreground: flagBool(flags, "foreground"),
		DryRun:     flagBool(flags, "dry-run"),
	})
	if err != nil {
		return err
	}

	return mcp.WriteStatus(st, flagBool(flags, "json"))
}

func mcpStatusHandler(ctx context.Context, _ []string, flags map[string]any) error {
	return mcp.WriteStatus(mcpRuntimeFromFlags(flags).Status(ctx), flagBool(flags, "json"))
}

func mcpLogsHandler(ctx context.Context, _ []string, flags map[string]any) error {
	rt := mcpRuntimeFromFlags(flags)
	tail := mcp.DefaultLogTail

	if raw := flagStr(flags, "tail"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return errors.New("--tail must be a positive integer")
		}

		tail = n
	}

	body, err := rt.ReadLogs(tail)
	if err != nil {
		return err
	}

	if flagBool(flags, "json") {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")

		return enc.Encode(struct {
			Text string `json:"text"`
			File string `json:"path"`
		}{File: rt.Status(ctx).LogFile, Text: body})
	}

	fmt.Print(body)

	return nil
}

func mcpStopHandler(ctx context.Context, _ []string, flags map[string]any) error {
	if err := confirmMCPDestruct(flags, "Stop the managed MCP listener?"); err != nil {
		return err
	}

	st, err := mcpRuntimeFromFlags(flags).Stop(ctx, flagBool(flags, "dry-run"))
	if err != nil {
		return err
	}

	return mcp.WriteStatus(st, flagBool(flags, "json"))
}

func mcpRestartHandler(ctx context.Context, _ []string, flags map[string]any) error {
	if err := confirmMCPDestruct(flags, "Restart the managed MCP listener?"); err != nil {
		return err
	}

	rt := mcpRuntimeFromFlags(flags)
	if _, err := rt.Stop(ctx, flagBool(flags, "dry-run")); err != nil {
		return err
	}

	st, err := rt.Start(ctx, mcpServerFromFlags(flags), mcp.StartOpts{
		DryRun: flagBool(flags, "dry-run"),
	})
	if err != nil {
		return err
	}

	return mcp.WriteStatus(st, flagBool(flags, "json"))
}

func confirmMCPDestruct(flags map[string]any, question string) error {
	if flagBool(flags, "dry-run") || flagBool(flags, "yes") {
		return nil
	}

	if promptYesNo(question) {
		return nil
	}

	fmt.Println("Cancelled")

	return nil
}

func mcpServerFromFlags(flags map[string]any) mcp.Server {
	home, _ := os.UserHomeDir()

	return mcp.Server{
		Persona:  flagStr(flags, "persona"),
		HomeDir:  home,
		CoreRoot: mcpCoreRoot(),
	}
}

func mcpRuntimeFromFlags(flags map[string]any) mcp.Runtime {
	home, _ := os.UserHomeDir()

	return mcp.Runtime{
		HomeDir: home,
		Listen:  flagStr(flags, "listen"),
	}
}

func mcpCoreRoot() string {
	home, _ := os.UserHomeDir()
	fallback := filepath.Join(home, "dev", "lightwave-core")

	cfg := config.Get()
	if cfg == nil || cfg.Paths.LightwaveRoot == "" {
		return fallback
	}

	return filepath.Join(cfg.Paths.LightwaveRoot, "lightwave-core")
}
