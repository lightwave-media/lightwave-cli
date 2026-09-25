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
	stepSkipped           = "skipped"
	stepFailed            = "failed"
	stepWaiting           = "waiting_approval"
	// Instance prints hold step output and the caller's --var values, so they
	// are the operator's alone (lightwave-cli#546).
	dirPerm  = 0o700
	filePerm = 0o600
)

// StepState is one row on the instance print.
type StepState struct {
	ID          string            `yaml:"id"`
	Kind        string            `yaml:"kind"`
	Status      string            `yaml:"status"`
	SignoffTier string            `yaml:"signoff_tier,omitempty"`
	Output      string            `yaml:"output,omitempty"`
	Outputs     map[string]string `yaml:"outputs,omitempty"`
	Warned      bool              `yaml:"warned,omitempty"`
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
	// Vars are the --var values the instance was started with. Inputs are
	// bound from them and the pinned edition's defaults on every apply.
	Vars map[string]string `yaml:"vars,omitempty"`
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

// ResolveInstanceID uses the explicit id, or the most recently created print
// for task. Instance ids are random UUIDs, so sorting them — which this did —
// picked an arbitrary instance, not the latest one.
func ResolveInstanceID(cwd, taskID, instanceID string) (string, error) {
	if instanceID != "" {
		return instanceID, nil
	}

	all, err := ListInstances(cwd, taskID)
	if err != nil {
		return "", err
	}

	if len(all) == 0 {
		return "", fmt.Errorf("no runbook instance for task %s", taskID)
	}

	return all[len(all)-1].InstanceID, nil
}

// ListInstances returns task's instance prints, oldest first.
func ListInstances(cwd, taskID string) ([]*Instance, error) {
	root := filepath.Join(cwd, ".tasks", taskID, "runbooks")

	ents, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("no runbook instance for task %s: %w", taskID, err)
	}

	var all []*Instance

	for _, e := range ents {
		if !e.IsDir() {
			continue
		}

		inst, err := Load(cwd, taskID, e.Name())
		if err != nil {
			return nil, err
		}

		all = append(all, inst)
	}

	sort.SliceStable(all, func(i, j int) bool {
		if all[i].CreatedAt != all[j].CreatedAt {
			return all[i].CreatedAt < all[j].CreatedAt
		}

		return all[i].UpdatedAt < all[j].UpdatedAt
	})

	return all, nil
}

// Finished reports whether the instance can no longer run.
func (inst *Instance) Finished() bool {
	return inst.Status == StatusCompleted || inst.Status == StatusFailed || inst.Status == StatusCancelled
}

// stampLayout is RFC 3339 with fixed-width nanoseconds, so timestamps sort as
// strings and two instances started in the same second still order by time.
// Whole seconds left the order to the UUID tie-break — an arbitrary pick.
const stampLayout = "2006-01-02T15:04:05.000000000Z07:00"

func nowUTC() string {
	return time.Now().UTC().Format(stampLayout)
}
