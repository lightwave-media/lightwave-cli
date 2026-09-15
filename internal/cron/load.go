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

// rawJob is the on-disk shape of a job declaration.
//
// Note it carries `handler`, while cron_job.yaml's required_fields name
// `dispatch`. The instances and the shape disagree; this reads what the
// instances actually contain.
type rawJob struct {
	ID       string `yaml:"id"`
	Title    string `yaml:"title"`
	Schedule string `yaml:"schedule"`
	Handler  struct {
		Transport string `yaml:"transport"`
		Command   string `yaml:"command"`
	} `yaml:"handler"`
}

// LoadJobs reads every job declaration in the scheduled-jobs family.
//
// Enumeration is from the lightwave-core checkout when present. A job declared
// on core's main but absent from this binary's embedded mirror would otherwise
// be invisible, and the mirror is pinned to a tag that lags main — so
// enumerating from the snapshot alone would under-report the very drift this
// verb exists to find.
func LoadJobs(lightwaveRoot string) ([]Job, error) {
	dir := filepath.Join(lightwaveRoot, "lightwave-core", "src", "schemas", JobsFamily)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("no scheduled-job declarations at %s: %w", dir, err)
	}

	var jobs []Job

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".yaml") || notAJob[name] {
			continue
		}

		data, readErr := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // a stamp path we just enumerated
		if readErr != nil {
			return nil, fmt.Errorf("read %s: %w", name, readErr)
		}

		var raw rawJob
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}

		// A file in the family with no id is a declaration of something other
		// than a job. Skipping it is right; failing would make one malformed
		// sibling take down the whole report.
		if raw.ID == "" {
			continue
		}

		jobs = append(jobs, Job{
			ID:        raw.ID,
			Title:     raw.Title,
			Schedule:  raw.Schedule,
			Transport: raw.Handler.Transport,
			Command:   raw.Handler.Command,
			Source:    JobsFamily + "/" + name,
		})
	}

	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })

	return jobs, nil
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

		agents = append(agents, Agent{
			Label:   label,
			Program: program,
			Loaded:  loaded[label],
		})
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
