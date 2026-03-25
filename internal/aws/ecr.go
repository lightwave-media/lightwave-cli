package aws

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
)

const (
	DefaultRegistry = "738605694078.dkr.ecr.us-east-1.amazonaws.com"
)

// ECRClient wraps the AWS ECR client
type ECRClient struct {
	client   *ecr.Client
	registry string
}

// NewECRClient creates a new ECR client
func NewECRClient(ctx context.Context) (*ECRClient, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return &ECRClient{
		client:   ecr.NewFromConfig(cfg),
		registry: DefaultRegistry,
	}, nil
}

// DockerLogin authenticates Docker to ECR
func (e *ECRClient) DockerLogin(ctx context.Context) error {
	output, err := e.client.GetAuthorizationToken(ctx, &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		return fmt.Errorf("failed to get ECR auth token: %w", err)
	}

	if len(output.AuthorizationData) == 0 {
		return fmt.Errorf("no authorization data returned from ECR")
	}

	authData := output.AuthorizationData[0]
	decoded, err := base64.StdEncoding.DecodeString(*authData.AuthorizationToken)
	if err != nil {
		return fmt.Errorf("failed to decode auth token: %w", err)
	}

	// Token is "AWS:<password>"
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("unexpected auth token format")
	}

	cmd := exec.CommandContext(ctx, "docker", "login",
		"--username", parts[0],
		"--password-stdin",
		e.registry,
	)
	cmd.Stdin = strings.NewReader(parts[1])
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker login failed: %w", err)
	}

	return nil
}

// BuildImage builds a Docker image
func (e *ECRClient) BuildImage(ctx context.Context, imageURI, dockerfile, contextDir string) error {
	cmd := exec.CommandContext(ctx, "docker", "build",
		"-t", imageURI,
		"-f", dockerfile,
		contextDir,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build failed: %w", err)
	}

	return nil
}

// PushImage pushes a Docker image to ECR
func (e *ECRClient) PushImage(ctx context.Context, imageURI string) error {
	cmd := exec.CommandContext(ctx, "docker", "push", imageURI)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker push failed: %w", err)
	}

	return nil
}

// ImageURI returns the full ECR image URI for a repository and tag
func (e *ECRClient) ImageURI(repository, tag string) string {
	return fmt.Sprintf("%s/%s:%s", e.registry, repository, tag)
}
