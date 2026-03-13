package aws

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// ECSClient wraps the AWS ECS client
type ECSClient struct {
	client  *ecs.Client
	cluster string
}

// ServiceStatus represents ECS service status
type ServiceStatus struct {
	Name           string
	Status         string
	DesiredCount   int32
	RunningCount   int32
	PendingCount   int32
	TaskDefinition string
	LastDeployment string
	Healthy        bool
}

// NewECSClient creates a new ECS client
func NewECSClient(ctx context.Context, cluster string) (*ECSClient, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return &ECSClient{
		client:  ecs.NewFromConfig(cfg),
		cluster: cluster,
	}, nil
}

// ListServices returns all services in the cluster
func (e *ECSClient) ListServices(ctx context.Context) ([]string, error) {
	var services []string
	paginator := ecs.NewListServicesPaginator(e.client, &ecs.ListServicesInput{
		Cluster: &e.cluster,
	})

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list services: %w", err)
		}
		services = append(services, output.ServiceArns...)
	}

	return services, nil
}

// GetServiceStatus returns the status of a service
func (e *ECSClient) GetServiceStatus(ctx context.Context, serviceName string) (*ServiceStatus, error) {
	output, err := e.client.DescribeServices(ctx, &ecs.DescribeServicesInput{
		Cluster:  &e.cluster,
		Services: []string{serviceName},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to describe service: %w", err)
	}

	if len(output.Services) == 0 {
		return nil, fmt.Errorf("service %s not found", serviceName)
	}

	svc := output.Services[0]
	status := &ServiceStatus{
		Name:           *svc.ServiceName,
		Status:         *svc.Status,
		DesiredCount:   svc.DesiredCount,
		RunningCount:   svc.RunningCount,
		PendingCount:   svc.PendingCount,
		TaskDefinition: extractTaskDefName(*svc.TaskDefinition),
		Healthy:        svc.RunningCount >= svc.DesiredCount && svc.DesiredCount > 0,
	}

	// Get last deployment info
	for _, d := range svc.Deployments {
		if d.Status != nil && *d.Status == "PRIMARY" {
			if d.UpdatedAt != nil {
				status.LastDeployment = d.UpdatedAt.Format("2006-01-02 15:04:05")
			}
			break
		}
	}

	return status, nil
}

// UpdateService forces a new deployment
func (e *ECSClient) UpdateService(ctx context.Context, serviceName string, forceNewDeployment bool) error {
	_, err := e.client.UpdateService(ctx, &ecs.UpdateServiceInput{
		Cluster:            &e.cluster,
		Service:            &serviceName,
		ForceNewDeployment: forceNewDeployment,
	})
	if err != nil {
		return fmt.Errorf("failed to update service: %w", err)
	}
	return nil
}

// WaitForStableService waits for a service to become stable
func (e *ECSClient) WaitForStableService(ctx context.Context, serviceName string) error {
	waiter := ecs.NewServicesStableWaiter(e.client)
	return waiter.Wait(ctx, &ecs.DescribeServicesInput{
		Cluster:  &e.cluster,
		Services: []string{serviceName},
	}, 600) // 10 minute timeout
}

// GetRunningTasks returns running tasks for a service
func (e *ECSClient) GetRunningTasks(ctx context.Context, serviceName string) ([]types.Task, error) {
	// List task ARNs
	listOutput, err := e.client.ListTasks(ctx, &ecs.ListTasksInput{
		Cluster:     &e.cluster,
		ServiceName: &serviceName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	if len(listOutput.TaskArns) == 0 {
		return nil, nil
	}

	// Describe tasks
	descOutput, err := e.client.DescribeTasks(ctx, &ecs.DescribeTasksInput{
		Cluster: &e.cluster,
		Tasks:   listOutput.TaskArns,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to describe tasks: %w", err)
	}

	return descOutput.Tasks, nil
}

func extractTaskDefName(arn string) string {
	parts := strings.Split(arn, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return arn
}
