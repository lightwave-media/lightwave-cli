package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/kickoff"
)

func init() {
	RegisterHandler("session.spawn", sessionSpawnHandler)
	RegisterHandler("session.shutdown", sessionShutdownHandler)
	RegisterHandler("session.verify", sessionVerifyHandler)
	RegisterHandler("session.promote", sessionPromoteHandler)
}

func sessionSpawnHandler(_ context.Context, _ []string, flags map[string]any) error {
	repo, _ := os.Getwd()

	session := flagStr(flags, "session")
	if err := kickoff.RequireFinalized(repo, session); err != nil {
		return err
	}

	dir := filepath.Join(repo, ".tasks", session)
	if err := os.MkdirAll(dir, codegenDirPerm); err != nil {
		return err
	}

	spawn := map[string]any{
		"spawn_ok":   true,
		"spawned_at": time.Now().UTC().Format(time.RFC3339),
		"persona":    flagStr(flags, "persona"),
		"task_id":    flagStr(flags, "task"),
		"session_id": session,
	}

	b, err := json.MarshalIndent(spawn, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, "spawn.json"), append(b, '\n'), reportFileMode)
}

func sessionShutdownHandler(_ context.Context, _ []string, flags map[string]any) error {
	repo, _ := os.Getwd()
	session := flagStr(flags, "session")
	dir := filepath.Join(repo, ".tasks", session)
	shutdown := map[string]any{
		"shutdown_ok": true,
		"shutdown_at": time.Now().UTC().Format(time.RFC3339),
	}

	b, err := json.MarshalIndent(shutdown, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(dir, "shutdown.json"), append(b, '\n'), reportFileMode); err != nil {
		return err
	}

	fmt.Println("session shutdown: ok")

	return nil
}

func sessionVerifyHandler(_ context.Context, _ []string, flags map[string]any) error {
	repo, _ := os.Getwd()

	session := flagStr(flags, "session")
	if flagBool(flags, "require-bookends") {
		if _, err := os.Stat(filepath.Join(repo, ".tasks", session, "spawn.json")); err != nil {
			return errors.New("missing spawn.json")
		}

		if _, err := os.Stat(filepath.Join(repo, ".tasks", session, "shutdown.json")); err != nil {
			return errors.New("missing shutdown.json")
		}
	}

	fmt.Println("session verify: ok")

	return nil
}

func sessionPromoteHandler(_ context.Context, _ []string, _ map[string]any) error {
	fmt.Println("session promote: stub (copy to spec/agile/tasks/)")
	return nil
}
