package runbook

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// InputDecl is one variable a runbook's <Inputs> block declares, in the
// boilerplate.yml shape the Runbooks app renders as a form.
type InputDecl struct {
	Name        string   `yaml:"name"`
	Type        string   `yaml:"type"`
	Default     any      `yaml:"default"`
	Options     []string `yaml:"options"`
	Validations []string `yaml:"validations"`
}

var (
	ErrUnknownInput = errors.New("runbook has no such input")
	ErrInvalidInput = errors.New("invalid runbook input")

	// <Inputs id="x">```yaml ... ```</Inputs>, or <Inputs id="x" path="templates/t" />
	// whose variables live in that blueprint's boilerplate.yml.
	inputsRe = regexp.MustCompile("(?s)<Inputs\\b((?:[^>\"]|\"(?:[^\"\\\\]|\\\\.)*\")*?)(/?)>(?:\\s*```ya?ml\\s*\\n(.*?)```)?")

	regexValidationRe = regexp.MustCompile(`^regex\("(.*)"\)$`)
)

func parseInputs(mdx, runbookDir string) ([]InputDecl, error) {
	var decls []InputDecl

	for _, m := range inputsRe.FindAllStringSubmatch(mdx, -1) {
		attrs := parseAttrs(m[1])
		body := m[3]

		if path := attrs["path"]; path != "" {
			raw, err := os.ReadFile(filepath.Join(runbookDir, path, "boilerplate.yml"))
			if err != nil {
				return nil, fmt.Errorf("inputs %q: %w", attrs["id"], err)
			}

			body = string(raw)
		}

		var doc struct {
			Variables []InputDecl `yaml:"variables"`
		}
		if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
			return nil, fmt.Errorf("inputs %q: %w", attrs["id"], err)
		}

		decls = append(decls, doc.Variables...)
	}

	return decls, nil
}

// BindInputs resolves every declared input from vars, falling back to the
// declared default, and refuses what the Runbooks app's form would refuse: an
// undeclared name (a typo would otherwise vanish), a missing required value,
// a value of the wrong type, an enum value off the list, a failed regex.
func BindInputs(decls []InputDecl, vars map[string]string) (map[string]any, error) {
	declared := make(map[string]bool, len(decls))
	for _, d := range decls {
		declared[d.Name] = true
	}

	for name := range vars {
		if !declared[name] {
			return nil, fmt.Errorf("%w %q (declared: %s)", ErrUnknownInput, name, strings.Join(inputNames(decls), ", "))
		}
	}

	bound := make(map[string]any, len(decls))

	for i := range decls {
		d := &decls[i]

		value, err := d.bind(vars)
		if err != nil {
			return nil, err
		}

		bound[d.Name] = value
	}

	return bound, nil
}

func (d *InputDecl) bind(vars map[string]string) (any, error) {
	raw, given := vars[d.Name]
	if !given && d.Default != nil {
		return d.Default, nil
	}

	if raw == "" && d.required() {
		return nil, fmt.Errorf("%w: %s is required", ErrInvalidInput, d.Name)
	}

	value, err := d.parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrInvalidInput, d.Name, err)
	}

	return value, d.checkRegex(raw)
}

// parse types a --var string the way the form would. An empty optional value
// is the type's zero value, so templates like {{- if .inputs.X }} stay false.
func (d *InputDecl) parse(raw string) (any, error) {
	switch d.Type {
	case "bool":
		if raw == "" {
			return false, nil
		}

		return strconv.ParseBool(raw)
	case "int":
		if raw == "" {
			return 0, nil
		}

		return strconv.Atoi(raw)
	case "enum":
		if raw != "" && !slices.Contains(d.Options, raw) {
			return nil, fmt.Errorf("%q is not one of %s", raw, strings.Join(d.Options, ", "))
		}

		return raw, nil
	case "list":
		if raw == "" {
			return []string{}, nil
		}

		return strings.Split(raw, ","), nil
	default:
		return raw, nil
	}
}

func (d *InputDecl) checkRegex(raw string) error {
	for _, v := range d.Validations {
		m := regexValidationRe.FindStringSubmatch(v)
		if m == nil || raw == "" {
			continue
		}

		re, err := regexp.Compile(m[1])
		if err != nil {
			return fmt.Errorf("%w: %s declares a bad regex: %w", ErrInvalidInput, d.Name, err)
		}

		if !re.MatchString(raw) {
			return fmt.Errorf("%w: %s=%q does not match %s", ErrInvalidInput, d.Name, raw, m[1])
		}
	}

	return nil
}

func (d *InputDecl) required() bool {
	return slices.Contains(d.Validations, "required")
}

// inputEnv renders bound inputs as NAME=value for a step's environment, the
// second way the catalog's scripts read them (session-signoff reads $PrUrl).
func inputEnv(bound map[string]any) []string {
	env := make([]string, 0, len(bound))
	for name, value := range bound {
		env = append(env, name+"="+inputString(value))
	}

	sort.Strings(env)

	return env
}

func inputString(value any) string {
	switch v := value.(type) {
	case []string:
		return strings.Join(v, ",")
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = fmt.Sprint(item)
		}

		return strings.Join(parts, ",")
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

// ParseVars merges a --vars-file (YAML mapping) with --var Key=Value pairs;
// a --var wins over the file.
func ParseVars(pairs []string, file string) (map[string]string, error) {
	vars := map[string]string{}

	if file != "" {
		raw, err := os.ReadFile(file) //nolint:gosec // an operator-named vars file
		if err != nil {
			return nil, err
		}

		var doc map[string]any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("vars file %s: %w", file, err)
		}

		for name, value := range doc {
			vars[name] = inputString(value)
		}
	}

	for _, pair := range pairs {
		name, value, ok := strings.Cut(pair, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("--var %q: want Key=Value", pair)
		}

		vars[name] = value
	}

	return vars, nil
}

func inputNames(decls []InputDecl) []string {
	names := make([]string, len(decls))
	for i, d := range decls {
		names[i] = d.Name
	}

	sort.Strings(names)

	return names
}
