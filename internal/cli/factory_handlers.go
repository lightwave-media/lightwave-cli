package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lightwave-media/lightwave-cli/internal/githuborg"
	"gopkg.in/yaml.v3"
)

func init() {
	RegisterHandler("factory.plan", factoryPlanHandler)
	RegisterHandler("factory.apply", factoryApplyHandler)
}

type manifestFile struct {
	Steps []manifestStep `yaml:"steps"`
}

type manifestStep struct {
	ID   string `yaml:"id"`
	Kind string `yaml:"kind"`
	Repo string `yaml:"repo"`
	Name string `yaml:"name"`
	Org  string `yaml:"org"`
}

func factoryPlanHandler(_ context.Context, _ []string, flags map[string]any) error {
	m, err := loadManifest(flags)
	if err != nil {
		return err
	}

	fmt.Printf("factory plan: %d steps\n", len(m.Steps))

	for i, s := range m.Steps {
		fmt.Printf("  %d. [%s] %s (%s)\n", i+1, s.Kind, s.ID, stepTarget(&s))
	}

	return nil
}

func factoryApplyHandler(ctx context.Context, _ []string, flags map[string]any) error {
	if flagBool(flags, "dry-run") {
		return factoryPlanHandler(ctx, nil, flags)
	}

	m, err := loadManifest(flags)
	if err != nil {
		return err
	}

	for i, step := range m.Steps {
		fmt.Printf("factory apply step %d/%d: [%s] %s\n", i+1, len(m.Steps), step.Kind, step.ID)

		if err := dispatchFactoryStep(ctx, &step, flags); err != nil {
			return fmt.Errorf("step %s: %w", step.ID, err)
		}
	}

	fmt.Printf("factory apply: completed %d steps\n", len(m.Steps))

	return nil
}

func dispatchFactoryStep(ctx context.Context, step *manifestStep, flags map[string]any) error {
	switch step.Kind {
	case "org-sync", "github-org-bootstrap", "org-bootstrap":
		opts := githuborg.Options{
			Org:           stepOrg(step),
			LightwaveRoot: lightwaveRoot(),
		}
		if step.Repo != "" {
			opts.TargetRepo = step.Repo
		}

		return githuborg.RunBootstrap(ctx, opts)
	case "scrum-sync", "project-board-hygiene":
		opts := githuborg.Options{
			Org:           stepOrg(step),
			LightwaveRoot: lightwaveRoot(),
			TargetRepo:    step.Repo,
		}
		_, err := githuborg.Sync(ctx, opts)

		return err
	case "create-repo", "repo-bootstrap":
		name := step.Name
		if name == "" {
			name = step.Repo
		}

		if name == "" {
			return errors.New("create-repo step missing name/repo")
		}

		return createRepoHandler(ctx, []string{name}, map[string]any{
			"org":  stepOrg(step),
			"kind": flagStr(flags, "kind"),
		})
	default:
		return fmt.Errorf("unsupported manifest kind %q", step.Kind)
	}
}

func stepOrg(step *manifestStep) string {
	if step.Org != "" {
		return step.Org
	}

	return githuborg.DefaultOrg
}

func stepTarget(step *manifestStep) string {
	if step.Repo != "" {
		return step.Repo
	}

	if step.Name != "" {
		return step.Name
	}

	return stepOrg(step)
}

func loadManifest(flags map[string]any) (*manifestFile, error) {
	path := flagStr(flags, "manifest")
	if path == "" {
		session := flagStr(flags, "session")
		repo, _ := os.Getwd()
		path = filepath.Join(repo, ".tasks", session, "kickoff", "scaffold_manifest.yaml")
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var m manifestFile
	if err := yaml.Unmarshal(b, &m); err != nil {
		return nil, err
	}

	return &m, nil
}
