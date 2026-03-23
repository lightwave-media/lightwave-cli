package sst

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Strategy represents the loaded strategy alignment config from SST.
type Strategy struct {
	Pillars []Pillar `yaml:"pillars"`
	Scoring Scoring  `yaml:"scoring"`
}

// Pillar is a strategic priority area with matching rules.
type Pillar struct {
	ID          string      `yaml:"id"`
	Name        string      `yaml:"name"`
	Description string      `yaml:"description"`
	Weight      int         `yaml:"weight"`
	Horizon     string      `yaml:"horizon"`
	Match       MatchRules  `yaml:"match"`
}

// MatchRules define how to detect alignment from issue metadata.
type MatchRules struct {
	Labels   []string `yaml:"labels"`
	Epics    []string `yaml:"epics"`
	Keywords []string `yaml:"keywords"`
}

// Scoring configures point values for each match type.
type Scoring struct {
	LabelMatch         int `yaml:"label_match"`
	EpicMatch          int `yaml:"epic_match"`
	KeywordMatch       int `yaml:"keyword_match"`
	AlignmentThreshold int `yaml:"alignment_threshold"`
}

// strategyFile wraps the YAML structure including _meta.
type strategyFile struct {
	Pillars []Pillar `yaml:"pillars"`
	Scoring Scoring  `yaml:"scoring"`
}

// LoadStrategy reads the strategy YAML from the SST definitions directory.
// It resolves the path relative to the lightwave root.
func LoadStrategy(lightwaveRoot string) (*Strategy, error) {
	path := filepath.Join(
		lightwaveRoot,
		"packages", "lightwave-core", "lightwave", "schema", "definitions",
		"governance", "strategy", "strategy.yaml",
	)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read strategy YAML: %w", err)
	}

	var sf strategyFile
	if err := yaml.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("parse strategy YAML: %w", err)
	}

	return &Strategy{
		Pillars: sf.Pillars,
		Scoring: sf.Scoring,
	}, nil
}

// IssueContext holds the metadata extracted from a GitHub Issue for scoring.
type IssueContext struct {
	Labels []string // GitHub labels (lowercase)
	Epic   string   // Epic name from issue body
	Title  string   // Issue title
	Body   string   // Issue body text
}

// Score calculates the strategy alignment score for an issue.
// Returns the total weighted score across all matching pillars.
func (s *Strategy) Score(ctx IssueContext) int {
	totalScore := 0

	// Normalize inputs for case-insensitive matching
	lowerLabels := make(map[string]bool, len(ctx.Labels))
	for _, l := range ctx.Labels {
		lowerLabels[strings.ToLower(l)] = true
	}
	lowerEpic := strings.ToLower(ctx.Epic)
	lowerTitle := strings.ToLower(ctx.Title)
	lowerBody := strings.ToLower(ctx.Body)
	searchText := lowerTitle + " " + lowerBody

	for _, pillar := range s.Pillars {
		pillarScore := 0

		// Label matching
		for _, matchLabel := range pillar.Match.Labels {
			if lowerLabels[strings.ToLower(matchLabel)] {
				pillarScore += s.Scoring.LabelMatch
			}
		}

		// Epic matching
		for _, matchEpic := range pillar.Match.Epics {
			if strings.Contains(lowerEpic, strings.ToLower(matchEpic)) {
				pillarScore += s.Scoring.EpicMatch
			}
		}

		// Keyword matching (against title + body)
		for _, kw := range pillar.Match.Keywords {
			if strings.Contains(searchText, strings.ToLower(kw)) {
				pillarScore += s.Scoring.KeywordMatch
			}
		}

		// Apply weight and threshold
		if pillarScore >= s.Scoring.AlignmentThreshold {
			totalScore += pillarScore * pillar.Weight
		}
	}

	return totalScore
}
