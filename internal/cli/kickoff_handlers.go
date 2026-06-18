package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/kickoff"
)

func init() {
	RegisterHandler("kickoff.start", kickoffStartHandler)
	RegisterHandler("kickoff.status", kickoffStatusHandler)
	RegisterHandler("kickoff.answer", kickoffAnswerHandler)
	RegisterHandler("kickoff.finalize", kickoffFinalizeHandler)
}

func kickoffStartHandler(_ context.Context, _ []string, flags map[string]any) error {
	repo, _ := os.Getwd()

	session := flagStr(flags, "session")
	if session == "" {
		session = time.Now().Format("2006-01-02") + "-session"
	}

	runbook := flagStr(flags, "runbook")
	if runbook == "" {
		runbook = "kickoff-initiative"
	}

	if err := kickoff.Start(repo, session, runbook, "operator"); err != nil {
		return err
	}

	fmt.Printf("kickoff started: %s\n", filepath.Join(repo, ".tasks", session, "kickoff"))

	return nil
}

func kickoffStatusHandler(_ context.Context, _ []string, flags map[string]any) error {
	repo, _ := os.Getwd()

	session := flagStr(flags, "session")
	if session == "" {
		return errors.New("kickoff status: --session required")
	}

	g, err := kickoff.ReadGate(repo, session)
	if err != nil {
		return err
	}

	if flagBool(flags, "require-finalized") && !g.KickoffOK {
		return errors.New("kickoff not finalized")
	}

	fmt.Printf("session=%s kickoff_ok=%v status=%s round=%s\n", session, g.KickoffOK, g.Status, g.Round)

	return nil
}

func kickoffAnswerHandler(_ context.Context, _ []string, flags map[string]any) error {
	repo, _ := os.Getwd()
	session := flagStr(flags, "session")
	round := flagStr(flags, "round")
	jsonPayload := flagStr(flags, "json")
	dir := kickoff.KickoffDir(repo, session)

	appendPath := filepath.Join(dir, fmt.Sprintf("turn-%s.json", round))
	if err := os.MkdirAll(dir, codegenDirPerm); err != nil {
		return err
	}

	return os.WriteFile(appendPath, []byte(jsonPayload), reportFileMode)
}

func kickoffFinalizeHandler(_ context.Context, _ []string, flags map[string]any) error {
	repo, _ := os.Getwd()
	session := flagStr(flags, "session")

	return kickoff.Finalize(repo, session)
}
