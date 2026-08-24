package provisioner

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	schedulertypes "github.com/aws/aws-sdk-go-v2/service/scheduler/types"
)

type fakeEC2Client struct {
	startInstancesFunc    func(ctx context.Context, in *ec2.StartInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error)
	stopInstancesFunc     func(ctx context.Context, in *ec2.StopInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error)
	describeInstancesFunc func(ctx context.Context, in *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

func (f *fakeEC2Client) StartInstances(ctx context.Context, in *ec2.StartInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error) {
	return f.startInstancesFunc(ctx, in, optFns...)
}

func (f *fakeEC2Client) StopInstances(ctx context.Context, in *ec2.StopInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error) {
	return f.stopInstancesFunc(ctx, in, optFns...)
}

func (f *fakeEC2Client) DescribeInstances(ctx context.Context, in *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return f.describeInstancesFunc(ctx, in, optFns...)
}

type fakeSchedulerClient struct {
	getScheduleFunc    func(ctx context.Context, in *scheduler.GetScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.GetScheduleOutput, error)
	updateScheduleFunc func(ctx context.Context, in *scheduler.UpdateScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.UpdateScheduleOutput, error)
}

func (f *fakeSchedulerClient) GetSchedule(ctx context.Context, in *scheduler.GetScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.GetScheduleOutput, error) {
	return f.getScheduleFunc(ctx, in, optFns...)
}

func (f *fakeSchedulerClient) UpdateSchedule(ctx context.Context, in *scheduler.UpdateScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.UpdateScheduleOutput, error) {
	return f.updateScheduleFunc(ctx, in, optFns...)
}

func newTestBackend(ec2Client EC2API, schedClient SchedulerAPI) *AWSBackend {
	return &AWSBackend{
		nodes:            map[string]NodeTarget{"edge-sa": {InstanceID: "i-abc123", Region: "sa-east-1"}},
		scheduleGroup:    DefaultScheduleGroup,
		ec2Clients:       map[string]EC2API{"sa-east-1": ec2Client},
		schedulerClients: map[string]SchedulerAPI{"sa-east-1": schedClient},
	}
}

func TestAWSBackend_Start_UnknownNode(t *testing.T) {
	b := newTestBackend(&fakeEC2Client{}, &fakeSchedulerClient{})
	err := b.Start(context.Background(), "does-not-exist")
	var notFound *ErrNodeNotFound
	if !errors.As(err, &notFound) {
		t.Fatalf("expected ErrNodeNotFound, got %v", err)
	}
}

func TestAWSBackend_Start_CallsCorrectInstance(t *testing.T) {
	var gotIDs []string
	client := &fakeEC2Client{
		startInstancesFunc: func(_ context.Context, in *ec2.StartInstancesInput, _ ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error) {
			gotIDs = in.InstanceIds
			return &ec2.StartInstancesOutput{}, nil
		},
	}
	b := newTestBackend(client, &fakeSchedulerClient{})

	if err := b.Start(context.Background(), "edge-sa"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gotIDs) != 1 || gotIDs[0] != "i-abc123" {
		t.Fatalf("expected [i-abc123], got %v", gotIDs)
	}
}

func TestAWSBackend_Stop_PropagatesError(t *testing.T) {
	client := &fakeEC2Client{
		stopInstancesFunc: func(_ context.Context, _ *ec2.StopInstancesInput, _ ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error) {
			return nil, errors.New("throttled")
		},
	}
	b := newTestBackend(client, &fakeSchedulerClient{})

	if err := b.Stop(context.Background(), "edge-sa"); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestAWSBackend_GetSchedule_NotFound(t *testing.T) {
	client := &fakeSchedulerClient{
		getScheduleFunc: func(_ context.Context, _ *scheduler.GetScheduleInput, _ ...func(*scheduler.Options)) (*scheduler.GetScheduleOutput, error) {
			return nil, &schedulertypes.ResourceNotFoundException{Message: aws.String("not found")}
		},
	}
	b := newTestBackend(&fakeEC2Client{}, client)

	sched, err := b.GetSchedule(context.Background(), "edge-sa")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sched.Enabled {
		t.Fatalf("expected Enabled=false when schedule doesn't exist, got %+v", sched)
	}
}

func TestAWSBackend_GetSchedule_ParsesBothSchedules(t *testing.T) {
	client := &fakeSchedulerClient{
		getScheduleFunc: func(_ context.Context, in *scheduler.GetScheduleInput, _ ...func(*scheduler.Options)) (*scheduler.GetScheduleOutput, error) {
			switch aws.ToString(in.Name) {
			case "edge-sa-stop":
				return &scheduler.GetScheduleOutput{
					ScheduleExpression:         aws.String("cron(0 0 * * ? *)"),
					ScheduleExpressionTimezone: aws.String("America/Sao_Paulo"),
					State:                      schedulertypes.ScheduleStateEnabled,
				}, nil
			case "edge-sa-start":
				return &scheduler.GetScheduleOutput{
					ScheduleExpression:         aws.String("cron(0 8 * * ? *)"),
					ScheduleExpressionTimezone: aws.String("America/Sao_Paulo"),
					State:                      schedulertypes.ScheduleStateEnabled,
				}, nil
			}
			return nil, errors.New("unexpected schedule name: " + aws.ToString(in.Name))
		},
	}
	b := newTestBackend(&fakeEC2Client{}, client)

	sched, err := b.GetSchedule(context.Background(), "edge-sa")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Schedule{Enabled: true, StopTime: "00:00", StartTime: "08:00", Timezone: "America/Sao_Paulo"}
	if sched != want {
		t.Fatalf("got %+v, want %+v", sched, want)
	}
}

func TestAWSBackend_SetSchedule_PreservesExistingTargetAndWindow(t *testing.T) {
	existingTarget := &schedulertypes.Target{Arn: aws.String("arn:aws:scheduler:::aws-sdk:ec2:stopInstances"), RoleArn: aws.String("arn:aws:iam::123:role/x")}
	existingWindow := &schedulertypes.FlexibleTimeWindow{Mode: schedulertypes.FlexibleTimeWindowModeOff}

	// Keyed by schedule name rather than "whichever call came last": SetSchedule applies
	// start before stop on purpose (#1250), so asserting on the final call would encode an
	// ordering this test does not care about and break the next time it changes.
	gotExprs := map[string]string{}
	var gotTZ string
	var gotTarget *schedulertypes.Target
	var gotWindow *schedulertypes.FlexibleTimeWindow

	client := &fakeSchedulerClient{
		getScheduleFunc: func(_ context.Context, _ *scheduler.GetScheduleInput, _ ...func(*scheduler.Options)) (*scheduler.GetScheduleOutput, error) {
			return &scheduler.GetScheduleOutput{Target: existingTarget, FlexibleTimeWindow: existingWindow}, nil
		},
		updateScheduleFunc: func(_ context.Context, in *scheduler.UpdateScheduleInput, _ ...func(*scheduler.Options)) (*scheduler.UpdateScheduleOutput, error) {
			gotExprs[aws.ToString(in.Name)] = aws.ToString(in.ScheduleExpression)
			gotTZ = aws.ToString(in.ScheduleExpressionTimezone)
			gotTarget = in.Target
			gotWindow = in.FlexibleTimeWindow
			return &scheduler.UpdateScheduleOutput{}, nil
		},
	}
	b := newTestBackend(&fakeEC2Client{}, client)

	err := b.SetSchedule(context.Background(), "edge-sa", Schedule{StopTime: "23:00", StartTime: "07:00", Timezone: "America/Sao_Paulo", Enabled: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := gotExprs["edge-sa-start"]; got != "cron(0 7 * * ? *)" {
		t.Errorf("expected the start schedule to use cron(0 7 * * ? *), got %q", got)
	}
	if got := gotExprs["edge-sa-stop"]; got != "cron(0 23 * * ? *)" {
		t.Errorf("expected the stop schedule to use cron(0 23 * * ? *), got %q", got)
	}
	if gotTZ != "America/Sao_Paulo" {
		t.Errorf("expected timezone America/Sao_Paulo, got %q", gotTZ)
	}
	if gotTarget != existingTarget {
		t.Error("expected UpdateSchedule to reuse the existing Target fetched via GetSchedule, not construct a new one")
	}
	if gotWindow != existingWindow {
		t.Error("expected UpdateSchedule to reuse the existing FlexibleTimeWindow fetched via GetSchedule")
	}
}

// TestAWSBackend_SetSchedule_TogglesEnabledState is the actual regression
// test for the per-node disable/enable requirement: setting Enabled=false
// must apply State=DISABLED to BOTH the stop and start schedules (so
// neither fires while the node is under manual control), and Enabled=true
// must apply State=ENABLED to both -- independent of every other node's
// schedule, which the shared-vs-per-node IAM role split doesn't affect
// (this is purely an EventBridge Scheduler State toggle).
func TestAWSBackend_SetSchedule_TogglesEnabledState(t *testing.T) {
	existingTarget := &schedulertypes.Target{Arn: aws.String("x"), RoleArn: aws.String("y")}
	existingWindow := &schedulertypes.FlexibleTimeWindow{Mode: schedulertypes.FlexibleTimeWindowModeOff}
	var gotStates []schedulertypes.ScheduleState

	client := &fakeSchedulerClient{
		getScheduleFunc: func(_ context.Context, _ *scheduler.GetScheduleInput, _ ...func(*scheduler.Options)) (*scheduler.GetScheduleOutput, error) {
			return &scheduler.GetScheduleOutput{Target: existingTarget, FlexibleTimeWindow: existingWindow}, nil
		},
		updateScheduleFunc: func(_ context.Context, in *scheduler.UpdateScheduleInput, _ ...func(*scheduler.Options)) (*scheduler.UpdateScheduleOutput, error) {
			gotStates = append(gotStates, in.State)
			return &scheduler.UpdateScheduleOutput{}, nil
		},
	}
	b := newTestBackend(&fakeEC2Client{}, client)

	if err := b.SetSchedule(context.Background(), "edge-sa", Schedule{StopTime: "00:00", StartTime: "08:00", Timezone: "UTC", Enabled: false}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, s := range gotStates {
		if s != schedulertypes.ScheduleStateDisabled {
			t.Errorf("Enabled=false: expected both schedules set to DISABLED, got %v", gotStates)
			break
		}
	}
	if len(gotStates) != 2 {
		t.Fatalf("expected 2 UpdateSchedule calls (stop + start), got %d", len(gotStates))
	}

	gotStates = nil
	if err := b.SetSchedule(context.Background(), "edge-sa", Schedule{StopTime: "00:00", StartTime: "08:00", Timezone: "UTC", Enabled: true}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, s := range gotStates {
		if s != schedulertypes.ScheduleStateEnabled {
			t.Errorf("Enabled=true: expected both schedules set to ENABLED, got %v", gotStates)
			break
		}
	}
}

// TestAWSBackend_GetSchedule_ReportsDisabledWhenEitherHalfIsDisabled ensures
// a node's schedule only reads back as "enabled" when BOTH its stop and
// start schedules are actually enabled in EventBridge -- not merely that
// they exist (the previous behavior, which couldn't distinguish a disabled
// node from a normal one).
func TestAWSBackend_GetSchedule_ReportsDisabledWhenEitherHalfIsDisabled(t *testing.T) {
	client := &fakeSchedulerClient{
		getScheduleFunc: func(_ context.Context, in *scheduler.GetScheduleInput, _ ...func(*scheduler.Options)) (*scheduler.GetScheduleOutput, error) {
			state := schedulertypes.ScheduleStateEnabled
			if aws.ToString(in.Name) == "edge-sa-stop" {
				state = schedulertypes.ScheduleStateDisabled
			}
			return &scheduler.GetScheduleOutput{
				ScheduleExpression:         aws.String("cron(0 0 * * ? *)"),
				ScheduleExpressionTimezone: aws.String("UTC"),
				State:                      state,
			}, nil
		},
	}
	b := newTestBackend(&fakeEC2Client{}, client)

	sched, err := b.GetSchedule(context.Background(), "edge-sa")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sched.Enabled {
		t.Error("expected Enabled=false when the stop schedule is disabled, even though the start schedule is enabled")
	}
}

func TestHHMMCronRoundTrip(t *testing.T) {
	cases := []string{"00:00", "08:00", "23:59", "07:05"}
	for _, hhmm := range cases {
		cron, err := hhmmToCron(hhmm)
		if err != nil {
			t.Fatalf("hhmmToCron(%q): %v", hhmm, err)
		}
		got, err := cronToHHMM(cron)
		if err != nil {
			t.Fatalf("cronToHHMM(%q): %v", cron, err)
		}
		if got != hhmm {
			t.Errorf("round-trip mismatch: %q -> %q -> %q", hhmm, cron, got)
		}
	}
}

func TestHHMMToCron_RejectsInvalid(t *testing.T) {
	invalid := []string{"", "24:00", "12:60", "not-a-time", "25:61"}
	for _, hhmm := range invalid {
		if _, err := hhmmToCron(hhmm); err == nil {
			t.Errorf("hhmmToCron(%q): expected error, got none", hhmm)
		}
	}
}

func TestHHMMToCron_AcceptsUnpaddedSingleDigits(t *testing.T) {
	cron, err := hhmmToCron("8:05")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cron != "cron(5 8 * * ? *)" {
		t.Errorf("got %q", cron)
	}
}
