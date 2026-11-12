package metrics

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestNotificationCountersRendered(t *testing.T) {
	sent := NotificationCount(ChannelTelegram, true)
	failed := NotificationCount(ChannelEmail, false)
	CountNotification(ChannelTelegram, true)
	CountNotification(ChannelEmail, false)
	CountNotification("sms", true) // unknown channels add no series
	body, err := render(context.Background(), &HTTP{}, nil, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	out := string(body)
	for _, want := range []string{
		"# TYPE gorun_notification_deliveries_total counter",
		fmt.Sprintf(`gorun_notification_deliveries_total{channel="telegram",outcome="sent"} %d`, sent+1),
		fmt.Sprintf(`gorun_notification_deliveries_total{channel="email",outcome="failed"} %d`, failed+1),
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("metrics missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, `channel="sms"`) {
		t.Fatal("unknown channel rendered")
	}
}
