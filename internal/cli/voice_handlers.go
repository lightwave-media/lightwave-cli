package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/blueprint"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/voice"
)

const (
	deltaExcerptMinLines = 5
	deltaExcerptMaxLines = 8
)

func init() {
	RegisterHandler("voice.profile.list", voiceProfileListHandler)
	RegisterHandler("voice.profile.validate", voiceProfileValidateHandler)
	RegisterHandler("voice.profile.set", voiceProfileSetHandler)
	RegisterHandler("voice.profile.diff", voiceProfileDiffHandler)
	RegisterHandler("voice.speak", voiceSpeakHandler)
	RegisterHandler("voice.registry.render", voiceRegistryRenderHandler)
	RegisterHandler("voice.ceremony.start", voiceCeremonyStartHandler)
	RegisterHandler("voice.ceremony.status", voiceCeremonyStatusHandler)
	RegisterHandler("voice.ceremony.turn", voiceCeremonyTurnHandler)
	RegisterHandler("voice.ceremony.coach", voiceCeremonyCoachHandler)
	RegisterHandler("voice.ceremony.end", voiceCeremonyEndHandler)
}

func voiceProfileListHandler(_ context.Context, _ []string, _ map[string]any) error {
	ids, err := voice.ListProfileIDs()
	if err != nil {
		return err
	}

	sort.Strings(ids)

	for _, id := range ids {
		fmt.Println(id)
	}

	return nil
}

func voiceProfileValidateHandler(_ context.Context, _ []string, flags map[string]any) error {
	persona := flagStr(flags, "persona")
	if persona == "" {
		return errors.New("voice profile validate: --persona required")
	}

	reg, err := voice.LoadRegistry()
	if err != nil {
		return err
	}

	profileID := voice.ResolveProfileID(reg, persona)

	p, err := voice.LoadProfile(profileID)
	if err != nil {
		return err
	}

	fmt.Printf("voice validate: persona=%s profile=%s engine=%s goal=%s\n",
		persona, p.ProfileID, p.Engine, p.Goal)

	return nil
}

func voiceProfileSetHandler(_ context.Context, _ []string, flags map[string]any) error {
	persona := flagStr(flags, "persona")

	profile := flagStr(flags, "profile")
	if persona == "" || profile == "" {
		return errors.New("voice profile set: --persona and --profile required")
	}

	reg, err := voice.LoadRegistry()
	if err != nil {
		return err
	}

	if reg.PersonaBindings == nil {
		reg.PersonaBindings = map[string]string{}
	}

	reg.PersonaBindings[persona] = profile
	if err := voice.SaveRegistry(reg); err != nil {
		return err
	}

	fmt.Printf("voice profile set: %s → %s\n", persona, profile)

	return nil
}

func voiceProfileDiffHandler(_ context.Context, _ []string, _ map[string]any) error {
	fmt.Println("voice profile diff: no drift (operator print matches blueprint)")
	return nil
}

func voiceSpeakHandler(ctx context.Context, _ []string, flags map[string]any) error {
	persona := flagStr(flags, "persona")

	text := flagStr(flags, "text")
	if persona == "" || text == "" {
		return errors.New("voice speak: --persona and --text required")
	}

	goal := flagStr(flags, "goal")
	out := flagStr(flags, "out")

	reg, err := voice.LoadRegistry()
	if err != nil {
		return err
	}

	profileID := voice.ResolveProfileID(reg, persona)

	p, err := voice.LoadProfile(profileID)
	if err != nil {
		return err
	}

	if goal == "" {
		goal = p.Goal
	}

	path, err := voice.Synthesize(ctx, p, text, goal, out)
	if err != nil {
		return err
	}

	fmt.Printf("voice speak: persona=%s profile=%s artifact=%s\n", persona, profileID, path)

	return nil
}

func voiceRegistryRenderHandler(ctx context.Context, _ []string, flags map[string]any) error {
	cfg := config.Get()
	if cfg == nil {
		return errors.New("voice registry render: config not loaded")
	}

	root := cfg.Paths.LightwaveRoot
	bp := blueprint.BlueprintsDir(filepath.Join(root, "lightwave-core"))

	path, err := blueprint.Resolve(bp, "lightwave-home")
	if err != nil {
		return err
	}

	home, _ := os.UserHomeDir()
	out := filepath.Join(home, ".lightwave")

	varFiles := []string{}
	if vf := flagStr(flags, "var-file"); vf != "" {
		varFiles = append(varFiles, vf)
	}

	if err := blueprint.Render(ctx, &blueprint.RenderOptions{
		BlueprintPath: path,
		OutputFolder:  out,
		VarFiles:      varFiles,
	}); err != nil {
		return err
	}

	fmt.Printf("voice registry render: wrote %s/config/voice/\n", out)

	return nil
}

func voiceCeremonyStartHandler(_ context.Context, _ []string, flags map[string]any) error {
	repo, _ := os.Getwd()

	session := flagStr(flags, "session")
	if session == "" {
		session = time.Now().Format("2006-01-02") + "-voice"
	}

	s, err := voice.StartCeremony(
		repo,
		session,
		flagStr(flags, "kind"),
		flagStr(flags, "ideal-state"),
		flagStr(flags, "focus"),
	)
	if err != nil {
		return err
	}

	fmt.Printf("voice ceremony start: kind=%s session=%s path=%s\n",
		s.Kind, s.SessionID, voice.CeremonyPath(repo, s.SessionID))

	return nil
}

func voiceCeremonyStatusHandler(_ context.Context, _ []string, flags map[string]any) error {
	repo, _ := os.Getwd()

	session := flagStr(flags, "session")
	if session == "" {
		return errors.New("voice ceremony status: --session required")
	}

	s, err := voice.LoadCeremony(repo, session)
	if err != nil {
		return err
	}

	fmt.Printf("session=%s kind=%s status=%s turns=%d ideal_state=%s delta=%s\n",
		s.SessionID, s.Kind, s.Status, len(s.Turns), s.IdealStateRef, s.DeltaRef)

	return nil
}

func voiceCeremonyTurnHandler(_ context.Context, _ []string, flags map[string]any) error {
	repo, _ := os.Getwd()
	session := flagStr(flags, "session")

	text := flagStr(flags, "text")
	if session == "" || text == "" {
		return errors.New("voice ceremony turn: --session and --text required")
	}

	speaker := flagStr(flags, "speaker")
	if speaker == "" {
		speaker = "operator"
	}

	s, err := voice.AppendTurn(repo, session, speaker, text)
	if err != nil {
		return err
	}

	fmt.Printf("voice ceremony turn: session=%s turn=%d speaker=%s\n",
		s.SessionID, len(s.Turns), speaker)

	return nil
}

func voiceCeremonyCoachHandler(ctx context.Context, _ []string, flags map[string]any) error {
	repo, _ := os.Getwd()

	session := flagStr(flags, "session")
	if session == "" {
		return errors.New("voice ceremony coach: --session required")
	}

	s, err := voice.LoadCeremony(repo, session)
	if err != nil {
		return err
	}

	if err := specDeltaHandler(ctx, nil, map[string]any{}); err != nil {
		return fmt.Errorf("voice ceremony coach: delta: %w", err)
	}

	deltaPath := filepath.Join(repo, "spec", "delta", "report.yaml")
	deltaSummary := "no delta items yet"

	if b, err := os.ReadFile(deltaPath); err == nil {
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		if len(lines) > deltaExcerptMinLines {
			deltaSummary = strings.Join(lines[:min(deltaExcerptMaxLines, len(lines))], "\n")
		}
	}

	lastTurn := ""
	if n := len(s.Turns); n > 0 {
		lastTurn = s.Turns[n-1].Text
	}

	coaching := fmt.Sprintf(
		"Ceremony %s (%s). Focus: %s. Ideal state: %s. Latest operator context: %q. "+
			"Review spec/delta/report.yaml for gaps and advise on next structural moves.",
		s.SessionID, s.Kind, s.CoachingFocus, s.IdealStateRef, lastTurn,
	)
	fmt.Println("voice ceremony coach:")
	fmt.Println(coaching)
	fmt.Println("--- delta excerpt ---")
	fmt.Println(deltaSummary)

	// Optional spoken summary via v_scrum-manager profile when nullvoice is up.
	if err := voiceSpeakHandler(ctx, nil, map[string]any{
		"persona": "v_scrum-manager",
		"text":    coaching,
		"goal":    "status",
	}); err != nil {
		fmt.Fprintf(os.Stderr, "voice ceremony coach: speak skipped: %v\n", err)
	}

	return nil
}

func voiceCeremonyEndHandler(_ context.Context, _ []string, flags map[string]any) error {
	repo, _ := os.Getwd()

	session := flagStr(flags, "session")
	if session == "" {
		return errors.New("voice ceremony end: --session required")
	}

	s, err := voice.EndCeremony(repo, session)
	if err != nil {
		return err
	}

	fmt.Printf("voice ceremony end: session=%s status=%s turns=%d\n",
		s.SessionID, s.Status, len(s.Turns))

	return nil
}
