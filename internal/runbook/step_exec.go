package runbook

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	shellquote "github.com/kballard/go-shellquote"

	"github.com/lightwave-media/lightwave-cli/internal/blueprint"
)

// The step contract below is the Runbooks app's, measured against the
// lightwave-core catalog rather than assumed: scripts call the injected
// log_info/log_warn/log_error, read inputs as {{ .inputs.X }} text or as $X,
// read RUNBOOK_DRY_RUN, write key=value lines to RUNBOOK_OUTPUT (later steps
// read them as {{ .outputs.<step>.<key> }}), and exit 0 ok, 2 warn, 1 fail.

// logHelpers are the functions the Runbooks app injects into every script.
// Under `set -e` a script calling one that is missing dies on its first line.
const logHelpers = `log_info()  { printf '[INFO] %s\n' "$*"; }
log_warn()  { printf '[WARN] %s\n' "$*" >&2; }
log_error() { printf '[ERROR] %s\n' "$*" >&2; }
`

const (
	exitWarn = 2

	modeScript   = "script"
	modeArgv     = "argv"
	modeShell    = "shell"
	modeTemplate = "template"
	modeProse    = "prose"
)

var (
	templateActionRe = regexp.MustCompile(`(?s)\{\{.*?\}\}`)
	placeholderRe    = regexp.MustCompile("\x00([0-9]+)\x00")
)

// stepResult is what one step did.
type stepResult struct {
	output  string
	outputs map[string]string
	mode    string
	exit    int
	skipped bool
	warned  bool
}

// runner executes an instance's steps with its bound inputs.
type runner struct {
	inputs     map[string]any
	outputs    map[string]map[string]string
	cwd        string
	runbookDir string
	dryRun     bool
}

func (r *runner) run(step *Step) (stepResult, error) {
	switch {
	case step.Kind == KindTemplate:
		return r.renderTemplate(step)
	case step.Path != "":
		return r.runScript(step)
	case step.Command != "":
		return r.runInline(step)
	default:
		return stepResult{mode: modeProse}, nil
	}
}

// runScript runs a path= script as `bash <rendered copy>`. The script is a
// stamp-controlled file; its {{ .inputs }} are rendered before it runs.
func (r *runner) runScript(step *Step) (stepResult, error) {
	res := stepResult{mode: modeScript}

	src, err := os.ReadFile(filepath.Join(r.runbookDir, step.Path)) //nolint:gosec // a script inside the published runbook
	if err != nil {
		return res, fmt.Errorf("step %q: %w", step.ID, err)
	}

	if r.dryRun && !strings.Contains(string(src), "RUNBOOK_DRY_RUN") {
		res.skipped = true
		res.output = "dry-run: not run — the script does not read RUNBOOK_DRY_RUN"

		return res, nil
	}

	body, err := r.render(step.ID, string(src))
	if err != nil {
		return res, err
	}

	dir, err := os.MkdirTemp("", "lw-runbook-step-")
	if err != nil {
		return res, err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	script := filepath.Join(dir, "step.sh")
	if err := os.WriteFile(script, []byte(logHelpers+body), filePerm); err != nil {
		return res, err
	}

	outputFile := filepath.Join(dir, "output")
	if err := os.WriteFile(outputFile, nil, filePerm); err != nil {
		return res, err
	}

	ctx, cancel := stepContext(step)
	defer cancel()

	res.output, res.exit, err = r.exec(ctx, outputFile, "bash", script)
	res.outputs = readOutputs(outputFile)
	res.warned = res.exit == exitWarn

	if err != nil && !res.warned {
		return res, fmt.Errorf("script %s: %w", step.Path, err)
	}

	return res, nil
}

// runInline runs a command= step. Without shell syntax it runs as argv, each
// word rendered on its own, so an input value is one argument and never new
// syntax. With shell syntax it only reaches here signed off (HighBlast), and
// runs under bash -c.
func (r *runner) runInline(step *Step) (stepResult, error) {
	if r.dryRun {
		return stepResult{mode: modeArgv, skipped: true, output: "dry-run: not run — inline commands have no dry-run contract"}, nil
	}

	argv, mode, err := r.inlineArgv(step)
	res := stepResult{mode: mode}

	if err != nil {
		return res, err
	}

	ctx, cancel := stepContext(step)
	defer cancel()

	res.output, res.exit, err = r.exec(ctx, "", argv[0], argv[1:]...)
	if err != nil {
		return res, err
	}

	if step.Expected != "" && !containsWord(res.output, step.Expected) {
		return res, fmt.Errorf("output does not contain expected %q", step.Expected)
	}

	return res, nil
}

func (r *runner) inlineArgv(step *Step) ([]string, string, error) {
	if needsShell(step.Command) {
		rendered, err := r.render(step.ID, step.Command)

		return []string{"bash", "-c", rendered}, modeShell, err
	}

	argv, err := r.renderArgv(step.ID, step.Command)

	return argv, modeArgv, err
}

// renderTemplate renders a Template step's blueprint into the working tree
// through the linked boilerplate engine, fed the runbook's inputs. Dry-run
// stages and lists the files without writing them.
//
// step.Path is relative to the runbook's own directory:
//
//	<Template id="product" path="templates/product-module" target="worktree" />
func (r *runner) renderTemplate(step *Step) (stepResult, error) {
	res := stepResult{mode: modeTemplate}

	if step.Path == "" {
		return res, fmt.Errorf("template step %q has no path attribute", step.ID)
	}

	src := filepath.Join(r.runbookDir, step.Path)
	if _, err := os.Stat(src); err != nil {
		return res, fmt.Errorf("template step %q: %w", step.ID, err)
	}

	// "worktree" (or unset) renders at the working tree root. Any other value
	// is relative to it; absolute targets are refused so a runbook cannot
	// write outside the tree it was applied in.
	dest := r.cwd

	if step.Target != "" && step.Target != "worktree" {
		if filepath.IsAbs(step.Target) {
			return res, fmt.Errorf("template step %q: absolute target %q is not allowed", step.ID, step.Target)
		}

		dest = filepath.Join(r.cwd, step.Target)
	}

	vars := make([]string, 0, len(r.inputs))
	for name, value := range r.inputs {
		vars = append(vars, name+"="+inputString(value))
	}

	if err := blueprint.Render(context.Background(), &blueprint.RenderOptions{
		BlueprintPath: src,
		OutputFolder:  dest,
		Vars:          vars,
		DryRun:        r.dryRun,
	}); err != nil {
		return res, fmt.Errorf("template step %q: %w", step.ID, err)
	}

	res.output = fmt.Sprintf("rendered %s -> %s", step.Path, dest)

	return res, nil
}

// exec runs one process in the working tree with the step environment and
// returns its combined output and exit code.
func (r *runner) exec(ctx context.Context, outputFile, name string, args ...string) (string, int, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // the published runbook's own step
	cmd.Dir = r.cwd
	cmd.Env = r.env(outputFile)

	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))

	if ctx.Err() != nil {
		return output, -1, fmt.Errorf("timed out: %w", ctx.Err())
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return output, exitErr.ExitCode(), err
	}

	return output, 0, err
}

func (r *runner) env(outputFile string) []string {
	if outputFile == "" {
		outputFile = os.DevNull
	}

	env := append(os.Environ(), inputEnv(r.inputs)...)

	return append(env,
		"RUNBOOK_DRY_RUN="+strconv.FormatBool(r.dryRun),
		"RUNBOOK_OUTPUT="+outputFile,
		"RUNBOOK_DIR="+r.runbookDir,
		"GENERATED_FILES="+r.cwd,
	)
}

// render executes text as a Go template over the inputs and earlier steps'
// outputs. An undeclared input or a missing output is an error, not "".
func (r *runner) render(stepID, text string) (string, error) {
	tmpl, err := template.New(stepID).Option("missingkey=error").Parse(text)
	if err != nil {
		return "", fmt.Errorf("step %q: template: %w", stepID, err)
	}

	var b strings.Builder
	if err := tmpl.Execute(&b, map[string]any{"inputs": r.inputs, "outputs": r.outputs}); err != nil {
		return "", fmt.Errorf("step %q: template: %w", stepID, err)
	}

	return b.String(), nil
}

// renderArgv splits command into words before rendering it. Template actions
// are masked first so `{{ .inputs.X }}` stays inside one word.
func (r *runner) renderArgv(stepID, command string) ([]string, error) {
	var actions []string

	masked := templateActionRe.ReplaceAllStringFunc(command, func(action string) string {
		actions = append(actions, action)

		return fmt.Sprintf("\x00%d\x00", len(actions)-1)
	})

	words, err := shellquote.Split(masked)
	if err != nil {
		return nil, fmt.Errorf("step %q: %w", stepID, err)
	}

	if len(words) == 0 {
		return nil, fmt.Errorf("step %q: empty command", stepID)
	}

	for i, word := range words {
		word = placeholderRe.ReplaceAllStringFunc(word, func(p string) string {
			n, _ := strconv.Atoi(strings.Trim(p, "\x00"))

			return actions[n]
		})

		if words[i], err = r.render(stepID, word); err != nil {
			return nil, err
		}
	}

	return words, nil
}

// needsShell reports whether a command uses syntax only a shell can run:
// pipes, lists, redirects, expansions, subshells, globs, a leading ~, or a
// newline. Template actions are ignored — {{ .x | lower }} is not a pipe.
func needsShell(command string) bool {
	masked := templateActionRe.ReplaceAllString(command, "x")

	var quote rune

	atWordStart := true

	for i := 0; i < len(masked); i++ {
		c := rune(masked[i])

		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			}
		case quote == '"':
			switch c {
			case '"':
				quote = 0
			case '$', '`':
				return true
			case '\\':
				i++
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '\\':
			i++
		case strings.ContainsRune("|&;<>()$`*?\n", c):
			return true
		case c == '~' && atWordStart:
			return true
		}

		atWordStart = quote == 0 && (c == ' ' || c == '\t')
	}

	return false
}

func stepContext(step *Step) (context.Context, context.CancelFunc) {
	if step.Timeout > 0 {
		return context.WithTimeout(context.Background(), step.Timeout)
	}

	return context.WithCancel(context.Background())
}

// readOutputs parses the key=value lines a step wrote to RUNBOOK_OUTPUT.
func readOutputs(path string) map[string]string {
	f, err := os.Open(path) //nolint:gosec // the step's own temp file
	if err != nil {
		return nil
	}
	defer f.Close()

	outputs := map[string]string{}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if key, value, ok := strings.Cut(scanner.Text(), "="); ok && key != "" {
			outputs[strings.TrimSpace(key)] = value
		}
	}

	if len(outputs) == 0 {
		return nil
	}

	return outputs
}

// containsWord reports whether output contains want as a whole word, so an
// expected "0" matches "     0" but not "10".
func containsWord(output, want string) bool {
	re := regexp.MustCompile(`(^|\W)` + regexp.QuoteMeta(want) + `(\W|$)`)

	return re.MatchString(output)
}
