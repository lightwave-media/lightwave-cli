package cron

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

// Record is the part of a job's daemon_secrets consumer record sync reads:
// which secret names the job is entitled to. It is built from
// internal/secrets' strict load of ~/.lightwave/specs/security, so a record
// that fails that load never reaches here.
type Record struct {
	ID string
	// Names are the env names the record loads (target_env_var, else the
	// basename of ssm_path).
	Names []string
}

var secretName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// secretShaped matches a credential that has been pasted into a stamp field.
// Names only, prefixes and high-entropy runs only: sync never compares against
// a secret's value, because nothing here reads values (CLAUDE.md §24).
var secretShaped = regexp.MustCompile(
	`(sk-[A-Za-z0-9_-]{16,}|sk_live_|ghp_[A-Za-z0-9]{20,}|gho_|github_pat_|AKIA[0-9A-Z]{16}|xox[abp]-|-----BEGIN [A-Z ]*PRIVATE KEY|\b[0-9a-f]{32,}\b|[A-Za-z0-9+/_-]{40,}={0,2})`)

// Refusal says why sync will not render a job, or "" when it may.
//
// Each rule is a flat guard naming what it enforces, in the order an operator
// fixes them: ownership, entitlement, then content.
func Refusal(job *Job, records map[string]Record) string {
	if job.Persona == "" {
		return "no persona: cron_job 1.1.0 needs the v_* persona that owns the job"
	}

	if job.DaemonSecretsRef == "" {
		return "no daemon_secrets_ref: the job has no consumer record entitling its secrets"
	}

	record, ok := records[job.DaemonSecretsRef]
	if !ok {
		return fmt.Sprintf("daemon_secrets_ref %q has no consumer record under ~/.lightwave/specs/security/daemon_secrets", job.DaemonSecretsRef)
	}

	for _, name := range job.SecretNames {
		if !secretName.MatchString(name) {
			return "secret_names entry is not an UPPER_SNAKE key name (a value in a name field is refused, not shown)"
		}

		if !slices.Contains(record.Names, name) {
			return fmt.Sprintf("secret %s is not in record %s's secret_loadings", name, record.ID)
		}
	}

	switch job.Dispatch.Kind {
	case DispatchShell:
		if strings.TrimSpace(job.Dispatch.Target) == "" {
			return "shell_command dispatch has no target command"
		}
	case DispatchAgentSession:
		if job.Dispatch.Target != job.Persona {
			return fmt.Sprintf("agent_session target %q must equal persona %q", job.Dispatch.Target, job.Persona)
		}
	case "":
		return "no dispatch: cron_job 1.1.0 needs dispatch.kind"
	default:
		return fmt.Sprintf("dispatch kind %q is not one lw cron run executes (shell_command, agent_session)", job.Dispatch.Kind)
	}

	if _, err := Intervals(job.Schedule); err != nil {
		return err.Error()
	}

	if field := secretField(job); field != "" {
		return field + " holds a secret-shaped value; a stamp names secrets, never carries them"
	}

	return ""
}

// secretField names the first field holding a secret-shaped value.
func secretField(job *Job) string {
	fields := map[string]string{
		"dispatch.target":          job.Dispatch.Target,
		"dispatch.prompt_template": job.Dispatch.PromptTemplate,
		"schedule":                 job.Schedule,
		"persona":                  job.Persona,
	}
	for key, value := range job.Dispatch.Args {
		fields["dispatch.args."+key] = fmt.Sprint(value)
	}

	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	for _, k := range keys {
		if secretShaped.MatchString(fields[k]) {
			return k
		}
	}

	return ""
}

// RecordNames lists the env names a secret_loadings entry provides.
func RecordNames(targetEnvVar, ssmPath string) string {
	if targetEnvVar != "" {
		return targetEnvVar
	}

	return path.Base(ssmPath)
}
