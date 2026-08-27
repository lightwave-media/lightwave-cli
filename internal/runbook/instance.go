package runbook

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	StatusPending         = "pending"
	StatusRunning         = "running"
	StatusWaitingApproval = "waiting_approval"
	StatusCompleted       = "completed"
	StatusFailed          = "failed"
	StatusCancelled       = "cancelled"
	stepPending           = "pending"
	stepCompleted         = "completed"
	stepFailed            = "failed"
	stepWaiting           = "waiting_approval"
	dirPerm               = 0o755
	filePerm              = 0o644
)

// StepState is one row on the instance print.
type StepState struct {
	ID          string `yaml:"id"`
	Kind        string `yaml:"kind"`
	Status      string `yaml:"status"`
	SignoffTier string `yaml:"signoff_tier,omitempty"`
	Output      string `yaml:"output,omitempty"`
}

// Instance is the agent-owned print at
// .tasks/{task_id}/runbooks/{instance_id}/instance.yaml.
type Instance struct {
	WorktreePath  string      `yaml:"worktree_path"`
	EditionHash   string      `yaml:"edition_hash"`
	AgentID       string      `yaml:"agent_id"`
	TaskID        string      `yaml:"task_id"`
	SessionID     string      `yaml:"session_id,omitempty"`
	RepoSlug      string      `yaml:"repo_slug"`
	RunbookSlug   string      `yaml:"runbook_slug"`
	Status        string      `yaml:"status"`
	InstanceID    string      `yaml:"instance_id"`
	UpdatedAt     string      `yaml:"updated_at"`
	Branch        string      `yaml:"branch"`
	CurrentStepID string      `yaml:"current_step_id,omitempty"`
	CreatedAt     string      `yaml:"created_at"`
	Steps         []StepState `yaml:"steps"`
	DryRun        bool        `yaml:"dry_run"`
}

// Dir is .tasks/{task}/runbooks/{instance} under cwd.
func Dir(cwd, taskID, instanceID string) string {
	return filepath.Join(cwd, ".tasks", taskID, "runbooks", instanceID)
}

// Path is the instance.yaml print.
func Path(cwd, taskID, instanceID string) string {
	return filepath.Join(Dir(cwd, taskID, instanceID), "instance.yaml")
}

// Save writes the instance print.
func Save(cwd string, inst *Instance) error {
	dir := Dir(cwd, inst.TaskID, inst.InstanceID)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}

	inst.UpdatedAt = nowUTC()

	raw, err := yaml.Marshal(inst)
	if err != nil {
		return err
	}

	return os.WriteFile(Path(cwd, inst.TaskID, inst.InstanceID), raw, filePerm)
}

// Load reads one instance print.
func Load(cwd, taskID, instanceID string) (*Instance, error) {
	raw, err := os.ReadFile(Path(cwd, taskID, instanceID))
	if err != nil {
		return nil, err
	}

	var inst Instance
	if err := yaml.Unmarshal(raw, &inst); err != nil {
		return nil, err
	}

	return &inst, nil
}

// ResolveInstanceID uses the explicit id, or the only/latest print for task.
func ResolveInstanceID(cwd, taskID, instanceID string) (string, error) {
	if instanceID != "" {
		return instanceID, nil
	}

	root := filepath.Join(cwd, ".tasks", taskID, "runbooks")

	ents, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("no runbook instance for task %s: %w", taskID, err)
	}

	var ids []string

	for _, e := range ents {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}

	if len(ids) == 0 {
		return "", fmt.Errorf("no runbook instance for task %s", taskID)
	}

	sort.Strings(ids)

	return ids[len(ids)-1], nil
}

func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}
