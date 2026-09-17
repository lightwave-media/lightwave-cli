package knowledge

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// RuntimeToken reads the shared runtime store; environment is only a local
// fallback. It never copies connector OAuth credentials or logs token values.
const credentialTimeout = 10 * time.Second

func RuntimeToken(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, credentialTimeout)
	defer cancel()

	config, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion("us-east-1"))
	if err == nil {
		var output *ssm.GetParameterOutput

		output, err = ssm.NewFromConfig(config).GetParameter(ctx, &ssm.GetParameterInput{
			Name: aws.String("/lightwave/prod/NOTION_API_KEY"), WithDecryption: aws.Bool(true),
		})
		if err == nil && output.Parameter != nil && output.Parameter.Value != nil && *output.Parameter.Value != "" {
			return *output.Parameter.Value, nil
		}
	}

	var missing *types.ParameterNotFound
	if err == nil || errors.As(err, &missing) {
		if token := os.Getenv("NOTION_API_KEY"); token != "" {
			return token, nil
		}

		return "", errors.New("notion runtime credential missing: provision /lightwave/prod/NOTION_API_KEY or an explicit local NOTION_API_KEY; interactive connector access does not configure the daemon")
	}

	return "", errors.New("cannot access the Notion credential in SSM; verify the configured AWS identity and permission")
}
