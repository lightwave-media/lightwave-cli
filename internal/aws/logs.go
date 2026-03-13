package aws

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
)

// LogsClient wraps the CloudWatch Logs client
type LogsClient struct {
	client *cloudwatchlogs.Client
}

// LogEvent represents a single log event
type LogEvent struct {
	Timestamp time.Time
	Message   string
	Stream    string
}

// NewLogsClient creates a new CloudWatch Logs client
func NewLogsClient(ctx context.Context) (*LogsClient, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return &LogsClient{
		client: cloudwatchlogs.NewFromConfig(cfg),
	}, nil
}

// GetRecentLogs returns recent logs from a log group
func (l *LogsClient) GetRecentLogs(ctx context.Context, logGroup string, since time.Duration, limit int32) ([]LogEvent, error) {
	startTime := time.Now().Add(-since).UnixMilli()

	// First, get log streams
	streamsOutput, err := l.client.DescribeLogStreams(ctx, &cloudwatchlogs.DescribeLogStreamsInput{
		LogGroupName: &logGroup,
		OrderBy:      "LastEventTime",
		Descending:   boolPtr(true),
		Limit:        int32Ptr(5), // Get 5 most recent streams
	})
	if err != nil {
		return nil, fmt.Errorf("failed to describe log streams: %w", err)
	}

	var allEvents []LogEvent

	// Get events from each stream
	for _, stream := range streamsOutput.LogStreams {
		eventsOutput, err := l.client.GetLogEvents(ctx, &cloudwatchlogs.GetLogEventsInput{
			LogGroupName:  &logGroup,
			LogStreamName: stream.LogStreamName,
			StartTime:     &startTime,
			Limit:         &limit,
		})
		if err != nil {
			continue // Skip streams with errors
		}

		for _, event := range eventsOutput.Events {
			allEvents = append(allEvents, LogEvent{
				Timestamp: time.UnixMilli(*event.Timestamp),
				Message:   *event.Message,
				Stream:    *stream.LogStreamName,
			})
		}
	}

	// Sort by timestamp
	sort.Slice(allEvents, func(i, j int) bool {
		return allEvents[i].Timestamp.Before(allEvents[j].Timestamp)
	})

	// Limit results
	if int32(len(allEvents)) > limit {
		allEvents = allEvents[len(allEvents)-int(limit):]
	}

	return allEvents, nil
}

// TailLogs tails logs in real-time (returns a channel)
func (l *LogsClient) TailLogs(ctx context.Context, logGroup string, streamPrefix string) (<-chan LogEvent, error) {
	events := make(chan LogEvent, 100)

	go func() {
		defer close(events)

		var lastToken *string
		startTime := time.Now().Add(-30 * time.Second).UnixMilli()

		for {
			select {
			case <-ctx.Done():
				return
			default:
				// Get log streams matching prefix
				streamsOutput, err := l.client.DescribeLogStreams(ctx, &cloudwatchlogs.DescribeLogStreamsInput{
					LogGroupName:        &logGroup,
					LogStreamNamePrefix: &streamPrefix,
					OrderBy:             "LastEventTime",
					Descending:          boolPtr(true),
					Limit:               int32Ptr(3),
				})
				if err != nil {
					time.Sleep(5 * time.Second)
					continue
				}

				for _, stream := range streamsOutput.LogStreams {
					eventsOutput, err := l.client.GetLogEvents(ctx, &cloudwatchlogs.GetLogEventsInput{
						LogGroupName:  &logGroup,
						LogStreamName: stream.LogStreamName,
						StartTime:     &startTime,
						NextToken:     lastToken,
					})
					if err != nil {
						continue
					}

					for _, event := range eventsOutput.Events {
						events <- LogEvent{
							Timestamp: time.UnixMilli(*event.Timestamp),
							Message:   *event.Message,
							Stream:    *stream.LogStreamName,
						}
					}

					if eventsOutput.NextForwardToken != nil && *eventsOutput.NextForwardToken != "" {
						lastToken = eventsOutput.NextForwardToken
					}
				}

				time.Sleep(2 * time.Second)
			}
		}
	}()

	return events, nil
}

// GetLogGroups returns log groups matching a prefix
func (l *LogsClient) GetLogGroups(ctx context.Context, prefix string) ([]string, error) {
	var groups []string
	paginator := cloudwatchlogs.NewDescribeLogGroupsPaginator(l.client, &cloudwatchlogs.DescribeLogGroupsInput{
		LogGroupNamePrefix: &prefix,
	})

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to describe log groups: %w", err)
		}
		for _, group := range output.LogGroups {
			groups = append(groups, *group.LogGroupName)
		}
	}

	return groups, nil
}

func boolPtr(b bool) *bool {
	return &b
}

func int32Ptr(i int32) *int32 {
	return &i
}
