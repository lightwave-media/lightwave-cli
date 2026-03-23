package sst

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadStrategyFromFile(t *testing.T) {
	// Find the lightwave root by walking up from this file's directory.
	// Resolves from packages/lightwave-cli/internal/sst → root.
	// Use runtime.Caller to get this file's absolute path, then walk up to repo root.
	// File is at packages/lightwave-cli/internal/sst/strategy_test.go → 4 levels up.
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(filename), "..", "..", "..", "..")
	root, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	yamlPath := filepath.Join(root, "packages", "lightwave-core", "lightwave", "schema",
		"definitions", "governance", "strategy", "strategy.yaml")

	if _, err := os.Stat(yamlPath); os.IsNotExist(err) {
		t.Skipf("strategy YAML not found at %s — skipping integration test", yamlPath)
	}

	s, err := LoadStrategy(root)
	if err != nil {
		t.Fatalf("LoadStrategy failed: %v", err)
	}
	if len(s.Pillars) == 0 {
		t.Fatal("expected pillars, got none")
	}
	if s.Scoring.LabelMatch == 0 {
		t.Fatal("scoring not loaded")
	}

	// Score issue #74 (the real top-of-queue task).
	ctx := IssueContext{
		Labels: []string{"enhancement", "ready", "p1", "needs-architecture-review"},
		Epic:   "CineOS MVP",
		Title:  "CineOS: Wire cineos.io domain to ALB + SSL",
	}
	score := s.Score(ctx)
	if score == 0 {
		t.Errorf("expected non-zero strategy score for cineos domain issue, got 0")
	}
	t.Logf("strategy score for #74: %d (across %d pillars)", score, len(s.Pillars))
}

func TestScoreLabelsMatch(t *testing.T) {
	s := &Strategy{
		Pillars: []Pillar{
			{
				ID:     "cineos_core",
				Weight: 9,
				Match: MatchRules{
					Labels: []string{"cineos", "product"},
				},
			},
		},
		Scoring: Scoring{
			LabelMatch:         3,
			EpicMatch:          5,
			KeywordMatch:       1,
			AlignmentThreshold: 3,
		},
	}

	ctx := IssueContext{
		Labels: []string{"cineos", "ready", "p1"},
		Title:  "CineOS: Wire domain",
	}

	score := s.Score(ctx)
	// 1 label match (cineos) = 3 points, >= threshold 3, * weight 9 = 27
	if score != 27 {
		t.Errorf("expected score 27, got %d", score)
	}
}

func TestScoreBelowThreshold(t *testing.T) {
	s := &Strategy{
		Pillars: []Pillar{
			{
				ID:     "billing",
				Weight: 7,
				Match: MatchRules{
					Keywords: []string{"stripe"},
				},
			},
		},
		Scoring: Scoring{
			LabelMatch:         3,
			EpicMatch:          5,
			KeywordMatch:       1,
			AlignmentThreshold: 3,
		},
	}

	ctx := IssueContext{
		Title: "Fix typo in stripe docs",
	}

	score := s.Score(ctx)
	// 1 keyword match = 1 point, below threshold 3 → 0
	if score != 0 {
		t.Errorf("expected score 0 (below threshold), got %d", score)
	}
}

func TestScoreMultiplePillars(t *testing.T) {
	s := &Strategy{
		Pillars: []Pillar{
			{
				ID:     "platform",
				Weight: 10,
				Match: MatchRules{
					Labels:   []string{"infrastructure"},
					Keywords: []string{"ssl", "domain"},
				},
			},
			{
				ID:     "cineos",
				Weight: 9,
				Match: MatchRules{
					Labels:   []string{"cineos"},
					Keywords: []string{"cineos"},
				},
			},
		},
		Scoring: Scoring{
			LabelMatch:         3,
			EpicMatch:          5,
			KeywordMatch:       1,
			AlignmentThreshold: 3,
		},
	}

	ctx := IssueContext{
		Labels: []string{"cineos", "infrastructure"},
		Title:  "CineOS: Wire cineos.io domain to ALB + SSL",
	}

	score := s.Score(ctx)
	// Platform: 1 label (3) + 2 keywords ssl+domain (2) = 5, >= 3, * 10 = 50
	// CineOS: 1 label (3) + 1 keyword cineos (1) = 4, >= 3, * 9 = 36
	// Total = 86
	if score != 86 {
		t.Errorf("expected score 86, got %d", score)
	}
}

func TestScoreNoMatch(t *testing.T) {
	s := &Strategy{
		Pillars: []Pillar{
			{
				ID:     "billing",
				Weight: 7,
				Match: MatchRules{
					Labels: []string{"billing", "stripe"},
				},
			},
		},
		Scoring: Scoring{
			LabelMatch:         3,
			EpicMatch:          5,
			KeywordMatch:       1,
			AlignmentThreshold: 3,
		},
	}

	ctx := IssueContext{
		Labels: []string{"ready", "p2"},
		Title:  "Fix login button color",
	}

	score := s.Score(ctx)
	if score != 0 {
		t.Errorf("expected score 0, got %d", score)
	}
}

func TestScoreEpicMatch(t *testing.T) {
	s := &Strategy{
		Pillars: []Pillar{
			{
				ID:     "agent_system",
				Weight: 8,
				Match: MatchRules{
					Epics: []string{"brain-architecture", "orchestrator"},
				},
			},
		},
		Scoring: Scoring{
			LabelMatch:         3,
			EpicMatch:          5,
			KeywordMatch:       1,
			AlignmentThreshold: 3,
		},
	}

	ctx := IssueContext{
		Epic:  "brain-architecture",
		Title: "Augusta memory module",
	}

	score := s.Score(ctx)
	// 1 epic match = 5 points, >= 3, * 8 = 40
	if score != 40 {
		t.Errorf("expected score 40, got %d", score)
	}
}
