package secrets

// rotator.go — the AWS side of lw secret rotate. Metadata reads run as
// lightwave-agent. The write runs as the rotator role: agent personas assume
// it in-process from lightwave-agent with their persona as session name, and
// an operator uses a profile whose role_session_name is op_*. Either way the
// identity checked is the one STS reports, never one the caller names.
//
// Endpoints are pinned to AWS. An endpoint_url in the profile, or
// AWS_ENDPOINT_URL[_SSM|_STS] in the environment, would otherwise send the
// PutParameter body, value included, somewhere else.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

const (
	assumedRoleSeconds = 900
	ledgerFileMode     = 0o600
)

var (
	personaName = regexp.MustCompile(`^v_[a-z0-9][a-z0-9_-]*$`)
	launchLabel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	probeStatus = regexp.MustCompile(`^([0-9]{3}|ok)$`)
)

type describeAPI interface {
	DescribeParameters(ctx context.Context, in *ssm.DescribeParametersInput, opts ...func(*ssm.Options)) (*ssm.DescribeParametersOutput, error)
}

type putAPI interface {
	PutParameter(ctx context.Context, in *ssm.PutParameterInput, opts ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
}

type ssmMeta struct{ api describeAPI }

type rotatorWriter struct {
	api    putAPI
	caller Caller
}

// NewMetaReader reads parameter metadata as lightwave-agent, whatever
// AWS_PROFILE says: reads never run as the rotator.
func NewMetaReader(ctx context.Context) (MetaReader, error) {
	cfg, err := loadProfile(ctx, AgentProfile)
	if err != nil {
		return nil, err
	}

	return ssmMeta{api: newSSM(&cfg)}, nil
}

// NewOperatorWriter writes through an operator profile. STS names the
// identity, which CheckCaller then holds to the record.
func NewOperatorWriter(ctx context.Context, profile string) (ValueWriter, error) {
	cfg, err := loadProfile(ctx, profile)
	if err != nil {
		return nil, err
	}

	who, err := newSTS(&cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("identify profile %s: %w", profile, awsError(err))
	}

	caller, err := callerFromARN(aws.ToString(who.Arn))
	if err != nil {
		return nil, err
	}

	return rotatorWriter{api: newSSM(&cfg), caller: caller}, nil
}

// NewPersonaWriter assumes role from lightwave-agent as persona. The role's
// trust policy admits lightwave-agent only under a v_* session name.
func NewPersonaWriter(ctx context.Context, persona, role string) (ValueWriter, error) {
	if !personaName.MatchString(persona) {
		return nil, fmt.Errorf("LW_PERSONA %q is not a v_* persona (an operator passes --profile): %w", persona, ErrRefused)
	}

	cfg, err := loadProfile(ctx, AgentProfile)
	if err != nil {
		return nil, err
	}

	stsClient := newSTS(&cfg)

	who, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("identify %s: %w", AgentProfile, awsError(err))
	}

	out, err := stsClient.AssumeRole(ctx, &sts.AssumeRoleInput{
		RoleArn:         aws.String("arn:aws:iam::" + aws.ToString(who.Account) + ":role/" + role),
		RoleSessionName: aws.String(persona),
		DurationSeconds: aws.Int32(assumedRoleSeconds),
	})
	if err != nil {
		return nil, fmt.Errorf("assume %s as %s: %w", role, persona, awsError(err))
	}

	caller, err := callerFromARN(aws.ToString(out.AssumedRoleUser.Arn))
	if err != nil {
		return nil, err
	}

	creds := aws.Credentials{
		AccessKeyID:     aws.ToString(out.Credentials.AccessKeyId),
		SecretAccessKey: aws.ToString(out.Credentials.SecretAccessKey),
		SessionToken:    aws.ToString(out.Credentials.SessionToken),
		Source:          "lw secret rotate",
		CanExpire:       true,
		Expires:         aws.ToTime(out.Credentials.Expiration),
	}
	cfg.Credentials = aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) { return creds, nil })

	return rotatorWriter{api: newSSM(&cfg), caller: caller}, nil
}

func loadProfile(ctx context.Context, profile string) (aws.Config, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(Region),
		awsconfig.WithSharedConfigProfile(profile),
		awsconfig.WithClientLogMode(0),
	)
	if err != nil {
		return aws.Config{}, fmt.Errorf("load AWS profile %s: %w", profile, err)
	}

	return cfg, nil
}

func newSSM(cfg *aws.Config) *ssm.Client {
	return ssm.NewFromConfig(*cfg, func(o *ssm.Options) { o.BaseEndpoint = nil })
}

func newSTS(cfg *aws.Config) *sts.Client {
	return sts.NewFromConfig(*cfg, func(o *sts.Options) { o.BaseEndpoint = nil })
}

// callerFromARN reads arn:aws:sts::<account>:assumed-role/<role>/<session>.
// Anything else (an IAM user, a root session) is refused: writes run only
// through an assumed role.
func callerFromARN(arn string) (Caller, error) {
	_, rest, isRole := strings.Cut(arn, ":assumed-role/")
	role, session, hasSession := strings.Cut(rest, "/")

	if !isRole || !hasSession || role == "" || session == "" {
		return Caller{}, fmt.Errorf("%s is not an assumed-role session; writes run only as the rotator role: %w", arn, ErrRefused)
	}

	return Caller{Role: role, Session: session}, nil
}

// Meta pages DescribeParameters to the end: a filtered page can come back
// empty and still carry a NextToken.
func (s ssmMeta) Meta(ctx context.Context, path string) (ParamMeta, error) {
	in := &ssm.DescribeParametersInput{
		ParameterFilters: []types.ParameterStringFilter{
			{Key: aws.String("Name"), Option: aws.String("Equals"), Values: []string{path}},
		},
	}

	for {
		out, err := s.api.DescribeParameters(ctx, in)
		if err != nil {
			return ParamMeta{}, awsError(err)
		}

		if len(out.Parameters) > 0 {
			p := out.Parameters[0]

			return ParamMeta{Type: string(p.Type), KeyID: aws.ToString(p.KeyId), Tier: string(p.Tier), Version: p.Version}, nil
		}

		if out.NextToken == nil {
			return ParamMeta{}, fmt.Errorf("%s does not exist", path)
		}

		in.NextToken = out.NextToken
	}
}

func (w rotatorWriter) Caller() Caller { return w.caller }

// Put writes value as a Standard SecureString under the default key, and
// returns the Version SSM assigned. The request holds an immutable copy of
// the value that cannot be cleared; the caller clears its own.
func (w rotatorWriter) Put(ctx context.Context, path string, value []byte) (int64, error) {
	out, err := w.api.PutParameter(ctx, &ssm.PutParameterInput{
		Name:      aws.String(path),
		Value:     aws.String(string(value)),
		Type:      types.ParameterTypeSecureString,
		Tier:      types.ParameterTierStandard,
		Overwrite: aws.Bool(true),
	})
	if err != nil {
		return 0, awsError(err)
	}

	return out.Version, nil
}

// awsError keeps an API error's code and drops its message, which can quote
// the request.
func awsError(err error) error {
	var api interface{ ErrorCode() string }
	if errors.As(err, &api) {
		return errors.New(api.ErrorCode())
	}

	return err
}

// Kickstart restarts a launchd job so it re-reads its keys at launch. Its
// stderr is not relayed.
func Kickstart(ctx context.Context, label string) error {
	if !launchLabel.MatchString(label) {
		return errors.New("not a launchd label")
	}

	target := "gui/" + strconv.Itoa(os.Getuid()) + "/" + label
	if err := exec.CommandContext(ctx, "launchctl", "kickstart", "-k", target).Run(); err != nil {
		return exitStatus(err)
	}

	return nil
}

// ProbeViaConfigExec runs a probe under `lw config exec --only keys`, so the
// probe reads the new value from SSM itself. config exec passes its own
// environment on, so the child gets only PATH, HOME and the agent profile:
// whatever else this process holds stays here. Only a status-shaped line of
// output is returned; anything else the probe prints is dropped.
func ProbeViaConfigExec(lw string) func(context.Context, []string, string) (string, error) {
	return func(ctx context.Context, keys []string, probe string) (string, error) {
		cmd := exec.CommandContext(ctx, lw, "config", "exec", "--only", strings.Join(keys, ","), "--", "/bin/sh", "-c", probe)
		cmd.Env = probeEnv(os.Getenv)
		cmd.Stderr = io.Discard

		out, err := cmd.Output()
		status := strings.TrimSpace(string(out))

		if !probeStatus.MatchString(status) {
			status = ""
		}

		if err != nil {
			return status, exitStatus(err)
		}

		return status, nil
	}
}

func probeEnv(getenv func(string) string) []string {
	env := []string{"AWS_PROFILE=" + AgentProfile}

	for _, key := range []string{"PATH", "HOME", "AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE"} {
		if v := getenv(key); v != "" {
			env = append(env, key+"="+v)
		}
	}

	return env
}

func exitStatus(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return fmt.Errorf("exit %d", exitErr.ExitCode())
	}

	return err
}

// AppendLedger appends one row to the NAME-only ledger at path.
func AppendLedger(path string) func(LedgerRow) error {
	return func(row LedgerRow) error {
		line, err := json.Marshal(row)
		if err != nil {
			return fmt.Errorf("encode ledger row: %w", err)
		}

		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, ledgerFileMode)
		if err != nil {
			return fmt.Errorf("open ledger: %w", err)
		}

		if _, err := f.Write(append(line, '\n')); err != nil {
			_ = f.Close()

			return fmt.Errorf("append ledger: %w", err)
		}

		return f.Close()
	}
}

// DefaultLedgerPath is the channel secret_rotation.exposure_count reads.
func DefaultLedgerPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home directory: %w", err)
	}

	return filepath.Join(home, ".lightwave", "observability", "secrets-exposure.jsonl"), nil
}
