package metrics

import (
	"fmt"
	"strings"
	"sync/atomic"
)

// Notification delivery channels counted by CountNotification.
const (
	ChannelEmail    = "email"
	ChannelTelegram = "telegram"
)

// notifyCounters holds process-lifetime delivery counts: [channel][outcome],
// outcome 0 = sent, 1 = failed. The set of series is fixed, so labels never
// grow with user input.
var notifyCounters [2][2]atomic.Uint64

var notifyChannels = []string{ChannelEmail, ChannelTelegram}

// CountNotification records one delivery attempt (one message, which may
// carry several notifications) on channel. Unknown channels are ignored.
func CountNotification(channel string, sent bool) {
	for i, c := range notifyChannels {
		if c != channel {
			continue
		}
		outcome := 1
		if sent {
			outcome = 0
		}
		notifyCounters[i][outcome].Add(1)
	}
}

// NotificationCount returns the count for channel and outcome (tests).
func NotificationCount(channel string, sent bool) uint64 {
	for i, c := range notifyChannels {
		if c == channel {
			if sent {
				return notifyCounters[i][0].Load()
			}
			return notifyCounters[i][1].Load()
		}
	}
	return 0
}

func writeNotificationCounters(b *strings.Builder) {
	writeHelpType(b, "gorun_notification_deliveries_total", "counter",
		"Notification messages sent or failed per channel since process start.")
	for i, c := range notifyChannels {
		fmt.Fprintf(b, "gorun_notification_deliveries_total{channel=%q,outcome=\"sent\"} %d\n", c, notifyCounters[i][0].Load())
		fmt.Fprintf(b, "gorun_notification_deliveries_total{channel=%q,outcome=\"failed\"} %d\n", c, notifyCounters[i][1].Load())
	}
}
