package workagents

import (
	"errors"
	"testing"

	"github.com/zadstack/gorun/internal/db"
)

// A close that commits after postChannelMessage read the open status still
// wins: the insert transaction re-checks it under the write lock.
func TestChannelMessageInsertRechecksOpenStatus(t *testing.T) {
	s, ctx := setup(t, nil)
	c, e := s.createChannel(ctx, "Race", nil)
	if e != nil {
		t.Fatal(e)
	}
	m := ChannelMessage{ID: db.NewID(), ChannelID: c.ID, AuthorKind: "user", AuthorID: owner(ctx), AuthorName: "Owner", Body: "late", Meta: []byte(`{}`), CreatedAt: db.FormatTime(s.now())}
	if e = s.insertChannelMessage(ctx, m, false); e != nil {
		t.Fatalf("insert into an open channel: %v", e)
	}
	if _, e = s.closeChannel(ctx, c.ID); e != nil {
		t.Fatal(e)
	}
	m.ID = db.NewID()
	if e = s.insertChannelMessage(ctx, m, false); !errors.Is(e, ErrChannelClosed) {
		t.Fatalf("insert into a closed channel: %v", e)
	}
	var n int
	if e = s.d.DB.QueryRow(`SELECT count(*) FROM workagents_channel_messages WHERE channel_id=?`, c.ID).Scan(&n); e != nil || n != 1 {
		t.Fatalf("messages = %d (%v), want only the one posted while open", n, e)
	}
}
