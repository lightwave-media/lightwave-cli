package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/cron"
	"github.com/lightwave-media/lightwave-cli/internal/nulltickets"
	"github.com/lightwave-media/lightwave-cli/internal/secrets"
)

// `lw cron sync` and `lw cron run` (lightwave-cli#302; cron_job 1.1.0, plan
// slices D4 and S1). Both are in the cron domain, `_status: in_development` in
// core's commands.yaml until S2 publishes it.
//
// sync renders each stamped job into a nix-darwin launchd agent. It loads
// nothing: since 2026-10-01 no launchd job on this host is loaded by hand, and
// nix-darwin is the loader. run is what each rendered agent calls on tick.

func init() {
	RegisterHandler("cron.sync", cronSyncHandler)
	RegisterHandler("cron.run", cronRunHandler)
}

// Seams for tests: where the secret map is read from and the clock.
var (
	cronSecretMapDir = secrets.DefaultMapDir
	cronNow          = time.Now
)

type cronSyncResult struct {
	// Module is the nix module text; omitted from --json.
	Module   string         `json:"-"`
	Rendered []cronRendered `json:"rendered"`
	Refused  []cronRefused  `json:"refused"`
	Disabled []string       `json:"disabled"`
}

type cronRendered struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Schedule    string   `json:"schedule"`
	Persona     string   `json:"persona"`
	Kind        string   `json:"dispatch_kind"`
	SecretNames []string `json:"secret_names"`
}

type cronRefused struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// cronRecords loads the consumer records sync checks entitlements against.
// The strict loader in internal/secrets is the one reader: a record that does
// not decode refuses the whole map rather than letting a job slip through.
func cronRecords() (map[string]cron.Record, error) {
	dir, err := cronSecretMapDir()
	if err != nil {
		return nil, err
	}

	m, err := secrets.LoadMap(dir)
	if err != nil {
		return nil, err
	}

	records := make(map[string]cron.Record, len(m.Daemons))

	for i := range m.Daemons {
		d := &m.Daemons[i]
		rec := cron.Record{ID: d.ID}

		for _, l := range d.SecretLoadings {
			rec.Names = append(rec.Names, cron.RecordNames(l.TargetEnvVar, l.SSMPath))
		}

		records[d.ID] = rec
	}

	return records, nil
}

func cronRenderOptions() (cron.RenderOptions, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return cron.RenderOptions{}, fmt.Errorf("resolve home: %w", err)
	}

	lw := filepath.Join(home, ".local", "bin", "lw")

	return cron.RenderOptions{
		LwPath: lw,
		LogDir: filepath.Join(home, ".lightwave", "observability", "launchd"),
		Path: strings.Join([]string{
			filepath.Join(home, ".local", "bin"),
			"/nix/var/nix/profiles/default/bin",
			"/opt/homebrew/bin", "/usr/bin", "/bin",
		}, ":"),
	}, nil
}

// cronSync renders every job. A job sync refuses is reported with its reason;
// a refusal never stops the others.
func cronSync(ctx context.Context, jobs []cron.Job) (*cronSyncResult, error) {
	opts, err := cronRenderOptions()
	if err != nil {
		return nil, err
	}

	result := &cronSyncResult{Rendered: []cronRendered{}, Refused: []cronRefused{}, Disabled: []string{}}

	records, recordsErr := cronRecords()

	var agents []cron.LaunchAgent

	for i := range jobs {
		job := &jobs[i]
		if !job.Enabled {
			result.Disabled = append(result.Disabled, job.ID)
			continue
		}

		if recordsErr != nil {
			result.Refused = append(result.Refused, cronRefused{job.ID, "secret map unreadable, so no entitlement can be checked: " + recordsErr.Error()})
			continue
		}

		if why := cron.Refusal(job, records); why != "" {
			result.Refused = append(result.Refused, cronRefused{job.ID, why})
			continue
		}

		agent, err := cron.Render(job, opts)
		if err != nil {
			result.Refused = append(result.Refused, cronRefused{job.ID, err.Error()})
			continue
		}

		if err := plutilLint(ctx, &agent); err != nil {
			return nil, fmt.Errorf("job %s rendered a plist launchd would not load: %w", job.ID, err)
		}

		agents = append(agents, agent)
		result.Rendered = append(result.Rendered, cronRendered{
			ID: job.ID, Label: agent.Label, Schedule: job.Schedule, Persona: job.Persona,
			Kind: job.Dispatch.Kind, SecretNames: job.SecretNames,
		})
	}

	result.Module = string(cron.EncodeNix(agents, "lightwave-core "+cron.StampRef+":src/schemas/"+cron.JobsFamily))

	return result, nil
}

// plutilLint checks the plist the agent becomes. On a host without plutil
// (Linux CI) it is skipped; the golden-file tests carry the format there.
func plutilLint(ctx context.Context, agent *cron.LaunchAgent) error {
	plutil, err := exec.LookPath("plutil")
	if err != nil {
		return nil //nolint:nilerr // no plutil is not a lint failure
	}

	dir, err := os.MkdirTemp("", "lw-cron-lint-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	file := filepath.Join(dir, agent.Label+".plist")
	if err := os.WriteFile(file, cron.EncodePlist(agent), lintFileMode); err != nil {
		return err
	}

	if out, err := exec.CommandContext(ctx, plutil, "-lint", file).CombinedOutput(); err != nil { //nolint:gosec // our own temp file
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}

	return nil
}

// lintFileMode is the scratch plist's mode: private, it is never loaded.
const lintFileMode = 0o600

func cronSyncHandler(ctx context.Context, _ []string, flags map[string]any) error {
	cfg := config.Get()
	if cfg == nil {
		return errors.New("config not loaded")
	}

	jobs, err := cron.LoadJobs(ctx, cfg.Paths.LightwaveRoot)
	if err != nil {
		return err
	}

	result, err := cronSync(ctx, jobs)
	if err != nil {
		return err
	}

	if flagBool(flags, "json") {
		out, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}

		fmt.Println(string(out))

		return nil
	}

	fmt.Printf("lw cron sync: %d rendered, %d refused, %d disabled\n", len(result.Rendered), len(result.Refused), len(result.Disabled))

	for _, r := range result.Refused {
		fmt.Printf("  REFUSED %-30s %s\n", r.ID, r.Reason)
	}

	for _, r := range result.Rendered {
		fmt.Printf("  OK      %-30s %-14s %s\n", r.ID, r.Schedule, r.Label)
	}

	if flagBool(flags, "dry-run") {
		return nil
	}

	// No output path is declared for sync yet, so the module goes to stdout for
	// the operator to place in nix-config; sync writes and loads nothing.
	fmt.Println()
	fmt.Print(result.Module)

	return nil
}

// shellMeta is what a shell would interpret. run executes a shell_command
// target as argv, never through a shell, so a target that needs one is refused.
// A leading ~ is the one expansion run does itself, token by token (homeArgv).
var shellMeta = regexp.MustCompile("[|;&<>$`\\\\(){}*?'\"\\n]")

// homeArgv expands a token that is ~ or starts with ~/ to the home directory,
// as a shell would. Any other ~ (~user, or one mid-token) is refused rather
// than passed through literally.
func homeArgv(tokens []string) ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	argv := make([]string, len(tokens))

	for i, token := range tokens {
		switch {
		case token == "~":
			argv[i] = home
		case strings.HasPrefix(token, "~/"):
			argv[i] = filepath.Join(home, token[2:])
		case strings.Contains(token, "~"):
			return nil, fmt.Errorf("token %q needs a shell to expand its ~", token)
		default:
			argv[i] = token
		}
	}

	return argv, nil
}

func cronFindJob(ctx context.Context, id string) (*cron.Job, error) {
	cfg := config.Get()
	if cfg == nil {
		return nil, errors.New("config not loaded")
	}

	jobs, err := cron.LoadJobs(ctx, cfg.Paths.LightwaveRoot)
	if err != nil {
		return nil, err
	}

	for i := range jobs {
		if jobs[i].ID == id {
			return &jobs[i], nil
		}
	}

	return nil, fmt.Errorf("no stamped job %q at %s", id, cron.StampRef)
}

func cronRunHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return errors.New("usage: lw cron run <id> [--dry-run] [--json]")
	}

	job, err := cronFindJob(ctx, args[0])
	if err != nil {
		return err
	}

	if !job.Enabled {
		return fmt.Errorf("job %s is disabled in the stamp", job.ID)
	}

	records, err := cronRecords()
	if err != nil {
		return fmt.Errorf("secret map unreadable: %w", err)
	}

	if why := cron.Refusal(job, records); why != "" {
		return fmt.Errorf("job %s: %s", job.ID, why)
	}

	dryRun := flagBool(flags, "dry-run")

	switch job.Dispatch.Kind {
	case cron.DispatchShell:
		return cronRunShell(ctx, job, dryRun)
	case cron.DispatchAgentSession:
		return cronRunAgentSession(ctx, job, dryRun, flagBool(flags, "json"))
	default:
		return fmt.Errorf("job %s: dispatch kind %q is not one lw cron run executes", job.ID, job.Dispatch.Kind)
	}
}

// cronRunShell runs a shell_command target as argv in this process's
// environment, which the agent's `lw config exec --only` has already narrowed
// to the job's entitled names.
func cronRunShell(ctx context.Context, job *cron.Job, dryRun bool) error {
	if shellMeta.MatchString(job.Dispatch.Target) {
		return fmt.Errorf("job %s: target needs a shell; lw cron run executes argv only", job.ID)
	}

	argv, err := homeArgv(strings.Fields(job.Dispatch.Target))
	if err != nil {
		return fmt.Errorf("job %s: %w; lw cron run executes argv only", job.ID, err)
	}

	if dryRun {
		fmt.Printf("[dry-run] %s: would run %q with secrets %s\n", job.ID, argv, strings.Join(job.SecretNames, ","))
		return nil
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // a stamped command, refused if it needs a shell
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, nil

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("job %s: %w", job.ID, err)
	}

	return nil
}

// routineTaskBody is the task's description: rendered only from the prompt
// template, the args and the window, so one window always yields one body and a
// repeated fire is an idempotent replay rather than a 409.
func routineTaskBody(job *cron.Job, window string) string {
	keys := make([]string, 0, len(job.Dispatch.Args))
	for k := range job.Dispatch.Args {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	var b strings.Builder

	b.WriteString(strings.TrimSpace(job.Dispatch.PromptTemplate))
	fmt.Fprintf(&b, "\n\nScheduled job %s, window %s.", job.ID, window)

	if len(keys) > 0 {
		b.WriteString("\nArgs:")

		for _, k := range keys {
			v, err := json.Marshal(job.Dispatch.Args[k])
			if err != nil {
				v = []byte(fmt.Sprint(job.Dispatch.Args[k]))
			}

			fmt.Fprintf(&b, "\n  %s: %s", k, v)
		}
	}

	return b.String()
}

// cronRunAgentSession queues one task on routine-<persona> for the fire slot
// this tick belongs to. nullboiler claims it and dispatches it to the persona.
func cronRunAgentSession(ctx context.Context, job *cron.Job, dryRun, asJSON bool) error {
	fired, err := cron.LastFire(job.Schedule, cronNow())
	if err != nil {
		return err
	}

	window := fired.Format("2006-01-02T15:04Z")
	key := fmt.Sprintf("routine:%s:%s", job.ID, window)
	pipelineName := "routine-" + job.Persona

	if dryRun {
		fmt.Printf("[dry-run] %s: would queue %s on pipeline %s\n", job.ID, key, pipelineName)
		return nil
	}

	queue, err := issueLoopQueue(ctx, false)
	if err != nil {
		return err
	}

	pipelineID, err := queue.PipelineIDByName(ctx, pipelineName)
	if err != nil {
		return err
	}

	if pipelineID == "" {
		return fmt.Errorf("job %s: no nulltickets pipeline %s; the factory installer creates routine lanes", job.ID, pipelineName)
	}

	taskID, err := queue.CreateTask(ctx, key, nulltickets.TaskRequest{
		PipelineID:  pipelineID,
		Title:       fmt.Sprintf("%s (%s)", job.Title, window),
		Description: routineTaskBody(job, window),
		Metadata: map[string]any{
			"source":  "lw cron run",
			"job_id":  job.ID,
			"window":  window,
			"persona": job.Persona,
		},
	})

	result := map[string]string{"job": job.ID, "key": key, "task_id": taskID}

	switch {
	case err == nil:
		result["action"] = "queued"
	case strings.Contains(err.Error(), "HTTP 409"):
		// The key already names a different body: the stamp changed after this
		// window's task was queued. That task stands.
		result["action"] = "already_queued"
	default:
		return err
	}

	if asJSON {
		out, err := json.Marshal(result)
		if err != nil {
			return err
		}

		fmt.Println(string(out))

		return nil
	}

	fmt.Printf("lw cron run %s: %s %s %s\n", job.ID, result["action"], key, taskID)

	return nil
}
