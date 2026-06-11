package research

import (
	"context"
	"fmt"
	"os"

	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

const (
	// SSMKeyPath is the canonical home of the Perplexity API key (SecureString).
	// AWS is the source of truth per the secrets-in-AWS rule.
	SSMKeyPath = "/lightwave/prod/PERPLEXITY_API_KEY"
	// EnvKey is the dev/CI convenience override.
	EnvKey = "PERPLEXITY_API_KEY"
	// keyRegion is where the parameter lives.
	keyRegion = "us-east-1"
)

// ResolveAPIKey returns the Perplexity API key. An explicit PERPLEXITY_API_KEY
// env var wins (fast, intentional override for dev/CI); otherwise the key is
// read from AWS SSM Parameter Store (SecureString) — the source of truth.
func ResolveAPIKey(ctx context.Context) (string, error) {
	if v := os.Getenv(EnvKey); v != "" {
		return v, nil
	}

	cfg, err := awscfg.LoadDefaultConfig(ctx, awscfg.WithRegion(keyRegion))
	if err != nil {
		return "", fmt.Errorf("research: load AWS config (set %s to bypass SSM): %w", EnvKey, err)
	}

	out, err := ssm.NewFromConfig(cfg).GetParameter(ctx, &ssm.GetParameterInput{
		Name:           strPtr(SSMKeyPath),
		WithDecryption: boolPtr(true),
	})
	if err != nil {
		return "", fmt.Errorf("research: read %s from SSM (or set %s): %w", SSMKeyPath, EnvKey, err)
	}

	if out.Parameter == nil || out.Parameter.Value == nil || *out.Parameter.Value == "" {
		return "", fmt.Errorf("research: %s is empty in SSM", SSMKeyPath)
	}

	return *out.Parameter.Value, nil
}

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }
