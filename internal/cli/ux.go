package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/db"
	"github.com/lightwave-media/lightwave-cli/internal/paperclip"
	"github.com/lightwave-media/lightwave-cli/internal/ux"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var uxCmd = &cobra.Command{
	Use:   "ux",
	Short: "UX recording and feedback loop",
	Long: `Record screen + mic while navigating LightWave products, then
analyze the recording with Claude for UX improvements.

Examples:
  lw ux init                   # Set up devices and download whisper model
  lw ux start --name "Homepage review"
  lw ux stop
  lw ux analyze
  lw ux items
  lw ux list`,
}

// ── init ────────────────────────────────────────────────────────────────

var uxInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Set up devices and download whisper model",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Check ffmpeg
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			return fmt.Errorf("ffmpeg not found — install with: brew install ffmpeg")
		}
		fmt.Printf("%s ffmpeg found\n", color.GreenString("✓"))

		// Check whisper-cli
		if _, err := exec.LookPath("whisper-cli"); err != nil {
			return fmt.Errorf("whisper-cli not found — install with: brew install whisper-cpp")
		}
		fmt.Printf("%s whisper-cli found\n", color.GreenString("✓"))

		// Device selection
		_, err := ux.PromptDeviceSelection()
		if err != nil {
			return fmt.Errorf("device setup: %w", err)
		}
		fmt.Printf("%s Devices configured\n", color.GreenString("✓"))

		// Download whisper model if needed
		modelPath := ux.DefaultWhisperModelPath()
		if _, err := os.Stat(modelPath); os.IsNotExist(err) {
			fmt.Println("\nDownloading whisper model (ggml-base.en.bin, ~142MB)...")
			if err := downloadWhisperModel(modelPath); err != nil {
				return fmt.Errorf("download model: %w", err)
			}
			fmt.Printf("%s Whisper model downloaded\n", color.GreenString("✓"))
		} else {
			fmt.Printf("%s Whisper model already present\n", color.GreenString("✓"))
		}

		fmt.Printf("\n%s UX recording ready. Run: lw ux start\n", color.GreenString("✓"))
		return nil
	},
}

// ── start ───────────────────────────────────────────────────────────────

var uxStartName string

var uxStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start recording screen + microphone",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Check for already-active session
		active, _ := ux.FindActiveSession()
		if active != nil {
			return fmt.Errorf("session %s is already recording — run 'lw ux stop' first", active.ID)
		}

		cfg, err := ux.EnsureConfig()
		if err != nil {
			return err
		}

		session, err := ux.CreateSession(uxStartName, cfg.Screen, cfg.AudioDevice)
		if err != nil {
			return fmt.Errorf("create session: %w", err)
		}

		if err := ux.StartRecording(session); err != nil {
			return fmt.Errorf("start recording: %w", err)
		}

		fmt.Printf("%s Recording started\n", color.GreenString("✓"))
		fmt.Printf("   Session: %s\n", color.CyanString(session.ID))
		if session.Name != "" {
			fmt.Printf("   Name:    %s\n", session.Name)
		}
		fmt.Printf("   Screen:  %d\n", session.Screen)
		fmt.Printf("   Audio:   %d\n", session.AudioDevice)
		fmt.Printf("   Logs:    backend + frontend (docker)\n")
		fmt.Printf("\n   Run %s to stop recording.\n", color.YellowString("lw ux stop"))

		return nil
	},
}

// ── stop ────────────────────────────────────────────────────────────────

var uxStopCmd = &cobra.Command{
	Use:   "stop [session-id]",
	Short: "Stop the active recording",
	RunE: func(cmd *cobra.Command, args []string) error {
		var session *ux.Session
		var err error

		if len(args) > 0 {
			session, err = ux.LoadSession(args[0])
		} else {
			session, err = ux.FindActiveSession()
		}
		if err != nil {
			return err
		}
		if session == nil {
			return fmt.Errorf("no active recording found")
		}
		if session.Status != ux.StatusRecording {
			return fmt.Errorf("session %s is not recording (status: %s)", session.ID, session.Status)
		}

		fmt.Println("Stopping recording...")
		if err := ux.StopRecording(session); err != nil {
			return fmt.Errorf("stop recording: %w", err)
		}

		fmt.Printf("%s Recording stopped\n", color.GreenString("✓"))
		fmt.Printf("   Session:  %s\n", color.CyanString(session.ID))
		fmt.Printf("   Duration: %s\n", ux.FormatDuration(session.DurationSecs))
		fmt.Printf("   File:     %s\n", ux.RecordingPath(session.ID))
		fmt.Printf("\n   Run %s to analyze.\n", color.YellowString("lw ux analyze"))

		return nil
	},
}

// ── analyze ─────────────────────────────────────────────────────────────

var uxAnalyzeForce bool

var uxAnalyzeCmd = &cobra.Command{
	Use:   "analyze [session-id]",
	Short: "Transcribe and analyze a recording with Claude",
	RunE: func(cmd *cobra.Command, args []string) error {
		var session *ux.Session
		var err error

		if len(args) > 0 {
			session, err = ux.LoadSession(args[0])
		} else {
			session, err = ux.LatestSession()
		}
		if err != nil {
			return err
		}

		// Guard: refuse to re-analyze unless --force
		if session.Status == ux.StatusAnalyzed && !uxAnalyzeForce {
			return fmt.Errorf("session %s already analyzed — use --force to re-analyze", session.ID)
		}

		fmt.Printf("Analyzing session %s...\n", color.CyanString(session.ID))

		if err := ux.Analyze(session.ID); err != nil {
			return err
		}

		// Display results
		items, err := ux.LoadItems(session.ID)
		if err != nil {
			fmt.Printf("%s Analysis complete (see %s)\n", color.GreenString("✓"), ux.AnalysisPath(session.ID))
			return nil
		}

		fmt.Printf("\n%s Found %d improvement items:\n\n", color.GreenString("✓"), len(items))
		printItems(items)

		return nil
	},
}

// ── delete ──────────────────────────────────────────────────────────────

var uxDeleteForce bool

var uxDeleteCmd = &cobra.Command{
	Use:   "delete <session-id>",
	Short: "Delete a UX recording session",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		session, err := ux.LoadSession(args[0])
		if err != nil {
			return err
		}

		if session.Status == ux.StatusRecording {
			return fmt.Errorf("session %s is still recording — stop it first with 'lw ux stop %s'", session.ID, session.ID)
		}

		if session.Status == ux.StatusAnalyzed && !uxDeleteForce {
			return fmt.Errorf("session %s has analysis data — use --force to delete", session.ID)
		}

		if err := ux.DeleteSession(session.ID); err != nil {
			return fmt.Errorf("failed to delete session: %w", err)
		}

		fmt.Printf("%s Deleted session %s\n", color.GreenString("✓"), session.ID)
		return nil
	},
}

// ── list ────────────────────────────────────────────────────────────────

var uxListCmd = &cobra.Command{
	Use:   "list",
	Short: "List UX recording sessions",
	RunE: func(cmd *cobra.Command, args []string) error {
		sessions, err := ux.ListSessions()
		if err != nil {
			return err
		}

		if len(sessions) == 0 {
			fmt.Println(color.YellowString("No sessions found. Run: lw ux start"))
			return nil
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"ID", "Name", "Status", "Duration"})
		table.SetBorder(false)

		for _, s := range sessions {
			name := s.Name
			if name == "" {
				name = "-"
			}
			duration := "-"
			if s.DurationSecs > 0 {
				duration = ux.FormatDuration(s.DurationSecs)
			}

			statusColor := color.YellowString
			switch s.Status {
			case ux.StatusRecording:
				statusColor = color.RedString
			case ux.StatusStopped:
				statusColor = color.YellowString
			case ux.StatusAnalyzed:
				statusColor = color.GreenString
			}

			table.Append([]string{s.ID, name, statusColor(s.Status), duration})
		}

		table.Render()
		return nil
	},
}

// ── items ───────────────────────────────────────────────────────────────

var uxItemsCmd = &cobra.Command{
	Use:   "items [session-id]",
	Short: "Show improvement items from analysis",
	RunE: func(cmd *cobra.Command, args []string) error {
		var session *ux.Session
		var err error

		if len(args) > 0 {
			session, err = ux.LoadSession(args[0])
		} else {
			session, err = ux.LatestSession()
		}
		if err != nil {
			return err
		}

		if session.Status != ux.StatusAnalyzed {
			return fmt.Errorf("session %s has not been analyzed yet — run 'lw ux analyze %s'", session.ID, session.ID)
		}

		items, err := ux.LoadItems(session.ID)
		if err != nil {
			return err
		}

		if len(items) == 0 {
			fmt.Println(color.YellowString("No improvement items found."))
			return nil
		}

		printItems(items)
		return nil
	},
}

// ── play ────────────────────────────────────────────────────────────────

var uxPlayCmd = &cobra.Command{
	Use:   "play [session-id]",
	Short: "Open recording in default video player",
	RunE: func(cmd *cobra.Command, args []string) error {
		var session *ux.Session
		var err error

		if len(args) > 0 {
			session, err = ux.LoadSession(args[0])
		} else {
			session, err = ux.LatestSession()
		}
		if err != nil {
			return err
		}

		path := ux.RecordingPath(session.ID)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return fmt.Errorf("recording not found: %s", path)
		}

		return exec.Command("open", path).Run()
	},
}

// ── devices ─────────────────────────────────────────────────────────────

var uxDevicesCmd = &cobra.Command{
	Use:   "devices",
	Short: "List available capture devices",
	RunE: func(cmd *cobra.Command, args []string) error {
		video, audio, err := ux.ListDevices()
		if err != nil {
			return err
		}

		fmt.Println(color.CyanString("Video devices:"))
		for _, d := range video {
			fmt.Printf("  [%d] %s\n", d.Index, d.Name)
		}
		fmt.Println()
		fmt.Println(color.CyanString("Audio devices:"))
		for _, d := range audio {
			fmt.Printf("  [%d] %s\n", d.Index, d.Name)
		}

		return nil
	},
}

// ── backlog ──────────────────────────────────────────────────────────

var (
	uxBacklogEpic   string
	uxBacklogSprint string
)

var uxBacklogCmd = &cobra.Command{
	Use:   "backlog [session-id]",
	Short: "Create backlog tasks from analyzed improvement items",
	Long: `Reads improvement items from an analyzed UX session and creates
tasks in the LightWave backlog.

Severity → Priority:  critical→p1_urgent, major→p2_high, minor→p3_medium
Category → Type:      bug/performance→fix, usability/design/feature_request→feature, content→chore`,
	RunE: func(cmd *cobra.Command, args []string) error {
		var session *ux.Session
		var err error

		if len(args) > 0 {
			session, err = ux.LoadSession(args[0])
		} else {
			session, err = ux.LatestSession()
		}
		if err != nil {
			return err
		}

		if session.Status != ux.StatusAnalyzed {
			return fmt.Errorf("session %s has not been analyzed yet — run 'lw ux analyze %s'", session.ID, session.ID)
		}

		items, err := ux.LoadItems(session.ID)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			fmt.Println(color.YellowString("No improvement items to create tasks from."))
			return nil
		}

		ctx := context.Background()
		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		fmt.Printf("Creating %d tasks from session %s...\n\n", len(items), color.CyanString(session.ID))

		for _, item := range items {
			opts := db.TaskCreateOptions{
				Title:       item.Description,
				Description: formatItemDescription(item, session.ID),
				Priority:    mapSeverityToPriority(item.Severity),
				TaskType:    mapCategoryToType(item.Category),
				EpicID:      uxBacklogEpic,
				SprintID:    uxBacklogSprint,
			}

			task, err := db.CreateTask(ctx, pool, opts)
			if err != nil {
				fmt.Printf("  %s Failed: %s — %v\n", color.RedString("✗"), item.Description, err)
				continue
			}

			fmt.Printf("  %s %s  %s\n", color.GreenString("✓"), color.YellowString(task.ShortID), task.Title)
		}

		fmt.Printf("\nDone. Run %s to see the backlog.\n", color.YellowString("lw task list"))
		return nil
	},
}

func mapSeverityToPriority(severity string) string {
	switch severity {
	case "critical":
		return "p1_urgent"
	case "major":
		return "p2_high"
	case "minor":
		return "p3_medium"
	default:
		return "p3_medium"
	}
}

func mapCategoryToType(category string) string {
	switch category {
	case "bug", "performance":
		return "fix"
	case "content":
		return "chore"
	default:
		return "feature"
	}
}

func formatItemDescription(item ux.ImprovementItem, sessionID string) string {
	desc := fmt.Sprintf("From UX session %s", sessionID)
	if item.Timestamp != "" {
		desc += fmt.Sprintf(" at %s", item.Timestamp)
	}
	if item.AffectedComponent != "" {
		desc += fmt.Sprintf("\n\nAffected component: %s", item.AffectedComponent)
	}
	if item.UserQuote != "" {
		desc += fmt.Sprintf("\n\nUser quote: \"%s\"", item.UserQuote)
	}
	desc += fmt.Sprintf("\n\nSource: %s | Severity: %s | Category: %s", item.Source, item.Severity, item.Category)
	return desc
}

// ── route ────────────────────────────────────────────────────────────────

const uxResearcherAgentID = "a9441f4f-6857-4f91-bb57-b17f6107f649"

var uxRouteCmd = &cobra.Command{
	Use:   "route [session-id]",
	Short: "Create a Paperclip issue from session analysis (assigned to UX Researcher)",
	RunE: func(cmd *cobra.Command, args []string) error {
		var session *ux.Session
		var err error

		if len(args) > 0 {
			session, err = ux.LoadSession(args[0])
		} else {
			session, err = ux.LatestSession()
		}
		if err != nil {
			return err
		}

		if session.Status != ux.StatusAnalyzed {
			return fmt.Errorf("session %s has not been analyzed yet — run 'lw ux analyze %s'", session.ID, session.ID)
		}

		analysisData, err := os.ReadFile(ux.AnalysisPath(session.ID))
		if err != nil {
			return fmt.Errorf("read analysis: %w", err)
		}

		pctx, err := paperclip.LoadContext()
		if err != nil {
			return fmt.Errorf("load paperclip context: %w", err)
		}
		companyID := pctx.ResolveCompanyID("")
		if companyID == "" {
			return fmt.Errorf("no paperclip company bound — run 'paperclipai auth' first")
		}

		items, _ := ux.LoadItems(session.ID)

		title := fmt.Sprintf("UX session review: %s", session.ID)
		if session.Name != "" {
			title = fmt.Sprintf("UX session review: %s", session.Name)
		}

		description := fmt.Sprintf("## UX Session: %s\n\n", session.ID)
		if session.Name != "" {
			description += fmt.Sprintf("**Name:** %s\n\n", session.Name)
		}
		description += fmt.Sprintf("**Duration:** %s\n**Items found:** %d\n\n", ux.FormatDuration(session.DurationSecs), len(items))
		description += "## Analysis\n\n" + string(analysisData)
		if _, statErr := os.Stat(ux.DockerSyncedPath(session.ID)); statErr == nil {
			description += fmt.Sprintf("\n\n**Docker logs:** `%s`", ux.DockerSyncedPath(session.ID))
		}

		ctx := context.Background()
		client := paperclip.NewClient()
		issue, err := client.CreateIssue(ctx, companyID, paperclip.Issue{
			Title:           title,
			Description:     description,
			Status:          "todo",
			AssigneeAgentID: uxResearcherAgentID,
		})
		if err != nil {
			return fmt.Errorf("create issue: %w", err)
		}

		fmt.Printf("%s Issue created: %s\n", color.GreenString("✓"), color.CyanString(issue.ID))
		fmt.Printf("   Title:    %s\n", issue.Title)
		fmt.Printf("   Assignee: UX Researcher\n")

		return nil
	},
}

// ── helpers ─────────────────────────────────────────────────────────────

func printItems(items []ux.ImprovementItem) {
	for _, item := range items {
		sevColor := color.YellowString
		switch item.Severity {
		case "critical":
			sevColor = color.RedString
		case "major":
			sevColor = color.MagentaString
		case "minor":
			sevColor = color.YellowString
		}

		fmt.Printf("  %s [%s] %s\n",
			sevColor(fmt.Sprintf("%-8s", item.Severity)),
			color.CyanString(item.Category),
			item.Description,
		)
		if item.UserQuote != "" {
			fmt.Printf("           %s %s\n", color.WhiteString("Quote:"), item.UserQuote)
		}
		if item.AffectedComponent != "" {
			fmt.Printf("           %s %s\n", color.WhiteString("Component:"), item.AffectedComponent)
		}
		if item.Timestamp != "" {
			fmt.Printf("           %s %s\n", color.WhiteString("At:"), item.Timestamp)
		}
		fmt.Println()
	}
}

const whisperModelURL = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.en.bin"

func downloadWhisperModel(dest string) error {
	if err := os.MkdirAll(ux.ModelsDir(), 0755); err != nil {
		return err
	}

	resp, err := http.Get(whisperModelURL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	written, err := io.Copy(f, resp.Body)
	if err != nil {
		return fmt.Errorf("write model: %w", err)
	}

	fmt.Printf("  Downloaded %d MB\n", written/1024/1024)
	return nil
}

func init() {
	uxStartCmd.Flags().StringVar(&uxStartName, "name", "", "Name/description for this session")

	uxAnalyzeCmd.Flags().BoolVar(&uxAnalyzeForce, "force", false, "Re-analyze an already-analyzed session")

	uxBacklogCmd.Flags().StringVar(&uxBacklogEpic, "epic", "", "Epic ID to assign tasks to")
	uxBacklogCmd.Flags().StringVar(&uxBacklogSprint, "sprint", "", "Sprint ID to assign tasks to")

	uxCmd.AddCommand(uxInitCmd)
	uxCmd.AddCommand(uxStartCmd)
	uxCmd.AddCommand(uxStopCmd)
	uxCmd.AddCommand(uxAnalyzeCmd)
	uxCmd.AddCommand(uxListCmd)
	uxCmd.AddCommand(uxItemsCmd)
	uxCmd.AddCommand(uxPlayCmd)
	uxCmd.AddCommand(uxDevicesCmd)
	uxCmd.AddCommand(uxBacklogCmd)
	uxCmd.AddCommand(uxRouteCmd)
	uxDeleteCmd.Flags().BoolVar(&uxDeleteForce, "force", false, "Delete even if session has analysis data")
	uxCmd.AddCommand(uxDeleteCmd)
}
