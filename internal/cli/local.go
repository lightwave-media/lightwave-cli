package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/config"
)

// Schema-driven local development environment. commands.yaml v3.0.0 declares 8
// commands. Replaces legacy `dev` (still wired in dev.go for backwards-compat
// during the Phase 4–5 migration). The thesis from the design plan: silent
// hangs come from un-supervised `make start-bg` shelling. Every operation here
// runs `docker compose` directly with bounded timeouts, structured failure,
// and a preflight gate.

const (
	// upTimeout caps "lw local up" — any longer means something is genuinely
	// wedged (image pull, build, hung healthcheck) and human attention beats
	// further waiting.
	upTimeout = 10 * time.Minute
	// healthPollInterval & healthSettleTimeout govern post-up healthcheck
	// polling. settleTimeout is the wall-clock window for *all* services to
	// report healthy.
	healthPollInterval  = 3 * time.Second
	healthSettleTimeout = 3 * time.Minute
	// restartTimeout & downTimeout — generous but bounded. Below the upTimeout
	// because nothing is being built.
	restartTimeout = 3 * time.Minute
	downTimeout    = 2 * time.Minute
)

// requiredPorts is the host-side port set the compose stack binds. preflight
// fails fast if any are already occupied — stale processes from prior runs
// were the #2 silent-hang cause.
var requiredPorts = []int{80, 443, 3000, 5432, 6379, 8000}

// criticalServices is the subset whose health is treated as gating. The full
// service set comes from `docker compose ps --format json`; we surface every
// service in `lw local health` but require these to settle before reporting
// `up` as successful.
var criticalServices = []string{"db", "redis", "backend", "frontend"}

func init() {
	RegisterHandler("local.up", localUpHandler)
	RegisterHandler("local.down", localDownHandler)
	RegisterHandler("local.logs", localLogsHandler)
	RegisterHandler("local.health", localHealthHandler)
	RegisterHandler("local.restart", localRestartHandler)
	RegisterHandler("local.preflight", localPreflightHandler)
	RegisterHandler("local.setup", localSetupHandler)
	RegisterHandler("local.clean", localCleanHandler)
}

// ---------------------------------------------------------------------------
// up / down / restart / logs
// ---------------------------------------------------------------------------

func localUpHandler(ctx context.Context, _ []string, flags map[string]any) error {
	skipPreflight := flagBool(flags, "skip-preflight")
	build := flagBool(flags, "build")
	bg := flagBool(flags, "bg")
	service := flagStr(flags, "service")

	if !skipPreflight {
		fmt.Println(color.CyanString("→ preflight"))
		if err := runPreflight(ctx, false); err != nil {
			return fmt.Errorf("preflight: %w (rerun with --skip-preflight to bypass, or use lw local preflight --fix)", err)
		}
	}

	upCtx, cancel := context.WithTimeout(ctx, upTimeout)
	defer cancel()

	args := []string{"up"}
	if bg {
		args = append(args, "-d")
	}
	if build {
		args = append(args, "--build")
	}
	if service != "" {
		args = append(args, service)
	}

	fmt.Println(color.CyanString("→ docker compose %s", strings.Join(args, " ")))
	if err := runCompose(upCtx, args...); err != nil {
		return fmt.Errorf("compose up: %w", err)
	}

	if !bg {
		// Foreground compose blocks until interrupt; nothing to settle.
		return nil
	}

	fmt.Println(color.CyanString("→ waiting for services to become healthy"))
	return waitHealthy(ctx)
}

func localDownHandler(ctx context.Context, _ []string, flags map[string]any) error {
	downCtx, cancel := context.WithTimeout(ctx, downTimeout)
	defer cancel()

	args := []string{"down"}
	if flagBool(flags, "volumes") {
		args = append(args, "--volumes")
	}
	return runCompose(downCtx, args...)
}

func localLogsHandler(ctx context.Context, _ []string, flags map[string]any) error {
	args := []string{"logs"}
	if flagBool(flags, "follow") {
		args = append(args, "-f")
	}
	if tail := flagStr(flags, "tail"); tail != "" {
		args = append(args, "--tail", tail)
	}
	if service := flagStr(flags, "service"); service != "" {
		args = append(args, service)
	}
	// Logs is intentionally un-timeouted — `--follow` is the user's signal.
	return runCompose(ctx, args...)
}

func localRestartHandler(ctx context.Context, _ []string, flags map[string]any) error {
	build := flagBool(flags, "build")
	service := flagStr(flags, "service")

	rCtx, cancel := context.WithTimeout(ctx, restartTimeout)
	defer cancel()

	if build {
		// Restart with rebuild is really down + up --build. Compose's plain
		// `restart` does not recreate containers, so a code change wouldn't
		// land.
		downArgs := []string{"down"}
		if service != "" {
			downArgs = []string{"stop", service}
		}
		if err := runCompose(rCtx, downArgs...); err != nil {
			return fmt.Errorf("stop before rebuild: %w", err)
		}
		upArgs := []string{"up", "-d", "--build"}
		if service != "" {
			upArgs = append(upArgs, service)
		}
		return runCompose(rCtx, upArgs...)
	}

	args := []string{"restart"}
	if service != "" {
		args = append(args, service)
	}
	return runCompose(rCtx, args...)
}

// ---------------------------------------------------------------------------
// health / preflight / setup / clean
// ---------------------------------------------------------------------------

// serviceHealth is one row of `docker compose ps --format json` after the
// fields we care about are extracted. State is the lifecycle (running, exited,
// etc); Health is the healthcheck result (healthy, unhealthy, starting, "" if
// no healthcheck defined).
type serviceHealth struct {
	Name   string `json:"Name"`
	Image  string `json:"Image"`
	State  string `json:"State"`
	Health string `json:"Health"`
}

func localHealthHandler(ctx context.Context, _ []string, flags map[string]any) error {
	asJSONFlag := asJSON(flags)
	watch := flagBool(flags, "watch")

	once := func() error {
		services, err := composePS(ctx)
		if err != nil {
			return err
		}
		if asJSONFlag {
			return emitJSON(services)
		}
		printLocalHealthTable(services)
		return nil
	}

	if !watch {
		return once()
	}
	// --watch: poll every 2s, redraw. Honors ctx cancellation.
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		// ANSI clear-screen so successive frames don't pile up.
		fmt.Print("\033[H\033[2J")
		if err := once(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

func localPreflightHandler(ctx context.Context, _ []string, flags map[string]any) error {
	fix := flagBool(flags, "fix")
	asJSONFlag := asJSON(flags)

	report := buildPreflightReport(ctx)
	if asJSONFlag {
		return emitJSON(report)
	}
	printPreflightReport(report)

	if !report.OK {
		if fix {
			// `--fix` is a hint surface — suggest commands rather than mutate
			// the host. Touching /etc/hosts or installing certs without
			// explicit consent violates the destructive-action guardrail.
			fmt.Printf("\n%s\n", color.YellowString("Suggested fixes:"))
			for _, h := range report.Hints {
				fmt.Printf("  %s\n", h)
			}
		}
		return fmt.Errorf("preflight failed: %d check(s)", report.FailCount)
	}
	return nil
}

func localSetupHandler(ctx context.Context, _ []string, flags map[string]any) error {
	dryRun := flagBool(flags, "dry-run")

	steps := []string{
		"Verify Docker daemon reachable",
		"Verify docker-compose.yml present",
		"Verify required ports free (80, 443, 3000, 5432, 6379, 8000)",
		"Verify .envrc loaded (LW_AGENT_KEY, COMPOSE_FILE)",
		"Verify lightwave-platform/src and packages/lightwave-core checked out",
	}

	if dryRun {
		fmt.Println(color.CyanString("Setup steps (dry run):"))
		for i, s := range steps {
			fmt.Printf("  %d. %s\n", i+1, s)
		}
		return nil
	}

	// `setup` is intentionally a thin wrapper over preflight today. The plan
	// reserves this slot for future provisioning (mkcert, /etc/hosts entries),
	// but those touch the host outside the project tree and need a separate
	// confirmation pass — keep manual for now.
	return runPreflight(ctx, true)
}

func localCleanHandler(ctx context.Context, _ []string, flags map[string]any) error {
	volumes := flagBool(flags, "volumes")
	images := flagBool(flags, "images")
	confirm := flagBool(flags, "confirm")

	args := []string{"down"}
	desc := "containers + networks"
	if volumes {
		args = append(args, "--volumes")
		desc += " + named volumes"
	}
	if images {
		args = append(args, "--rmi", "local")
		desc += " + locally-built images"
	}

	fmt.Printf("Will remove: %s\n", color.YellowString(desc))
	if !confirm {
		if !promptYesNo("Proceed?") {
			fmt.Println("aborted")
			return nil
		}
	}

	cCtx, cancel := context.WithTimeout(ctx, downTimeout)
	defer cancel()
	return runCompose(cCtx, args...)
}

// ---------------------------------------------------------------------------
// supervisor primitives
// ---------------------------------------------------------------------------

// composeCmd builds an exec.Cmd for `docker compose <args>` rooted at the
// workspace, with COMPOSE_FILE relativized into absolute paths so docker can
// resolve files no matter where the binary was invoked from.
func composeCmd(ctx context.Context, args ...string) *exec.Cmd {
	cfg := config.Get()
	root := cfg.Paths.LightwaveRoot

	full := append([]string{"compose"}, args...)
	c := exec.CommandContext(ctx, "docker", full...)
	c.Dir = root

	if composeFile := os.Getenv("COMPOSE_FILE"); composeFile != "" {
		parts := strings.Split(composeFile, ":")
		for i, p := range parts {
			if !filepath.IsAbs(p) {
				parts[i] = filepath.Join(root, p)
			}
		}
		c.Env = append(os.Environ(), "COMPOSE_FILE="+strings.Join(parts, ":"))
	}
	return c
}

// runCompose runs `docker compose <args>` streaming stdio. Caller controls
// timeout via ctx.
func runCompose(ctx context.Context, args ...string) error {
	c := composeCmd(ctx, args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("docker compose %s: timeout exceeded", strings.Join(args, " "))
		}
		return err
	}
	return nil
}

// composePS returns parsed `docker compose ps --format json` rows. Compose
// emits one JSON object per line (NDJSON) on this command, not a top-level
// array.
func composePS(ctx context.Context) ([]serviceHealth, error) {
	c := composeCmd(ctx, "ps", "--all", "--format", "json")
	out, err := c.Output()
	if err != nil {
		return nil, fmt.Errorf("docker compose ps: %w", err)
	}

	var services []serviceHealth
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var s serviceHealth
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			return nil, fmt.Errorf("parse compose ps row: %w (line: %s)", err, line)
		}
		services = append(services, s)
	}
	return services, nil
}

// waitHealthy polls compose ps until every criticalServices entry reports
// State=running and (Health="healthy" OR no healthcheck defined). Returns the
// failing services if the settle window expires.
func waitHealthy(ctx context.Context) error {
	deadline := time.Now().Add(healthSettleTimeout)
	t := time.NewTicker(healthPollInterval)
	defer t.Stop()

	for {
		services, err := composePS(ctx)
		if err != nil {
			return err
		}

		// Compose names containers as <project>-<service>-<n>; we also accept
		// bare service names if compose emits them.
		var pending []string
		for _, want := range criticalServices {
			found := false
			for _, s := range services {
				if s.Name == want || strings.Contains(s.Name, "-"+want+"-") || strings.HasSuffix(s.Name, "-"+want) {
					found = true
					if s.State != "running" {
						pending = append(pending, fmt.Sprintf("%s [state=%s]", want, s.State))
						break
					}
					if s.Health != "" && s.Health != "healthy" {
						pending = append(pending, fmt.Sprintf("%s [health=%s]", want, s.Health))
					}
					break
				}
			}
			if !found {
				pending = append(pending, want+" [missing]")
			}
		}

		if len(pending) == 0 {
			fmt.Println(color.GreenString("✓ all critical services healthy"))
			return nil
		}

		if time.Now().After(deadline) {
			fmt.Printf("\n%s services did not settle within %s:\n",
				color.RedString("✗"), healthSettleTimeout)
			for _, p := range pending {
				fmt.Printf("  - %s\n", p)
			}
			fmt.Printf("\n%s\n", color.YellowString("Inspect logs: lw local logs --service <name> --tail 100"))
			return fmt.Errorf("services unhealthy: %s", strings.Join(pending, ", "))
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

// ---------------------------------------------------------------------------
// preflight
// ---------------------------------------------------------------------------

type preflightCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

type preflightReport struct {
	OK        bool             `json:"ok"`
	FailCount int              `json:"fail_count"`
	Checks    []preflightCheck `json:"checks"`
	Hints     []string         `json:"hints,omitempty"`
}

func buildPreflightReport(ctx context.Context) preflightReport {
	cfg := config.Get()
	root := cfg.Paths.LightwaveRoot
	checks := []preflightCheck{}
	hints := []string{}

	// 1. Docker daemon
	dCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	if err := exec.CommandContext(dCtx, "docker", "info").Run(); err != nil {
		checks = append(checks, preflightCheck{Name: "docker daemon", OK: false, Detail: "docker info failed"})
		hints = append(hints, "Start Docker Desktop and rerun")
	} else {
		checks = append(checks, preflightCheck{Name: "docker daemon", OK: true})
	}
	cancel()

	// 2. Compose file present
	composePath := filepath.Join(root, "docker-compose.yml")
	if _, err := os.Stat(composePath); err != nil {
		checks = append(checks, preflightCheck{Name: "docker-compose.yml", OK: false, Detail: composePath + " not found"})
		hints = append(hints, "Run from a workspace where lightwave-media root has docker-compose.yml")
	} else {
		checks = append(checks, preflightCheck{Name: "docker-compose.yml", OK: true})
	}

	// 3. Required ports free OR already held by our stack. If composePS
	// returns any running services, treat the bound ports as friendly —
	// preflight is a pre-`up` gate, but it's also called by `setup`, so we
	// don't want to flag a healthy running stack as broken.
	stackRunning := false
	if services, err := composePS(ctx); err == nil {
		for _, s := range services {
			if s.State == "running" {
				stackRunning = true
				break
			}
		}
	}
	for _, port := range requiredPorts {
		if !portInUse(port) {
			checks = append(checks, preflightCheck{Name: fmt.Sprintf("port %d free", port), OK: true})
			continue
		}
		if stackRunning {
			checks = append(checks, preflightCheck{
				Name:   fmt.Sprintf("port %d", port),
				OK:     true,
				Detail: "held by running lw stack",
			})
			continue
		}
		checks = append(checks, preflightCheck{
			Name:   fmt.Sprintf("port %d free", port),
			OK:     false,
			Detail: "occupied by another process",
		})
		hints = append(hints, fmt.Sprintf("lsof -i :%d  # find and kill the process holding port %d", port, port))
	}

	// 4. .envrc bridge — LW_AGENT_KEY is the canonical signal that direnv
	// loaded SSM-backed secrets.
	if os.Getenv("LW_AGENT_KEY") == "" {
		checks = append(checks, preflightCheck{Name: "LW_AGENT_KEY in env", OK: false, Detail: "direnv may not have loaded"})
		hints = append(hints, "cd into the workspace and run: direnv allow")
	} else {
		checks = append(checks, preflightCheck{Name: "LW_AGENT_KEY in env", OK: true})
	}

	// 5. lightwave-core symlinkable (compose mounts it)
	corePath := filepath.Join(root, "packages", "lightwave-core")
	if _, err := os.Stat(corePath); err != nil {
		checks = append(checks, preflightCheck{Name: "packages/lightwave-core", OK: false, Detail: corePath + " missing"})
		hints = append(hints, "git submodule update --init --recursive  # or pull packages")
	} else {
		checks = append(checks, preflightCheck{Name: "packages/lightwave-core", OK: true})
	}

	report := preflightReport{Checks: checks, Hints: hints}
	for _, c := range checks {
		if !c.OK {
			report.FailCount++
		}
	}
	report.OK = report.FailCount == 0
	return report
}

func runPreflight(ctx context.Context, verbose bool) error {
	report := buildPreflightReport(ctx)
	if verbose || !report.OK {
		printPreflightReport(report)
	}
	if !report.OK {
		return fmt.Errorf("%d check(s) failed", report.FailCount)
	}
	return nil
}

func printPreflightReport(r preflightReport) {
	for _, c := range r.Checks {
		mark := color.GreenString("✓")
		if !c.OK {
			mark = color.RedString("✗")
		}
		if c.Detail != "" {
			fmt.Printf("  %s %s — %s\n", mark, c.Name, c.Detail)
		} else {
			fmt.Printf("  %s %s\n", mark, c.Name)
		}
	}
}

// portInUse returns true when something is listening on 127.0.0.1:port.
// Specifically tests TCP — UDP services aren't part of the compose stack.
func portInUse(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return true
	}
	_ = l.Close()
	return false
}

// printLocalHealthTable renders the compose ps view in a compact form. We prefer
// raw printf over tablewriter here so --watch redraws stay tear-free.
func printLocalHealthTable(services []serviceHealth) {
	if len(services) == 0 {
		fmt.Println(color.YellowString("No services running"))
		return
	}
	fmt.Printf("%-30s %-12s %-12s %s\n",
		color.CyanString("NAME"),
		color.CyanString("STATE"),
		color.CyanString("HEALTH"),
		color.CyanString("IMAGE"))
	for _, s := range services {
		state := s.State
		switch state {
		case "running":
			state = color.GreenString(state)
		case "exited", "dead":
			state = color.RedString(state)
		default:
			state = color.YellowString(state)
		}
		health := s.Health
		switch health {
		case "healthy":
			health = color.GreenString(health)
		case "unhealthy":
			health = color.RedString(health)
		case "starting":
			health = color.YellowString(health)
		case "":
			health = "-"
		}
		fmt.Printf("%-30s %-12s %-12s %s\n", s.Name, state, health, s.Image)
	}
}

// ---------------------------------------------------------------------------
// flag accessors + prompt — kept here because no other domain needs them yet.
// Move into a shared helpers file once a third caller appears.
// ---------------------------------------------------------------------------

func flagStr(flags map[string]any, key string) string {
	v, ok := flags[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func flagBool(flags map[string]any, key string) bool {
	v, ok := flags[key]
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

func promptYesNo(question string) bool {
	fmt.Printf("%s [y/N] ", question)
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil {
		return false
	}
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes"
}
