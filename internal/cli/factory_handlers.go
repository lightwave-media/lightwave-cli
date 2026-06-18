package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func init() {
	RegisterHandler("factory.plan", factoryPlanHandler)
	RegisterHandler("factory.apply", factoryApplyHandler)
}

type manifestFile struct {
	Steps []struct {
		ID   string `yaml:"id"`
		Kind string `yaml:"kind"`
		Repo string `yaml:"repo"`
	} `yaml:"steps"`
}

func factoryPlanHandler(_ context.Context, _ []string, flags map[string]any) error {
	m, err := loadManifest(flags)
	if err != nil {
		return err
	}

	fmt.Printf("factory plan: %d steps\n", len(m.Steps))

	for i, s := range m.Steps {
		fmt.Printf("  %d. [%s] %s (%s)\n", i+1, s.Kind, s.ID, s.Repo)
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

	fmt.Printf("factory apply: executed %d steps (stub dispatch)\n", len(m.Steps))

	return nil
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
