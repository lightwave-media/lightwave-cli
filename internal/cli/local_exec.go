package cli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"sort"
	"strings"

	"github.com/fatih/color"
)

func init() {
	RegisterHandler("local.exec", localExecHandler)
	RegisterHandler("local.install-frontend", localInstallFrontendHandler)
}

// localExecHandler implements `lw local exec <service> [cmd...]`.
//
// Routes to `docker compose exec <service> <cmd>` when the container is
// running, falls back to `docker compose run --rm <service> <cmd>` otherwise.
// Default cmd is `bash`. Service is validated against the resolved compose
// file so a typo produces a useful error rather than docker's terse
// "service not found" output.
//
// The whole point: it gives agents (and humans following bash-guard) a
// sanctioned `lw` route to non-backend service shells / one-shots — closing
// the gap that motivated lightwave-media/lightwave-cli#11.
func localExecHandler(ctx context.Context, args []string, _ map[string]any) error {
	if len(args) < 1 {
		return errors.New("usage: lw local exec <service> [cmd...]")
	}

	service := args[0]

	cmdArgs := args[1:]
	if len(cmdArgs) == 0 {
		cmdArgs = []string{"bash"}
	}

	services, err := composeServices(ctx)
	if err != nil {
		return err
	}

	if !slices.Contains(services, service) {
		return fmt.Errorf("unknown service %q (available: %s)", service, strings.Join(services, ", "))
	}

	running, err := isComposeServiceRunning(ctx, service)
	if err != nil {
		return err
	}

	composeArgs := []string{"exec", service}
	if !running {
		// `run --rm` overrides the service's `command:` and skips its
		// startup install. That's fine here because the caller already
		// chose to run a specific command.
		composeArgs = []string{"run", "--rm", service}
	}

	return runCompose(ctx, append(composeArgs, cmdArgs...)...)
}

// localInstallFrontendHandler implements `lw local install-frontend`.
//
// Default mode: runs `pnpm install --frozen-lockfile` against the frontend
// service via the same code path as `lw local exec frontend pnpm
// install --frozen-lockfile`. Recovers the common "named volume drifted
// from lockfile" failure that produces ~150 spurious TS errors after a
// Dependabot bump.
//
// --force: removes the project-scoped `frontend_node_modules` volume
// first so the install starts from clean state. Destructive — gated on
// --yes per the destructive-command standard (lightwave-cli/CLAUDE.md).
func localInstallFrontendHandler(ctx context.Context, _ []string, flags map[string]any) error {
	force := flagBool(flags, "force")
	yes := flagBool(flags, "yes")

	if force {
		volume, err := resolveFrontendVolumeName(ctx)
		if err != nil {
			return err
		}

		fmt.Printf("Will remove docker volume: %s\n", color.YellowString(volume))

		if !yes {
			if !promptYesNo("Proceed?") {
				fmt.Println("aborted")
				return nil
			}
		}

		if err := stopAndRemoveVolume(ctx, "frontend", volume); err != nil {
			return err
		}

		fmt.Println(color.GreenString("✓ removed %s", volume))
	}

	// Reuse the exec path so `--force` + the default install share one
	// route to docker. running=false → `run --rm` which is what we want
	// after a forced volume teardown anyway.
	return localExecHandler(ctx, []string{
		"frontend", "pnpm", "install", "--frozen-lockfile",
	}, nil)
}

// composeServices returns the list of services defined in the resolved
// compose file. Uses `docker compose config --services` rather than
// parsing YAML ourselves — keeps us aligned with whatever overrides /
// COMPOSE_FILE chains the caller has set.
func composeServices(ctx context.Context) ([]string, error) {
	c := composeCmd(ctx, "config", "--services")

	out, err := c.Output()
	if err != nil {
		return nil, fmt.Errorf("docker compose config --services: %w", err)
	}

	var services []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			services = append(services, line)
		}
	}

	sort.Strings(services)

	return services, nil
}

// isComposeServiceRunning probes `docker compose ps` for a running
// container of the named service. Returns false (not an error) for
// services that are simply not up — the caller falls back to `run --rm`.
func isComposeServiceRunning(ctx context.Context, service string) (bool, error) {
	rows, err := composePS(ctx)
	if err != nil {
		return false, err
	}

	for _, r := range rows {
		// composePS returns container Name (often `<project>-<service>-N`)
		// and ID. The Service field on the JSON row is the canonical
		// name, but composePS doesn't expose it — match by container name.
		if r.Name == service ||
			strings.Contains(r.Name, "-"+service+"-") ||
			strings.HasSuffix(r.Name, "-"+service) {
			if r.State == "running" {
				return true, nil
			}
		}
	}

	return false, nil
}

// resolveFrontendVolumeName finds the docker volume whose name ends in
// "_frontend_node_modules". The compose project prefix is whatever
// docker chose at first `up`; we don't try to second-guess it.
func resolveFrontendVolumeName(ctx context.Context) (string, error) {
	c := exec.CommandContext(ctx, "docker", "volume", "ls", "--format", "{{.Name}}")

	out, err := c.Output()
	if err != nil {
		return "", fmt.Errorf("docker volume ls: %w", err)
	}

	var matches []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, "_frontend_node_modules") {
			matches = append(matches, line)
		}
	}

	if len(matches) == 0 {
		return "", errors.New("no docker volume matching *_frontend_node_modules — has the stack ever been brought up?")
	}

	if len(matches) > 1 {
		return "", fmt.Errorf("multiple frontend_node_modules volumes found (%v) — refusing to guess; remove the stale one manually", matches)
	}

	return matches[0], nil
}

// stopAndRemoveVolume stops the named compose service, removes its
// container, then removes the docker volume. Volume rm fails if any
// container still references it — bringing the service down first is
// what makes `--force` reliable.
func stopAndRemoveVolume(ctx context.Context, service, volume string) error {
	if err := runCompose(ctx, "stop", service); err != nil {
		return fmt.Errorf("stop %s: %w", service, err)
	}

	if err := runCompose(ctx, "rm", "-f", service); err != nil {
		return fmt.Errorf("rm %s: %w", service, err)
	}

	c := exec.CommandContext(ctx, "docker", "volume", "rm", volume)
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("docker volume rm %s: %w (output: %s)", volume, err, strings.TrimSpace(string(out)))
	}

	return nil
}
