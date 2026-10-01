package cron

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"sort"
	"strings"
)

// The renderer for `lw cron sync`. One LaunchAgent value, two encoders:
//
//   - EncodeNix writes a nix-darwin `launchd.user.agents.<label>` attrset.
//     That is what sync writes: since 2026-10-01 no launchd job on this host is
//     loaded by hand, and nix-darwin is the loader.
//   - EncodePlist writes the launchd XML the same agent becomes. It is never
//     loaded by sync; it exists so every render can be checked with
//     `plutil -lint` and pinned by golden files without a nix build.
//
// Not a gruntwork boilerplate template, deliberately: boilerplate renders Go
// text/template, which does not escape XML or nix strings, and every field
// here comes from a stamp, so a `<`, `&` or `${` in a command or prompt would
// render a broken or injected agent. Each encoder escapes for its own format.

// LabelPrefix is the launchd label prefix of a rendered job.
const LabelPrefix = AgentPrefix + "cron."

// LaunchAgent is one rendered job, format-neutral.
type LaunchAgent struct {
	Environment       map[string]string
	Label             string
	StandardOutPath   string
	StandardErrorPath string
	ProgramArguments  []string
	Intervals         []Interval
}

// RenderOptions are the host paths a render needs; nothing else varies.
type RenderOptions struct {
	// LwPath is the lw binary the agent runs.
	LwPath string
	// LogDir holds each agent's stdout and stderr.
	LogDir string
	// Path is the agent's PATH.
	Path string
}

// Render turns a job sync may render into its LaunchAgent. Call Refusal first.
//
// ProgramArguments are `lw config exec --only <names> -- lw cron run <id>`, or
// just `lw cron run <id>` when the job runs with no secrets: the job holds
// exactly its entitled keys, by name, and nothing else (CLAUDE.md §24).
func Render(job *Job, opts RenderOptions) (LaunchAgent, error) {
	intervals, err := Intervals(job.Schedule)
	if err != nil {
		return LaunchAgent{}, err
	}

	label := LabelPrefix + job.ID
	args := []string{}

	if len(job.SecretNames) > 0 {
		names := append([]string(nil), job.SecretNames...)
		sort.Strings(names)
		args = append(args, opts.LwPath, "config", "exec", "--only", strings.Join(names, ","), "--")
	}

	args = append(args, opts.LwPath, "cron", "run", job.ID)

	return LaunchAgent{
		Label:             label,
		ProgramArguments:  args,
		Intervals:         intervals,
		StandardOutPath:   opts.LogDir + "/" + label + ".stdout.log",
		StandardErrorPath: opts.LogDir + "/" + label + ".stderr.log",
		Environment: map[string]string{
			"PATH":        opts.Path,
			"LW_AGENT_ID": job.Persona,
			// lw config exec reads SSM under the read-only agent profile.
			"AWS_PROFILE": "lightwave-agent",
		},
	}, nil
}

func intervalPairs(iv Interval) [][2]any {
	var pairs [][2]any

	for _, kv := range []struct {
		val *int
		key string
	}{{iv.Day, "Day"}, {iv.Hour, "Hour"}, {iv.Minute, "Minute"}, {iv.Month, "Month"}, {iv.Weekday, "Weekday"}} {
		if kv.val != nil {
			pairs = append(pairs, [2]any{kv.key, *kv.val})
		}
	}

	return pairs
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}

// EncodePlist writes the launchd property list for a.
func EncodePlist(a *LaunchAgent) []byte {
	var b bytes.Buffer

	esc := func(s string) string {
		var e bytes.Buffer

		_ = xml.EscapeText(&e, []byte(s))

		return e.String()
	}
	str := func(indent, s string) { fmt.Fprintf(&b, "%s<string>%s</string>\n", indent, esc(s)) }
	key := func(indent, k string) { fmt.Fprintf(&b, "%s<key>%s</key>\n", indent, esc(k)) }

	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n<dict>\n")

	key("  ", "Label")
	str("  ", a.Label)

	key("  ", "ProgramArguments")
	b.WriteString("  <array>\n")

	for _, arg := range a.ProgramArguments {
		str("    ", arg)
	}

	b.WriteString("  </array>\n")

	key("  ", "EnvironmentVariables")
	b.WriteString("  <dict>\n")

	for _, k := range sortedKeys(a.Environment) {
		key("    ", k)
		str("    ", a.Environment[k])
	}

	b.WriteString("  </dict>\n")

	key("  ", "StartCalendarInterval")
	b.WriteString("  <array>\n")

	for _, iv := range a.Intervals {
		b.WriteString("    <dict>\n")

		for _, p := range intervalPairs(iv) {
			key("      ", p[0].(string))
			fmt.Fprintf(&b, "      <integer>%d</integer>\n", p[1].(int))
		}

		b.WriteString("    </dict>\n")
	}

	b.WriteString("  </array>\n")

	key("  ", "StandardOutPath")
	str("  ", a.StandardOutPath)
	key("  ", "StandardErrorPath")
	str("  ", a.StandardErrorPath)

	b.WriteString("</dict>\n</plist>\n")

	return b.Bytes()
}

// nixString quotes s as a nix double-quoted string: backslash, the quote and
// the interpolation opener `${` are the three things that change meaning.
func nixString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "${", `\${`)

	return `"` + s + `"`
}

// EncodeNix writes one nix module declaring every agent in agents as a
// nix-darwin `launchd.user.agents` entry, ordered by label.
func EncodeNix(agents []LaunchAgent, source string) []byte {
	var b bytes.Buffer

	sorted := append([]LaunchAgent(nil), agents...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Label < sorted[j].Label })

	fmt.Fprintf(&b, "# Rendered by `lw cron sync` from %s. Do not edit: change the stamp\n", source)
	b.WriteString("# in lightwave-core and re-run `lw cron sync --out <this checkout>`.\n")
	b.WriteString("{ ... }:\n{\n")

	for _, a := range sorted {
		fmt.Fprintf(&b, "  launchd.user.agents.%s.serviceConfig = {\n", nixString(a.Label))
		fmt.Fprintf(&b, "    Label = %s;\n", nixString(a.Label))

		b.WriteString("    ProgramArguments = [")

		for _, arg := range a.ProgramArguments {
			b.WriteString(" " + nixString(arg))
		}

		b.WriteString(" ];\n    EnvironmentVariables = {\n")

		for _, k := range sortedKeys(a.Environment) {
			fmt.Fprintf(&b, "      %s = %s;\n", k, nixString(a.Environment[k]))
		}

		b.WriteString("    };\n    StartCalendarInterval = [\n")

		for _, iv := range a.Intervals {
			b.WriteString("      {")

			for _, p := range intervalPairs(iv) {
				fmt.Fprintf(&b, " %s = %d;", p[0], p[1])
			}

			b.WriteString(" }\n")
		}

		b.WriteString("    ];\n")
		fmt.Fprintf(&b, "    StandardOutPath = %s;\n", nixString(a.StandardOutPath))
		fmt.Fprintf(&b, "    StandardErrorPath = %s;\n", nixString(a.StandardErrorPath))
		b.WriteString("  };\n")
	}

	b.WriteString("}\n")

	return b.Bytes()
}
