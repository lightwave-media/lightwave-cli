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
	// Root is where this print lives: the working tree for a runbook that can
	// change files, CheckOnlyRoot for one that cannot. Set by Save and Load.
	Root string `yaml:"-"`
}

// CheckOnlyRoot holds the prints of runbooks with no Command or Template
// step. They change nothing, so they may run anywhere (#553) — session-signoff
// runs after its PR merged and its worktree is gone — and their prints belong
// to the operator, not to whichever directory they ran in.
func CheckOnlyRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	return filepath.Join(home, ".lightwave", "runbooks")
}

// Dir is .tasks/{task}/runbooks/{instance} under root.
func Dir(root, taskID, instanceID string) string {
	return filepath.Join(root, ".tasks", taskID, "runbooks", instanceID)
}

// Path is the instance.yaml print.
func Path(root, taskID, instanceID string) string {
	return filepath.Join(Dir(root, taskID, instanceID), "instance.yaml")
}

// Save writes the instance print under root.
func Save(root string, inst *Instance) error {
	dir := Dir(root, inst.TaskID, inst.InstanceID)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}

	inst.UpdatedAt = nowUTC()
	inst.Root = root

	raw, err := yaml.Marshal(inst)
	if err != nil {
		return err
	}

	return os.WriteFile(Path(root, inst.TaskID, inst.InstanceID), raw, filePerm)
}

// Load reads one instance print from root.
func Load(root, taskID, instanceID string) (*Instance, error) {
	raw, err := os.ReadFile(Path(root, taskID, instanceID))
	if err != nil {
		return nil, err
	}

	var inst Instance
	if err := yaml.Unmarshal(raw, &inst); err != nil {
		return nil, err
	}

	inst.Root = root

	return &inst, nil
}

// Finished reports whether the instance can no longer run.
func (inst *Instance) Finished() bool {
	return inst.Status == StatusCompleted || inst.Status == StatusFailed || inst.Status == StatusCancelled
}

func loadFrom(root, taskID, instanceID string) (*Instance, error) {
	id, err := ResolveInstanceID(root, taskID, instanceID)
	if err != nil {
		return nil, err
	}

	return Load(root, taskID, id)
}

// ResolveInstanceID uses the explicit id, or the most recently created print
// for task. Instance ids are random UUIDs, so sorting them — which this did —
// picked an arbitrary instance, not the latest one.
func ResolveInstanceID(root, taskID, instanceID string) (string, error) {
	if instanceID != "" {
		return instanceID, nil
	}

	all, err := ListInstances(root, taskID)
	if err != nil {
		return "", err
	}

	if len(all) == 0 {
		return "", fmt.Errorf("no runbook instance for task %s", taskID)
	}

	return all[len(all)-1].InstanceID, nil
}

// ListInstances returns task's instance prints under root, oldest first.
func ListInstances(root, taskID string) ([]*Instance, error) {
	ents, err := os.ReadDir(filepath.Join(root, ".tasks", taskID, "runbooks"))
	if err != nil {
		return nil, fmt.Errorf("no runbook instance for task %s: %w", taskID, err)
	}

	var all []*Instance

	for _, e := range ents {
		if !e.IsDir() {
			continue
		}

		inst, err := Load(root, taskID, e.Name())
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

// stampLayout is RFC 3339 with fixed-width nanoseconds, so timestamps sort as
// strings and two instances started in the same second still order by time.
// Whole seconds left the order to the UUID tie-break — an arbitrary pick.
const stampLayout = "2006-01-02T15:04:05.000000000Z07:00"

func nowUTC() string {
	return time.Now().UTC().Format(stampLayout)
}
