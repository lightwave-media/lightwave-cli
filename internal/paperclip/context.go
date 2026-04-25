package paperclip

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ContextFile mirrors ~/.paperclip/context.json written by the paperclipai CLI.
type ContextFile struct {
	Version        int                       `json:"version"`
	CurrentProfile string                    `json:"currentProfile"`
	Profiles       map[string]ContextProfile `json:"profiles"`
}

// ContextProfile holds the company binding for a paperclipai profile.
type ContextProfile struct {
	CompanyID string `json:"companyId,omitempty"`
}

// LoadContext reads ~/.paperclip/context.json. Returns the profile name in use
// and an empty file if the path does not exist.
func LoadContext() (*ContextFile, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home dir: %w", err)
	}
	path := filepath.Join(home, ".paperclip", "context.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ContextFile{Profiles: map[string]ContextProfile{}}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var ctx ContextFile
	if err := json.Unmarshal(data, &ctx); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &ctx, nil
}

// ResolveCompanyID returns the companyId from the named profile, or the current
// profile if profileName is empty. Returns "" if not bound.
func (cf *ContextFile) ResolveCompanyID(profileName string) string {
	if profileName == "" {
		profileName = cf.CurrentProfile
	}
	if profileName == "" {
		return ""
	}
	p, ok := cf.Profiles[profileName]
	if !ok {
		return ""
	}
	return p.CompanyID
}
