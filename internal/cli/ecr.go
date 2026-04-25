package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/aws"
	"github.com/spf13/cobra"
)

// Service configs: maps service name to Dockerfile and build context
var serviceConfigs = map[string]struct {
	repository string
	dockerfile string
	context    string
}{
	"backend": {
		repository: "platform",
		dockerfile: "lightwave-platform/src/Dockerfile",
		context:    "lightwave-platform/src/",
	},
	"frontend": {
		repository: "platform-frontend",
		dockerfile: "lightwave-platform/src/frontend/Dockerfile.frontend",
		context:    "lightwave-platform/src/frontend/",
	},
}

var ecrTag string

var ecrCmd = &cobra.Command{
	Use:   "ecr",
	Short: "ECR container registry operations",
}

var ecrPushCmd = &cobra.Command{
	Use:   "push <service>",
	Short: "Build and push image to ECR",
	Long: `Build a Docker image and push it to ECR.

This is the emergency deploy path when GitHub Actions is unavailable.
Requires Docker running locally and AWS credentials configured.

Services:
  backend   — Django platform (lightwave-platform/src/Dockerfile)
  frontend  — TanStack frontend (lightwave-platform/src/Dockerfile.frontend)

Examples:
  lw aws ecr push backend
  lw aws ecr push backend --tag $(git rev-parse --short HEAD)
  lw aws ecr push backend --tag v1.2.3`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		serviceName := args[0]

		svc, ok := serviceConfigs[serviceName]
		if !ok {
			return fmt.Errorf("unknown service %q — available: backend, frontend", serviceName)
		}

		// Resolve paths relative to workspace root
		workspaceRoot, err := findWorkspaceRoot()
		if err != nil {
			return err
		}

		dockerfile := filepath.Join(workspaceRoot, svc.dockerfile)
		contextDir := filepath.Join(workspaceRoot, svc.context)

		// Verify Dockerfile exists
		if _, err := os.Stat(dockerfile); os.IsNotExist(err) {
			return fmt.Errorf("Dockerfile not found: %s", dockerfile)
		}

		client, err := aws.NewECRClient(ctx)
		if err != nil {
			return err
		}

		imageURI := client.ImageURI(svc.repository, ecrTag)

		// Step 1: Docker login to ECR
		fmt.Printf("%s Authenticating to ECR...\n", color.CyanString("→"))
		if err := client.DockerLogin(ctx); err != nil {
			return err
		}
		fmt.Println(color.GreenString("✓ Authenticated"))

		// Step 2: Build
		fmt.Printf("%s Building %s...\n", color.CyanString("→"), imageURI)
		if err := client.BuildImage(ctx, imageURI, dockerfile, contextDir); err != nil {
			return err
		}
		fmt.Println(color.GreenString("✓ Built"))

		// Step 3: Push
		fmt.Printf("%s Pushing %s...\n", color.CyanString("→"), imageURI)
		if err := client.PushImage(ctx, imageURI); err != nil {
			return err
		}
		fmt.Println(color.GreenString("✓ Pushed"))

		fmt.Printf("\n%s %s\n", color.CyanString("Image:"), imageURI)
		fmt.Printf("\nNext: %s\n", color.YellowString("lw aws ecs deploy django --wait"))

		return nil
	},
}

func findWorkspaceRoot() (string, error) {
	// Walk up from cwd looking for the lightwave-platform directory
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get working directory: %w", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "lightwave-platform")); err == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("could not find workspace root (directory containing lightwave-platform)")
}

func init() {
	ecrPushCmd.Flags().StringVar(&ecrTag, "tag", "latest", "Image tag")

	ecrCmd.AddCommand(ecrPushCmd)
}
