package provisioner

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	schedulertypes "github.com/aws/aws-sdk-go-v2/service/scheduler/types"
)

// EC2API is the minimal subset of *ec2.Client this backend needs.
// Hand-declaring it (rather than depending on the concrete client) is what
// makes AWSBackend unit-testable without real AWS calls -- matches the
// pattern already used for Route53Provider in pkg/ops/dns_route53.go.
type EC2API interface {
	StartInstances(ctx context.Context, in *ec2.StartInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error)
	StopInstances(ctx context.Context, in *ec2.StopInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error)
	DescribeInstances(ctx context.Context, in *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

// SchedulerAPI is the minimal subset of *scheduler.Client this backend needs.
type SchedulerAPI interface {
	GetSchedule(ctx context.Context, in *scheduler.GetScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.GetScheduleOutput, error)
	UpdateSchedule(ctx context.Context, in *scheduler.UpdateScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.UpdateScheduleOutput, error)
}

// AWSBackend implements Backend against AWS EC2 (start/stop/restart) and
// EventBridge Scheduler (schedule read/update). It expects the stop/start
// schedules it reads and writes to already exist, in the naming convention
// established by scripts/common/schedule-edge-node-hours.sh: "<node_id>-stop"
// and "<node_id>-start" within a single schedule group.
type AWSBackend struct {
	nodes         map[string]NodeTarget
	scheduleGroup string

	// Keyed by region -- EC2/Scheduler clients are region-scoped, and
	// different edge nodes can live in different regions.
	ec2Clients       map[string]EC2API
	schedulerClients map[string]SchedulerAPI
}

// NewAWSBackend builds region-scoped EC2 and Scheduler clients for every
// region present in cfg.Nodes, using the AWS SDK's default credential chain
// (env vars / shared profile / EC2 instance metadata role) -- no
// project-specific credential handling. When deployed, this process is
// expected to run on the central EC2 instance with an attached IAM instance
// profile scoped to exactly the actions it needs.
func NewAWSBackend(ctx context.Context, cfg *Config) (*AWSBackend, error) {
	regions := map[string]struct{}{}
	for _, target := range cfg.Nodes {
		regions[target.Region] = struct{}{}
	}

	ec2Clients := make(map[string]EC2API, len(regions))
	schedulerClients := make(map[string]SchedulerAPI, len(regions))
	for region := range regions {
		awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
		if err != nil {
			return nil, fmt.Errorf("loading AWS config for region %s: %w", region, err)
		}
		ec2Clients[region] = ec2.NewFromConfig(awsCfg)
		schedulerClients[region] = scheduler.NewFromConfig(awsCfg)
	}

	return &AWSBackend{
		nodes:            cfg.Nodes,
		scheduleGroup:    cfg.ScheduleGroup,
		ec2Clients:       ec2Clients,
		schedulerClients: schedulerClients,
	}, nil
}

func (b *AWSBackend) Capabilities() Capabilities {
	return Capabilities{StartStop: true, Restart: true, Scheduling: true}
}

func (b *AWSBackend) target(nodeID string) (NodeTarget, error) {
	t, ok := b.nodes[nodeID]
	if !ok {
		return NodeTarget{}, &ErrNodeNotFound{NodeID: nodeID}
	}
	return t, nil
}

func (b *AWSBackend) Start(ctx context.Context, nodeID string) error {
	target, err := b.target(nodeID)
	if err != nil {
		return err
	}
	_, err = b.ec2Clients[target.Region].StartInstances(ctx, &ec2.StartInstancesInput{
		InstanceIds: []string{target.InstanceID},
	})
	if err != nil {
		return fmt.Errorf("starting %s: %w", nodeID, err)
	}
	return nil
}

func (b *AWSBackend) Stop(ctx context.Context, nodeID string) error {
	target, err := b.target(nodeID)
	if err != nil {
		return err
	}
	_, err = b.ec2Clients[target.Region].StopInstances(ctx, &ec2.StopInstancesInput{
		InstanceIds: []string{target.InstanceID},
	})
	if err != nil {
		return fmt.Errorf("stopping %s: %w", nodeID, err)
	}
	return nil
}

// Restart stops the instance, waits for it to fully reach the stopped
// state, then starts it again. EC2 has no atomic "reboot with guaranteed
// Elastic IP re-association" primitive, so this explicit sequencing is the
// AWS-specific way to fulfil a generic "restart" request -- a future
// non-AWS Backend might have a genuine native restart call instead. This
// method blocks for as long as the whole sequence takes (often 30-90s);
// callers (the HTTP layer) are expected to run it in a background goroutine
// rather than waiting on it inline.
func (b *AWSBackend) Restart(ctx context.Context, nodeID string) error {
	target, err := b.target(nodeID)
	if err != nil {
		return err
	}
	client := b.ec2Clients[target.Region]

	if _, err := client.StopInstances(ctx, &ec2.StopInstancesInput{
		InstanceIds: []string{target.InstanceID},
	}); err != nil {
		return fmt.Errorf("restart(%s): stopping: %w", nodeID, err)
	}

	waiter := ec2.NewInstanceStoppedWaiter(client)
	if err := waiter.Wait(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{target.InstanceID},
	}, 5*time.Minute); err != nil {
		return fmt.Errorf("restart(%s): waiting for stopped state: %w", nodeID, err)
	}

	if _, err := client.StartInstances(ctx, &ec2.StartInstancesInput{
		InstanceIds: []string{target.InstanceID},
	}); err != nil {
		return fmt.Errorf("restart(%s): starting: %w", nodeID, err)
	}
	return nil
}

func (b *AWSBackend) GetSchedule(ctx context.Context, nodeID string) (Schedule, error) {
	target, err := b.target(nodeID)
	if err != nil {
		return Schedule{}, err
	}
	client := b.schedulerClients[target.Region]

	stopSched, err := client.GetSchedule(ctx, &scheduler.GetScheduleInput{
		Name:      aws.String(nodeID + "-stop"),
		GroupName: aws.String(b.scheduleGroup),
	})
	if isScheduleNotFound(err) {
		return Schedule{Enabled: false}, nil
	}
	if err != nil {
		return Schedule{}, fmt.Errorf("getting %s-stop schedule: %w", nodeID, err)
	}

	startSched, err := client.GetSchedule(ctx, &scheduler.GetScheduleInput{
		Name:      aws.String(nodeID + "-start"),
		GroupName: aws.String(b.scheduleGroup),
	})
	if isScheduleNotFound(err) {
		return Schedule{Enabled: false}, nil
	}
	if err != nil {
		return Schedule{}, fmt.Errorf("getting %s-start schedule: %w", nodeID, err)
	}

	stopTime, err := cronToHHMM(aws.ToString(stopSched.ScheduleExpression))
	if err != nil {
		return Schedule{}, fmt.Errorf("parsing %s-stop schedule expression: %w", nodeID, err)
	}
	startTime, err := cronToHHMM(aws.ToString(startSched.ScheduleExpression))
	if err != nil {
		return Schedule{}, fmt.Errorf("parsing %s-start schedule expression: %w", nodeID, err)
	}

	// A node's schedule is only reported "enabled" if BOTH the stop and start
	// schedules are actually ENABLED in EventBridge -- not merely that they
	// exist. This is what lets a specific node be taken off the nightly
	// schedule (left running or stopped indefinitely under manual control,
	// per #883/#884) while every other node's schedule keeps firing.
	enabled := stopSched.State == schedulertypes.ScheduleStateEnabled && startSched.State == schedulertypes.ScheduleStateEnabled

	return Schedule{
		Enabled:   enabled,
		StopTime:  stopTime,
		StartTime: startTime,
		Timezone:  aws.ToString(stopSched.ScheduleExpressionTimezone),
	}, nil
}

// SetSchedule updates the existing "<node_id>-stop" and "<node_id>-start"
// schedules IN PLACE via UpdateSchedule -- never delete-then-recreate (see
// issue #885's explicit requirement). UpdateSchedule requires the full
// schedule definition, so each schedule is first fetched via GetSchedule to
// preserve its existing Target/FlexibleTimeWindow exactly, with only the
// cron expression, timezone, and State (see s.Enabled) changed. Setting
// Enabled=false disables both the stop and start schedules for this node
// via EventBridge's own State field -- the schedules keep their configured
// times, just don't fire, so re-enabling later needs no reconfiguration.
func (b *AWSBackend) SetSchedule(ctx context.Context, nodeID string, s Schedule) error {
	target, err := b.target(nodeID)
	if err != nil {
		return err
	}
	client := b.schedulerClients[target.Region]

	if err := b.updateOne(ctx, client, nodeID+"-stop", s.StopTime, s.Timezone, s.Enabled); err != nil {
		return err
	}
	if err := b.updateOne(ctx, client, nodeID+"-start", s.StartTime, s.Timezone, s.Enabled); err != nil {
		return err
	}
	return nil
}

func (b *AWSBackend) updateOne(ctx context.Context, client SchedulerAPI, name, hhmm, timezone string, enabled bool) error {
	current, err := client.GetSchedule(ctx, &scheduler.GetScheduleInput{
		Name:      aws.String(name),
		GroupName: aws.String(b.scheduleGroup),
	})
	if err != nil {
		return fmt.Errorf("fetching existing schedule %s before update: %w", name, err)
	}

	cron, err := hhmmToCron(hhmm)
	if err != nil {
		return fmt.Errorf("schedule %s: %w", name, err)
	}

	state := schedulertypes.ScheduleStateDisabled
	if enabled {
		state = schedulertypes.ScheduleStateEnabled
	}

	_, err = client.UpdateSchedule(ctx, &scheduler.UpdateScheduleInput{
		Name:                       aws.String(name),
		GroupName:                  aws.String(b.scheduleGroup),
		ScheduleExpression:         aws.String(cron),
		ScheduleExpressionTimezone: aws.String(timezone),
		FlexibleTimeWindow:         current.FlexibleTimeWindow,
		Target:                     current.Target,
		State:                      state,
		Description:                current.Description,
	})
	if err != nil {
		return fmt.Errorf("updating schedule %s: %w", name, err)
	}
	return nil
}

func isScheduleNotFound(err error) bool {
	if err == nil {
		return false
	}
	var notFound *schedulertypes.ResourceNotFoundException
	return errors.As(err, &notFound)
}

// hhmmToCron converts "HH:MM" into EventBridge Scheduler's six-field cron
// expression for a daily trigger at that time: cron(minute hour * * ? *).
func hhmmToCron(hhmm string) (string, error) {
	hour, minute, err := parseHHMM(hhmm)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("cron(%d %d * * ? *)", minute, hour), nil
}

// cronToHHMM extracts "HH:MM" back out of a cron(minute hour * * ? *)
// expression created by hhmmToCron / scripts/common/schedule-edge-node-hours.sh.
func cronToHHMM(cron string) (string, error) {
	inner := strings.TrimSuffix(strings.TrimPrefix(cron, "cron("), ")")
	fields := strings.Fields(inner)
	if len(fields) < 2 {
		return "", fmt.Errorf("unrecognized cron expression: %q", cron)
	}
	minute, err := strconv.Atoi(fields[0])
	if err != nil {
		return "", fmt.Errorf("unrecognized cron minute field in %q: %w", cron, err)
	}
	hour, err := strconv.Atoi(fields[1])
	if err != nil {
		return "", fmt.Errorf("unrecognized cron hour field in %q: %w", cron, err)
	}
	return fmt.Sprintf("%02d:%02d", hour, minute), nil
}

func parseHHMM(hhmm string) (hour, minute int, err error) {
	parts := strings.SplitN(hhmm, ":", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid HH:MM value: %q", hhmm)
	}
	hour, err = strconv.Atoi(parts[0])
	if err != nil || hour < 0 || hour > 23 {
		return 0, 0, fmt.Errorf("invalid hour in %q", hhmm)
	}
	minute, err = strconv.Atoi(parts[1])
	if err != nil || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("invalid minute in %q", hhmm)
	}
	return hour, minute, nil
}
