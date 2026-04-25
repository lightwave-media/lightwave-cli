package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// TaskDefFile mirrors the JSON format of .aws/task-definition-*.json
type TaskDefFile struct {
	Family                  string               `json:"family"`
	TaskRoleArn             string               `json:"taskRoleArn"`
	ExecutionRoleArn        string               `json:"executionRoleArn"`
	NetworkMode             string               `json:"networkMode"`
	RequiresCompatibilities []string             `json:"requiresCompatibilities"`
	CPU                     string               `json:"cpu"`
	Memory                  string               `json:"memory"`
	RuntimePlatform         *RuntimePlatformFile `json:"runtimePlatform"`
	ContainerDefinitions    []ContainerDefFile   `json:"containerDefinitions"`
}

type RuntimePlatformFile struct {
	CPUArchitecture       string `json:"cpuArchitecture"`
	OperatingSystemFamily string `json:"operatingSystemFamily"`
}

type ContainerDefFile struct {
	Name             string           `json:"name"`
	Image            string           `json:"image"`
	CPU              int32            `json:"cpu"`
	Memory           int32            `json:"memory"`
	Essential        bool             `json:"essential"`
	PortMappings     []PortMapFile    `json:"portMappings"`
	Environment      []EnvVarFile     `json:"environment"`
	LogConfiguration *LogConfigFile   `json:"logConfiguration"`
	HealthCheck      *HealthCheckFile `json:"healthCheck"`
}

type PortMapFile struct {
	ContainerPort int32  `json:"containerPort"`
	HostPort      int32  `json:"hostPort"`
	Protocol      string `json:"protocol"`
}

type EnvVarFile struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type LogConfigFile struct {
	LogDriver string            `json:"logDriver"`
	Options   map[string]string `json:"options"`
}

type HealthCheckFile struct {
	Command     []string `json:"command"`
	Interval    int32    `json:"interval"`
	Timeout     int32    `json:"timeout"`
	Retries     int32    `json:"retries"`
	StartPeriod int32    `json:"startPeriod"`
}

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

// RegisterTaskDefinitionFromFile reads a task definition JSON file, registers it, and returns the ARN.
func (e *ECSClient) RegisterTaskDefinitionFromFile(ctx context.Context, filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read task definition file: %w", err)
	}

	var tdf TaskDefFile
	if err := json.Unmarshal(data, &tdf); err != nil {
		return "", fmt.Errorf("failed to parse task definition JSON: %w", err)
	}

	input := &ecs.RegisterTaskDefinitionInput{
		Family:      aws.String(tdf.Family),
		NetworkMode: types.NetworkMode(tdf.NetworkMode),
		Cpu:         aws.String(tdf.CPU),
		Memory:      aws.String(tdf.Memory),
	}

	if tdf.TaskRoleArn != "" {
		input.TaskRoleArn = aws.String(tdf.TaskRoleArn)
	}
	if tdf.ExecutionRoleArn != "" {
		input.ExecutionRoleArn = aws.String(tdf.ExecutionRoleArn)
	}

	for _, rc := range tdf.RequiresCompatibilities {
		input.RequiresCompatibilities = append(input.RequiresCompatibilities, types.Compatibility(rc))
	}

	if tdf.RuntimePlatform != nil {
		input.RuntimePlatform = &types.RuntimePlatform{
			CpuArchitecture:       types.CPUArchitecture(tdf.RuntimePlatform.CPUArchitecture),
			OperatingSystemFamily: types.OSFamily(tdf.RuntimePlatform.OperatingSystemFamily),
		}
	}

	for _, cd := range tdf.ContainerDefinitions {
		cdef := types.ContainerDefinition{
			Name:      aws.String(cd.Name),
			Image:     aws.String(cd.Image),
			Cpu:       cd.CPU,
			Memory:    aws.Int32(cd.Memory),
			Essential: aws.Bool(cd.Essential),
		}

		for _, pm := range cd.PortMappings {
			proto := types.TransportProtocol(pm.Protocol)
			cdef.PortMappings = append(cdef.PortMappings, types.PortMapping{
				ContainerPort: aws.Int32(pm.ContainerPort),
				HostPort:      aws.Int32(pm.HostPort),
				Protocol:      proto,
			})
		}

		for _, ev := range cd.Environment {
			cdef.Environment = append(cdef.Environment, types.KeyValuePair{
				Name:  aws.String(ev.Name),
				Value: aws.String(ev.Value),
			})
		}

		if cd.LogConfiguration != nil {
			cdef.LogConfiguration = &types.LogConfiguration{
				LogDriver: types.LogDriver(cd.LogConfiguration.LogDriver),
				Options:   cd.LogConfiguration.Options,
			}
		}

		if cd.HealthCheck != nil {
			cdef.HealthCheck = &types.HealthCheck{
				Command:     cd.HealthCheck.Command,
				Interval:    aws.Int32(cd.HealthCheck.Interval),
				Timeout:     aws.Int32(cd.HealthCheck.Timeout),
				Retries:     aws.Int32(cd.HealthCheck.Retries),
				StartPeriod: aws.Int32(cd.HealthCheck.StartPeriod),
			}
		}

		input.ContainerDefinitions = append(input.ContainerDefinitions, cdef)
	}

	output, err := e.client.RegisterTaskDefinition(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to register task definition: %w", err)
	}

	return *output.TaskDefinition.TaskDefinitionArn, nil
}

// UpdateServiceWithTaskDef updates a service to use a specific task definition ARN.
func (e *ECSClient) UpdateServiceWithTaskDef(ctx context.Context, serviceName, taskDefArn string) error {
	_, err := e.client.UpdateService(ctx, &ecs.UpdateServiceInput{
		Cluster:            &e.cluster,
		Service:            &serviceName,
		TaskDefinition:     aws.String(taskDefArn),
		ForceNewDeployment: true,
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
