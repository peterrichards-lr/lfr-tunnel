package provisioner

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	schedulertypes "github.com/aws/aws-sdk-go-v2/service/scheduler/types"
)

// TestSetScheduleAppliesStartBeforeStop pins the ordering, which is a safety property rather
// than a stylistic one (#1250).
//
// The stop and start schedules are two separate EventBridge resources with no transaction
// across them, so a failure between the two calls leaves the node half-configured. Applying
// stop first means the surviving half is "switches off tonight, with nothing scheduled to
// switch it back on" -- an outage until a human notices. Applying start first means it is
// "wakes as usual, never sleeps", which costs an instance-hour.
func TestSetScheduleAppliesStartBeforeStop(t *testing.T) {
	var order []string

	client := &fakeSchedulerClient{
		getScheduleFunc: func(_ context.Context, _ *scheduler.GetScheduleInput, _ ...func(*scheduler.Options)) (*scheduler.GetScheduleOutput, error) {
			return &scheduler.GetScheduleOutput{
				Target:             &schedulertypes.Target{Arn: aws.String("arn:x"), RoleArn: aws.String("arn:y")},
				FlexibleTimeWindow: &schedulertypes.FlexibleTimeWindow{Mode: schedulertypes.FlexibleTimeWindowModeOff},
			}, nil
		},
		updateScheduleFunc: func(_ context.Context, in *scheduler.UpdateScheduleInput, _ ...func(*scheduler.Options)) (*scheduler.UpdateScheduleOutput, error) {
			order = append(order, aws.ToString(in.Name))
			return &scheduler.UpdateScheduleOutput{}, nil
		},
	}
	b := newTestBackend(&fakeEC2Client{}, client)

	err := b.SetSchedule(context.Background(), "edge-sa", Schedule{
		StopTime: "00:00", StartTime: "08:00", Timezone: "America/Sao_Paulo", Enabled: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(order) != 2 {
		t.Fatalf("expected both schedules to be updated, got %v", order)
	}
	if order[0] != "edge-sa-start" || order[1] != "edge-sa-stop" {
		t.Errorf("expected start to be applied before stop so a partial failure cannot strand a node powered off, got %v", order)
	}
}
