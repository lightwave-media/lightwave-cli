package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/lightwave-media/lightwave-cli/internal/repo"
)

var errSysDevTauri = errors.New("dev/run-tauri-dev.sh not found for lightwave-sys")

func init() {
	RegisterHandler("local.tauri", localTauriHandler)
}

// localTauriHandler is a thin wrapper around
// lightwave-sys/dev/run-tauri-dev.sh — boots the ZeroClaw desktop shell
// in dev mode. Not part of the gate composite (it's interactive); just a
// convenience so the persona's "every dev verb under one namespace"
// promise covers tauri too.
func localTauriHandler(ctx context.Context, _ []string, _ map[string]any) error {
	info, err := detectRepo()
	if err != nil {
		return err
	}

	if info.ID != repo.LightwaveSys {
		stageNotImplemented("tauri", info)
		return nil
	}

	script := sysDevScript(info, "run-tauri-dev.sh")
	if script == "" {
		return errSysDevTauri
	}

	if err := runStreaming(ctx, info.Root, script); err != nil {
		return fmt.Errorf("run-tauri-dev.sh: %w", err)
	}

	return nil
}
