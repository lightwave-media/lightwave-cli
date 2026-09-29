package secrets

// secretmap.go — the secret map (lightwave-core#819): key records per
// policy/security/secret_rotation 1.0.0 and their consumers per
// policy/security/daemon_secrets 1.1.0, read from the instances of record under
// ~/.lightwave/specs/security (CORE-0048). The map holds names and policy,
// never values.
//
// Decoding is strict. A misspelt policy field (`peers-literal:`, `mirror:`)
// would otherwise decode to empty and let a refusal fail open, so an unknown
// field fails the load and names itself.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Record is one key record (secret_rotation).
type Record struct {
	Generate             *GenerateSpec `yaml:"generate"`
	RotationIntervalDays *int          `yaml:"rotation_interval_days"`
	ExposureCount        *int          `yaml:"exposure_count"`
	// Vendor is decoded loosely: no verb in this slice rotates a vendor key.
	Vendor         any      `yaml:"vendor"`
	Name           string   `yaml:"name"`
	SSMPath        string   `yaml:"ssm_path"`
	Status         string   `yaml:"status"`
	RotationMode   string   `yaml:"rotation_mode"`
	WriterIdentity string   `yaml:"writer_identity"`
	OwnerPersona   string   `yaml:"owner_persona"`
	Store          string   `yaml:"store"`
	LocalPath      string   `yaml:"local_path"`
	Notes          string   `yaml:"notes"`
	Aliases        []string `yaml:"aliases"`
	Rotators       []string `yaml:"rotators"`
	Mirrors        []string `yaml:"mirrors"`
	PeersLiteral   []string `yaml:"peers_literal"`
}

// GenerateSpec is the policy for a generated successor value.
type GenerateSpec struct {
	Charset       string `yaml:"charset"`
	Algorithm     string `yaml:"algorithm"`
	PublicSSMPath string `yaml:"public_ssm_path"`
	Length        int    `yaml:"length"`
}

// Daemon is one consumer (daemon_secrets). Launch fields are decoded loosely:
// rotation reads only the loadings, the refresh action and the probe.
type Daemon struct {
	Refresh              *RefreshSpec    `yaml:"refresh"`
	Verify               *VerifySpec     `yaml:"verify"`
	LaunchInvariants     any             `yaml:"launch_invariants"`
	LaunchSequence       any             `yaml:"launch_sequence"`
	RequiredEnvAfterLoad any             `yaml:"required_env_after_load"`
	SupportedInitSystems any             `yaml:"supported_init_systems"`
	FailoverBehavior     any             `yaml:"failover_behavior"`
	AWSCredentialSource  any             `yaml:"aws_credential_source"`
	ID                   string          `yaml:"id"`
	DaemonID             string          `yaml:"daemon_id"`
	Owner                string          `yaml:"owner"`
	SecretLoadings       []SecretLoading `yaml:"secret_loadings"`
}

// SecretLoading is one key a daemon loads at start.
type SecretLoading struct {
	SSMPath      string `yaml:"ssm_path"`
	TargetEnvVar string `yaml:"target_env_var"`
	SecretType   string `yaml:"secret_type"`
	AWSRegion    string `yaml:"aws_region"`
	Notes        string `yaml:"notes"`
	SoftFail     bool   `yaml:"soft_fail"`
}

// RefreshSpec is how a consumer is made to re-read its keys.
type RefreshSpec struct {
	Action  string `yaml:"action"`
	Target  string `yaml:"target"`
	AuthEnv string `yaml:"auth_env"`
}

// VerifySpec is the probe that shows a consumer works with the new value.
type VerifySpec struct {
	Probe string `yaml:"probe"`
}

// Map is the whole secret map.
type Map struct {
	Records []Record
	Daemons []Daemon
}

var keyName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// DefaultMapDir is where the instances of record live.
func DefaultMapDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home directory: %w", err)
	}

	return filepath.Join(home, ".lightwave", "specs", "security"), nil
}

// LoadMap reads dir/secret_rotation.yaml and dir/daemon_secrets/*.yaml, and
// refuses a record whose name or ssm_path breaks the schema's pattern.
func LoadMap(dir string) (*Map, error) {
	var keys struct {
		Records []Record `yaml:"records"`
	}

	if err := readStrict(filepath.Join(dir, "secret_rotation.yaml"), &keys); err != nil {
		return nil, err
	}

	for i := range keys.Records {
		rec := &keys.Records[i]
		if !keyName.MatchString(rec.Name) || rec.SSMPath != Path+rec.Name {
			return nil, fmt.Errorf("secret map: record %q: name must be UPPER_SNAKE and ssm_path %s<name>", rec.Name, Path)
		}
	}

	files, err := filepath.Glob(filepath.Join(dir, "daemon_secrets", "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("list daemon_secrets: %w", err)
	}

	m := &Map{Records: keys.Records, Daemons: make([]Daemon, len(files))}

	for i, file := range files {
		if err := readStrict(file, &m.Daemons[i]); err != nil {
			return nil, err
		}
	}

	return m, nil
}

// quoted matches the backtick-quoted scalar a yaml.v3 type error echoes. The
// map holds no values, but a value pasted into it by mistake must not reach
// stderr through the error that rejects it.
var quoted = regexp.MustCompile("`[^`]*`")

func readStrict(path string, into any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read secret map: %w", err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)

	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("parse %s: %s", path, quoted.ReplaceAllString(err.Error(), "`…`"))
	}

	return nil
}

// Lookup returns the record named name (bare, or as its full SSM path). An
// alias is refused: it names another record's value.
func (m *Map) Lookup(name string) (*Record, error) {
	key := strings.TrimPrefix(name, Path)

	for i := range m.Records {
		rec := &m.Records[i]
		if rec.Name == key {
			return rec, nil
		}

		if slices.Contains(rec.Aliases, Path+key) {
			return nil, refuse(key, "an alias of "+rec.Name+"; name the record")
		}
	}

	return nil, refuse(key, "not in the secret map (~/.lightwave/specs/security/secret_rotation.yaml)")
}

// Consumers derives rec's consumers: every daemon that loads its ssm_path or
// one of its aliases, each once. Consumers are never hand-listed on a record.
func (m *Map) Consumers(rec *Record) []*Daemon {
	var out []*Daemon

	for i := range m.Daemons {
		if loadsKey(&m.Daemons[i], rec) {
			out = append(out, &m.Daemons[i])
		}
	}

	return out
}

func loadsKey(d *Daemon, rec *Record) bool {
	return slices.ContainsFunc(d.SecretLoadings, func(l SecretLoading) bool {
		return l.SSMPath == rec.SSMPath || slices.Contains(rec.Aliases, l.SSMPath)
	})
}

// ErrRefused marks a policy refusal. Nothing was written.
var ErrRefused = errors.New("refused")

func refuse(name, why string) error {
	return fmt.Errorf("%s: %w: %s", name, ErrRefused, why)
}
