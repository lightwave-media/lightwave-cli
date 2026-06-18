package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func init() {
	RegisterHandler("failure.record", failureRecordHandler)
	RegisterHandler("failure.file", failureFileHandler)
	RegisterHandler("failure.status", failureStatusHandler)
}

func failureRecordHandler(_ context.Context, _ []string, flags map[string]any) error {
	home, _ := os.UserHomeDir()

	dir := filepath.Join(home, ".lightwave", "observability", "failures")
	if err := os.MkdirAll(dir, codegenDirPerm); err != nil {
		return err
	}

	kind := flagStr(flags, "kind")
	if kind == "" {
		kind = "tool-gap"
	}

	rec := map[string]any{
		"id":          fmt.Sprintf("fail-%d", time.Now().Unix()),
		"kind":        kind,
		"summary":     flagStr(flags, "summary"),
		"detected_at": time.Now().UTC().Format(time.RFC3339),
		"fsm_state":   "triage",
	}

	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, "last-record.json"), append(b, '\n'), reportFileMode)
}

func failureFileHandler(ctx context.Context, _ []string, flags map[string]any) error {
	fmt.Println("failure file: would create GitHub issue with label status:triage (stub)")
	return failureRecordHandler(ctx, nil, flags)
}

func failureStatusHandler(_ context.Context, _ []string, _ map[string]any) error {
	fmt.Println("failure status: triage")
	return nil
}
