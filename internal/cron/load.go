package cron

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// JobsFamily is the stamp path holding scheduled job declarations.
const JobsFamily = "workflows/scheduled_jobs"

// notAJob are files in the family that declare a shape or an index rather than
// a job instance. cron_job.yaml is the SHAPE — reading it as an instance would
// report a job called "id" scheduled "str".
var notAJob = map[string]bool{
	"__index.yaml":  true,
	"cron_job.yaml": true,
}

// StampRef is the ref job declarations are read from. Never the working tree:
// ~/dev/lightwave-core is shared by every session on this machine and may be on
// anyone's branch, so its working tree is not a fact (CLAUDE.md §16).
const StampRef = "origin/main"

// rawJob is the on-disk shape of a job declaration, both shapes at once:
// instances still on cron_job 1.0.0 carry `handler`; 1.1.0 instances carry
// `dispatch`, `persona`, `daemon_secrets_ref` and `secret_names`.
type rawJob struct {
	Enabled          *bool    `yaml:"enabled"`
	ID               string   `yaml:"id"`
	Name             string   `yaml:"name"`
	Title            string   `yaml:"title"`
	Schedule         string   `yaml:"schedule"`
	Persona          string   `yaml:"persona"`
	DaemonSecretsRef string   `yaml:"daemon_secrets_ref"`
	SecretNames      []string `yaml:"secret_names"`
	Handler          struct {
		Transport string `yaml:"transport"`
		Command   string `yaml:"command"`
	} `yaml:"handler"`
	Dispatch Dispatch `yaml:"dispatch"`
}

// LoadJobs reads every job declaration in the scheduled-jobs family from
// lightwave-core at StampRef.
//
// Enumeration is from core rather than this binary's embedded mirror: a job
// declared on core's main but absent from the mirror, which is pinned to a tag
// that lags main, would be invisible, and under-report the very drift this verb
// exists to find.
func LoadJobs(ctx context.Context, lightwaveRoot string) ([]Job, error) {
	core := filepath.Join(lightwaveRoot, "lightwave-core")
	prefix := "src/schemas/" + JobsFamily + "/"

	names, err := gitOutput(ctx, core, "ls-tree", "--name-only", StampRef, prefix)
	if err != nil {
		return nil, fmt.Errorf("no scheduled-job declarations at %s:%s in %s (run `git fetch origin` there): %w", StampRef, prefix, core, err)
	}

	if strings.TrimSpace(names) == "" {
		return nil, fmt.Errorf("no scheduled-job declarations at %s:%s in %s", StampRef, prefix, core)
	}

	var jobs []Job

	for _, path := range strings.Split(strings.TrimSpace(names), "\n") {
		name := strings.TrimPrefix(path, prefix)
		if !strings.HasSuffix(name, ".yaml") || notAJob[name] {
			continue
		}

		data, readErr := gitOutput(ctx, core, "show", StampRef+":"+path)
		if readErr != nil {
			return nil, fmt.Errorf("read %s at %s: %w", name, StampRef, readErr)
		}

		var raw rawJob
		if err := yaml.Unmarshal([]byte(data), &raw); err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}

		// A file in the family with no id is a declaration of something other
		// than a job. Skipping it is right; failing would make one malformed
		// sibling take down the whole report.
		if raw.ID == "" {
			continue
		}

		jobs = append(jobs, jobFromRaw(&raw, JobsFamily+"/"+name))
	}

	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })

	return jobs, nil
}

func jobFromRaw(raw *rawJob, source string) Job {
	title := raw.Title
	if title == "" {
		title = raw.Name
	}

	job := Job{
		ID:               raw.ID,
		Title:            title,
		Schedule:         raw.Schedule,
		Transport:        raw.Handler.Transport,
		Command:          raw.Handler.Command,
		Source:           source,
		Persona:          raw.Persona,
		DaemonSecretsRef: raw.DaemonSecretsRef,
		SecretNames:      raw.SecretNames,
		Dispatch:         raw.Dispatch,
		Enabled:          raw.Enabled == nil || *raw.Enabled,
	}
	// A 1.1.0 shell_command is the command list and reconcile match against.
	if job.Command == "" && job.Dispatch.Kind == DispatchShell {
		job.Command = job.Dispatch.Target
	}

	return job
}

// gitOutput runs git in dir and returns stdout; the error carries git's stderr.
func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...) //nolint:gosec // fixed git verbs over a stamp path
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	var stderr strings.Builder

	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}

	return string(out), nil
}

// AgentPrefix is the label prefix for this estate's launchd agents.
const AgentPrefix = "com.lightwave."

// LoadAgents reads the LaunchAgent fleet and marks which are loaded.
func LoadAgents(ctx context.Context, home string) ([]Agent, error) {
	dir := filepath.Join(home, "Library", "LaunchAgents")

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no LaunchAgents dir is a legal state, not an error
		}

		return nil, fmt.Errorf("read %s: %w", dir, err)
	}

	loaded := loadedLabels(ctx)

	var agents []Agent

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, AgentPrefix) || !strings.HasSuffix(name, ".plist") {
			continue
		}

		path := filepath.Join(dir, name)

		f, openErr := os.Open(path) //nolint:gosec // a plist path we just enumerated
		if openErr != nil {
			continue // an unreadable plist is reported as absent, not fatal
		}

		label, program := parsePlist(f)
		f.Close()

		if label == "" {
			label = strings.TrimSuffix(name, ".plist")
		}

		agent := Agent{
			Label:   label,
			Program: program,
			Loaded:  loaded[label],
		}
		if agent.Loaded {
			agent.LastExit, agent.Runs = launchdCounters(ctx, label)
		}

		agents = append(agents, agent)
	}

	sort.Slice(agents, func(i, j int) bool { return agents[i].Label < agents[j].Label })

	return agents, nil
}

// loadedLabels returns the labels launchctl currently lists.
//
// Best-effort: launchctl failing returns an empty set, so every agent reports
// Loaded=false rather than the whole verb erroring. A missing launchctl is a
// reason to report less, not a reason to report nothing.
func loadedLabels(ctx context.Context) map[string]bool {
	out, err := exec.CommandContext(ctx, "launchctl", "list").Output()
	if err != nil {
		return map[string]bool{}
	}

	labels := map[string]bool{}

	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && strings.HasPrefix(fields[2], AgentPrefix) {
			labels[fields[2]] = true
		}
	}

	return labels
}

// launchdCounters reads `launchctl print gui/<uid>/<label>` for the agent's
// "last exit code" and "runs". Best-effort: a field launchd does not print is
// nil, never 0 — "never ran" and "unknown" are different answers.
func launchdCounters(ctx context.Context, label string) (lastExit, runs *int) {
	out, err := exec.CommandContext(ctx, "launchctl", "print", fmt.Sprintf("gui/%d/%s", os.Getuid(), label)).Output() //nolint:gosec // label from our own enumeration
	if err != nil {
		return nil, nil
	}

	return parseLaunchdCounters(string(out))
}

// parseLaunchdCounters extracts the two counters from `launchctl print`.
func parseLaunchdCounters(text string) (lastExit, runs *int) {
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " = ")
		if !ok {
			continue
		}

		n, err := strconv.Atoi(strings.Fields(value + " x")[0])
		if err != nil {
			continue
		}

		switch key {
		case "last exit code":
			lastExit = &n
		case "runs":
			runs = &n
		}
	}

	return lastExit, runs
}

// parsePlist extracts the Label and a flattened program string.
//
// A launchd plist is a flat <key>value</key> sequence inside one <dict>, so a
// value belongs to the most recent <key>. Tracking which element the character
// data sits in is what makes that reliable — keying off raw text alone would
// let a VALUE become the next key, and "com.lightwave.pr-review" is a plausible
// enough key name that the bug would not look like one.
//
// Streaming tokens avoids a plist dependency for two fields.
func parsePlist(r io.Reader) (label, program string) {
	decoder := xml.NewDecoder(r)
	decoder.Strict = false
	decoder.Entity = xml.HTMLEntity

	var (
		lastKey  string
		element  string
		inArgs   bool
		depth    int
		argDepth int
		args     []string
	)

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			element = t.Name.Local

			if element == "array" && lastKey == "ProgramArguments" {
				inArgs = true
				argDepth = depth
			}
		case xml.EndElement:
			if inArgs && t.Name.Local == "array" && depth == argDepth {
				inArgs = false
			}

			depth--
			element = ""
		case xml.CharData:
			text := strings.TrimSpace(string(t))
			if text == "" {
				continue
			}

			switch {
			case element == "key":
				lastKey = text
			case inArgs && element == "string":
				args = append(args, text)
			case lastKey == "Label" && element == "string" && label == "":
				label = text
			}
		}
	}

	return label, strings.Join(args, " ")
}
