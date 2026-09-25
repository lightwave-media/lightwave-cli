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
	"syscall"
	"text/template"
	"time"

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

// pipeWaitDelay bounds how long a finished or timed-out step may keep its
// output pipe open through a child process.
const pipeWaitDelay = 2 * time.Second

var (
	templateActionRe = regexp.MustCompile(`(?s)\{\{.*?\}\}`)
	placeholderRe    = regexp.MustCompile("\x00([0-9]+)\x00")
	valueRefRe       = regexp.MustCompile(`\.(inputs|outputs)((?:\.\w+)*)`)

	// codeValueRe is what a value may hold where it becomes code. Spaces,
	// quotes, $, ;, globs and ~ would each let it change the program.
	codeValueRe = regexp.MustCompile(`^[A-Za-z0-9_./:@%+=,-]*$`)

	interpreters = map[string]bool{
		"bash": true, "sh": true, "zsh": true, "dash": true, "ksh": true, "fish": true,
		"env": true, "xargs": true, "sudo": true,
	}
	codeFlags = map[string]bool{"-c": true, "-e": true, "--command": true, "--eval": true}
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

	if reason := r.dryRunSkip(string(src)); reason != "" {
		res.skipped = true
		res.output = "dry-run: not run — " + reason

		return res, nil
	}

	if err := r.checkCodeValues(step.ID, string(src)); err != nil {
		return res, err
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

	return res, checkExpected(step, res.output)
}

// dryRunSkip is why a script cannot run in a dry run, or "" when it can. A
// script that ignores RUNBOOK_DRY_RUN would do its work for real, and one
// reading a step that did not run has no outputs to render.
func (r *runner) dryRunSkip(src string) string {
	if !r.dryRun {
		return ""
	}

	if !strings.Contains(src, "RUNBOOK_DRY_RUN") {
		return "the script does not read RUNBOOK_DRY_RUN"
	}

	for _, ref := range valueRefs(src) {
		if ref.root != "outputs" || len(ref.path) == 0 {
			continue
		}

		if _, ran := r.outputs[ref.path[0]]; !ran {
			return fmt.Sprintf("it reads outputs of step %q, which produced none", ref.path[0])
		}
	}

	return ""
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

	return res, checkExpected(step, res.output)
}

// inlineArgv renders a command= step. Shell syntax runs under bash -c; a
// command that hands a string to an interpreter runs as argv. Both reach here
// only signed off, and in both the values become code, so they must be inert.
func (r *runner) inlineArgv(step *Step) ([]string, string, error) {
	mode := modeArgv
	if needsShell(step.Command) {
		mode = modeShell
	}

	if mode == modeShell || runsCode(step.Command) {
		if err := r.checkCodeValues(step.ID, step.Command); err != nil {
			return nil, mode, err
		}
	}

	if mode == modeShell {
		rendered, err := r.render(step.ID, step.Command)

		return []string{"bash", "-c", rendered}, mode, err
	}

	argv, err := r.renderArgv(step.ID, step.Command)

	return argv, mode, err
}

// checkExpected fails a step whose output lacks its expected= word. The
// catalog writes `test -f x && echo found || echo missing`, which exits 0
// either way.
func checkExpected(step *Step, output string) error {
	if step.Expected == "" || containsWord(output, step.Expected) {
		return nil
	}

	return fmt.Errorf("output does not contain expected %q", step.Expected)
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

	// A dry run lists what it would write and writes nothing, so the step is
	// skipped, not completed.
	if r.dryRun {
		res.skipped = true
		res.output = "dry-run: listed, nothing written — " + res.output
	}

	return res, nil
}

// exec runs one process in the working tree with the step environment and
// returns its combined output and exit code.
func (r *runner) exec(ctx context.Context, outputFile, name string, args ...string) (string, int, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // the published runbook's own step
	cmd.Dir = r.cwd
	cmd.Env = r.env(outputFile)

	// A timeout kills the step's whole process group. Killing only bash left
	// its children holding the output pipe, and CombinedOutput waited for
	// them: `sleep 600` in a script with timeoutMs=200 blocked for 600s.
	// WaitDelay bounds the wait for a child that escaped the group.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = pipeWaitDelay

	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))

	if ctx.Err() != nil {
		return output, -1, fmt.Errorf("timed out: %w", ctx.Err())
	}

	// The step exited 0 but left a background child holding stdout (a server
	// it started, say). The step succeeded; the child is its business.
	if errors.Is(err, exec.ErrWaitDelay) {
		return output, 0, nil
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
	masked, actions := maskActions(command)

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

// maskActions replaces each template action with a NUL-delimited index, so a
// shell-word split cannot cut `{{ .inputs.X }}` apart.
func maskActions(command string) (string, []string) {
	var actions []string

	masked := templateActionRe.ReplaceAllStringFunc(command, func(action string) string {
		actions = append(actions, action)

		return fmt.Sprintf("\x00%d\x00", len(actions)-1)
	})

	return masked, actions
}

// runsCode reports whether a command hands a string to an interpreter, which
// is as dangerous as shell syntax though it has none: it starts with one
// (`bash -c 'touch {{ .inputs.X }}'`, env, xargs, sudo), or a template action
// sits in the argument of -c/-e/--command/--eval (`psql -c "... {{ .x }}"`).
func runsCode(command string) bool {
	masked, _ := maskActions(command)

	words, err := shellquote.Split(masked)
	if err != nil || len(words) == 0 {
		return false
	}

	if interpreters[filepath.Base(words[0])] {
		return true
	}

	for i := 1; i < len(words); i++ {
		if codeFlags[words[i-1]] && strings.Contains(words[i], "\x00") {
			return true
		}
	}

	return false
}

// valueRef is one `.inputs[.Name]` or `.outputs[.step[.key]]` a template
// action reads. An empty path reads the whole map.
type valueRef struct {
	root string
	path []string
}

func valueRefs(text string) []valueRef {
	var refs []valueRef

	for _, action := range templateActionRe.FindAllString(text, -1) {
		for _, m := range valueRefRe.FindAllStringSubmatch(action, -1) {
			path := strings.FieldsFunc(m[2], func(c rune) bool { return c == '.' })
			refs = append(refs, valueRef{root: m[1], path: path})
		}
	}

	return refs
}

// checkCodeValues refuses to render a value into text a shell or interpreter
// parses — a script's source, a bash -c string, an interpreter's argument —
// unless it is inert there. An input of `x; touch pwned` would otherwise run,
// signed off or not: sign-off approves the command, not the values in it.
func (r *runner) checkCodeValues(stepID, text string) error {
	for _, ref := range valueRefs(text) {
		for name, value := range r.referenced(ref) {
			for _, s := range renderedStrings(value) {
				if !codeValueRe.MatchString(s) {
					return fmt.Errorf("%w: step %q renders %s=%q into code, where only letters, digits and _./:@%%+=,- "+
						"may appear (an input is also in the step's environment by name)", ErrUnsafeValue, stepID, name, s)
				}
			}
		}
	}

	return nil
}

// referenced is every value ref reads, by name.
func (r *runner) referenced(ref valueRef) map[string]any {
	found := map[string]any{}

	if ref.root == "inputs" {
		for name, value := range r.inputs {
			if len(ref.path) == 0 || ref.path[0] == name {
				found[name] = value
			}
		}

		return found
	}

	for step, outputs := range r.outputs {
		for key, value := range outputs {
			if (len(ref.path) == 0 || ref.path[0] == step) && (len(ref.path) < 2 || ref.path[1] == key) {
				found[step+"."+key] = value
			}
		}
	}

	return found
}

// renderedStrings is how a value can appear once rendered: each item of a
// list, anything else as fmt prints it.
func renderedStrings(value any) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, len(v))
		for i := range v {
			out[i] = fmt.Sprint(v[i])
		}

		return out
	default:
		return []string{fmt.Sprint(v)}
	}
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
