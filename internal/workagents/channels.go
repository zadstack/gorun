package workagents

// Agent channels (قنوات الوكلاء): owner-scoped shared threads where the
// account's AI agents and humans post messages and advisory task hand-offs.
// Messages and hand-offs are data only: nothing here executes, publishes or
// changes any other module's state, and channel data never leaves the
// owning account (every query is scoped by owner(ctx)).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

const (
	// channelCap is the maximum number of open channels per owner; creating
	// beyond it fails with ErrChannelBusy (HTTP 429).
	channelCap = 20
	// channelMemberCap is the maximum members per channel: the owner user
	// plus up to 11 of their own agent keys.
	channelMemberCap = 12
	// channelMessageCapBounds is the message body bound in bytes, matching
	// the workagents_channel_messages.body CHECK (SQLite length() counts
	// characters, so a byte bound is always at least as strict).
	channelMessageCapBytes = 8192
	// channelTitleCapRunes is the title bound in characters, matching the
	// workagents_channels.title CHECK.
	channelTitleCapRunes = 120
	// channelListLimit bounds ad hoc list reads (admin screen, MCP list).
	channelListLimit = 100
)

// Stable codes surfaced to REST (inside the workagents_failed message) and
// to MCP tools (as ToolError codes). They never leak message contents.
var (
	ErrChannelInvalid  = errors.New("channel_input_invalid")
	ErrChannelNotFound = errors.New("channel not found")
	ErrChannelClosed   = errors.New("channel_closed")
	ErrChannelMember   = errors.New("member_not_allowed")
	ErrChannelBusy     = errors.New("channel limit reached for this account (20 open channels)")
	ErrChannelDenied   = errors.New("channel permission denied")
)

// ChannelMember is one thread participant: kind "user" (the owner account)
// or "agent_key" (an agent key of the same owner, enforced in code because
// api_keys has no owner column to reference).
type ChannelMember struct {
	Kind        string `json:"kind" enum:"user,agent_key" doc:"user: the owning account; agent_key: one of its agent keys"`
	MemberID    string `json:"member_id"`
	DisplayName string `json:"display_name,omitempty"`
	AddedAt     string `json:"added_at,omitempty"`
}

// Channel is a shared thread for this owner's agents and humans.
type Channel struct {
	ID        string          `json:"id" doc:"Opaque channel id"`
	Title     string          `json:"title"`
	Status    string          `json:"status" enum:"open,closed"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
	Members   []ChannelMember `json:"members,omitempty" doc:"Included by the detail view only"`
}

// ChannelSummary is a list row: channel identity plus activity counters.
type ChannelSummary struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Status       string `json:"status" enum:"open,closed"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	MemberCount  int    `json:"member_count"`
	MessageCount int    `json:"message_count"`
	LastActivity string `json:"last_activity,omitempty"`
}

// ChannelHandoff is the advisory payload stored in a message's meta as
// {"type":"handoff",...}. It points another member at this message. A
// hand-off is advisory ONLY: it executes nothing, schedules nothing and has
// no side effects beyond the audit trail — the targeted member (or a human)
// still decides and acts through their own governed tools and approvals.
type ChannelHandoff struct {
	TargetMemberID   string `json:"target_member_id"`
	TargetMemberKind string `json:"target_member_kind" enum:"user,agent_key"`
}

// ChannelMessage is one posted message. Meta is advisory JSON; a hand-off
// above is the only well-known shape.
type ChannelMessage struct {
	ID         string          `json:"id" doc:"Opaque message id"`
	ChannelID  string          `json:"channel_id"`
	AuthorKind string          `json:"author_kind" enum:"user,agent_key"`
	AuthorID   string          `json:"author_id" doc:"user or api_keys id"`
	AuthorName string          `json:"author_name"`
	Body       string          `json:"body"`
	Meta       json.RawMessage `json:"meta,omitempty" doc:"Advisory metadata; a hand-off marks the recipient, it does not execute"`
	CreatedAt  string          `json:"created_at"`
}

// channelReadAllowed reports whether ctx may read its owner's channels.
func channelReadAllowed(ctx context.Context) bool {
	return owner(ctx) != "" && auth.Can(ctx, "workagents.read")
}

// channelWriteAllowed reports whether ctx may post to its owner's channels.
func channelWriteAllowed(ctx context.Context) bool {
	return owner(ctx) != "" && auth.Can(ctx, "workagents.channels")
}

// channelManageAllowed reports whether ctx may create threads (create uses
// the write permission), add members and close channels.
func channelManageAllowed(ctx context.Context) bool {
	return owner(ctx) != "" && auth.Can(ctx, "workagents.manage")
}

// channelAuthor is who is posting: kind "user" (the owner account) or
// "agent_key" (one of its agent keys).
type channelAuthor struct {
	Kind, ID, Name string
}

// channelAuthorFromContext resolves the posting identity for session
// principals: a session user posts as kind "user" (always the owning
// account of this scope).
func channelAuthorFromContext(ctx context.Context) (channelAuthor, bool) {
	a := auth.ActorFromContext(ctx)
	if a.Kind != "user" || a.ID == "" || a.ID != owner(ctx) {
		return channelAuthor{}, false
	}
	return channelAuthor{Kind: "user", ID: a.ID, Name: a.Name}, true
}

// postAuthor resolves the message author for posting: agent keys post as
// kind "agent_key" with their api_keys id; a plain (human) REST key operates
// as its owner — author kind "user" with the owner's member id, named after
// the key — exactly like the owner's own member row.
func (s *service) postAuthor(ctx context.Context) (channelAuthor, bool) {
	a := auth.ActorFromContext(ctx)
	switch a.Kind {
	case "api_key":
		if a.ID == "" {
			return channelAuthor{}, false
		}
		keys, ok := kernel.Get[auth.KeyService](s.d, kernel.ServiceAPIKeys)
		if !ok {
			return channelAuthor{}, false
		}
		k, e := keys.Get(ctx, a.ID)
		if e != nil {
			return channelAuthor{}, false
		}
		if k.Kind == auth.KeyKindAgent {
			name := k.Name
			if name == "" {
				name = a.ID
			}
			return channelAuthor{Kind: "agent_key", ID: k.ID, Name: name}, true
		}
		if owner(ctx) == "" || k.UserID != owner(ctx) {
			return channelAuthor{}, false
		}
		return channelAuthor{Kind: "user", ID: owner(ctx), Name: k.Name}, true
	default:
		return channelAuthorFromContext(ctx)
	}
}

// createChannel creates a channel for the acting owner: the owner user is
// always a member, plus at most 11 of their own, live agent keys. Requests
// through the admin screen or the API share this path.
func (s *service) createChannel(ctx context.Context, title string, members []ChannelMember) (Channel, error) {
	if !channelWriteAllowed(ctx) {
		return Channel{}, ErrChannelDenied
	}
	return s.createChannelFor(ctx, owner(ctx), title, members)
}

// createChannelFor validates and stores the channel in one transaction with
// its audit event. Ownership is fixed by ownerID; callers never pass a user
// value from request input.
func (s *service) createChannelFor(ctx context.Context, ownerID, title string, members []ChannelMember) (Channel, error) {
	title = strings.TrimSpace(title)
	if rc := utf8.RuneCountInString(title); rc < 1 || rc > channelTitleCapRunes {
		return Channel{}, fmt.Errorf("%w: title must be 1-120 characters", ErrChannelInvalid)
	}
	users, ok := kernel.Get[auth.Service](s.d, kernel.ServiceAuth)
	if !ok {
		return Channel{}, errUnavailable
	}
	ownerUser, e := users.Get(ctx, ownerID)
	if e != nil || ownerUser.Status != auth.StatusActive {
		return Channel{}, ErrChannelNotFound
	}
	// Members: the owner user plus validated agent keys, deduplicated. The
	// key service is required only when agent keys are requested.
	var keys auth.KeyService
	seen := map[string]bool{"user\x00" + ownerID: true}
	agentKeys := map[string]*auth.APIKey{}
	for _, m := range members {
		m.Kind = strings.TrimSpace(m.Kind)
		switch m.Kind {
		case "user":
			if m.MemberID != ownerID {
				return Channel{}, fmt.Errorf("%w: only the owning account joins as a user member", ErrChannelInvalid)
			}
		case "agent_key":
			if !seen["agent\x00"+m.MemberID] {
				if keys == nil {
					if k, ok := kernel.Get[auth.KeyService](s.d, kernel.ServiceAPIKeys); ok {
						keys = k
					} else {
						return Channel{}, errUnavailable
					}
				}
				k, e := s.agentKeyMember(ctx, keys, ownerID, m.MemberID)
				if e != nil {
					return Channel{}, e
				}
				agentKeys[m.MemberID] = k
				seen["agent\x00"+m.MemberID] = true
			}
		default:
			return Channel{}, fmt.Errorf("%w: member kind must be user or agent_key", ErrChannelInvalid)
		}
	}
	if 1+len(agentKeys) > channelMemberCap {
		return Channel{}, fmt.Errorf("%w: at most %d members per channel", ErrChannelInvalid, channelMemberCap)
	}
	c := Channel{ID: db.NewID(), Title: title, Status: "open", CreatedAt: db.FormatTime(s.now()), UpdatedAt: db.FormatTime(s.now())}
	e = db.InTx(ctx, s.d.DB, func(tx *sql.Tx) error {
		var open int
		if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM workagents_channels WHERE owner_id=? AND status='open'`, ownerID).Scan(&open); e != nil {
			return e
		}
		if open >= channelCap {
			return ErrChannelBusy
		}
		if _, e := tx.ExecContext(ctx, `INSERT INTO workagents_channels(id,owner_id,title,status,created_at,updated_at)VALUES(?,?,?,'open',?,?)`, c.ID, ownerID, c.Title, c.CreatedAt, c.UpdatedAt); e != nil {
			if isUniqueConstraint(e) {
				return fmt.Errorf("%w: a channel with this title already exists", ErrChannelInvalid)
			}
			return e
		}
		if _, e := s.insertChannelMember(ctx, tx, c.ID, "user", ownerID, ownerUser.Name, c.CreatedAt); e != nil {
			return e
		}
		for id, k := range agentKeys {
			if _, e := s.insertChannelMember(ctx, tx, c.ID, "agent_key", id, k.Name, c.CreatedAt); e != nil {
				return e
			}
		}
		total := 1 + len(agentKeys)
		if s.d.Audit != nil {
			s.d.Audit.RecordTx(ctx, tx, "workagent.channel.created", "channel", c.ID, map[string]any{"member_count": total})
		}
		return nil
	})
	if e != nil {
		return Channel{}, e
	}
	return s.channelDetail(ctx, c.ID)
}

// insertChannelMember writes one membership row inside tx.
func (s *service) insertChannelMember(ctx context.Context, tx *sql.Tx, channelID, kind, memberID, name, addedAt string) (sql.Result, error) {
	return tx.ExecContext(ctx, `INSERT INTO workagents_channel_members(channel_id,member_kind,member_id,display_name,added_at)VALUES(?,?,?,?,?)`,
		channelID, kind, memberID, name, addedAt)
}

// agentKeyMember validates memberID as a live agent key of ownerID (exists,
// kind agent, same owner, not revoked, not expired) and returns it.
func (s *service) agentKeyMember(ctx context.Context, keys auth.KeyService, ownerID, memberID string) (*auth.APIKey, error) {
	k, e := keys.Get(ctx, memberID)
	if e != nil {
		return nil, ErrChannelMember
	}
	if k.Kind != auth.KeyKindAgent || k.UserID != ownerID || k.RevokedAt != "" {
		return nil, ErrChannelMember
	}
	if k.ExpiresAt != "" {
		if exp, e := db.ParseTime(k.ExpiresAt); e != nil || !s.now().Before(exp) {
			return nil, ErrChannelMember
		}
	}
	return k, nil
}

const channelSummarySelect = `SELECT c.id,c.title,c.status,c.created_at,c.updated_at,
 (SELECT count(*) FROM workagents_channel_members m WHERE m.channel_id=c.id),
 (SELECT count(*) FROM workagents_channel_messages g WHERE g.channel_id=c.id),
 COALESCE((SELECT max(created_at) FROM workagents_channel_messages g WHERE g.channel_id=c.id),c.updated_at)
FROM workagents_channels c`

func scanChannelSummary(row interface{ Scan(...any) error }) (ChannelSummary, error) {
	var out ChannelSummary
	if e := row.Scan(&out.ID, &out.Title, &out.Status, &out.CreatedAt, &out.UpdatedAt, &out.MemberCount, &out.MessageCount, &out.LastActivity); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return out, ErrChannelNotFound
		}
		return out, e
	}
	return out, nil
}

// channelsPage lists the owner's channels newest first with one page of
// results and the total count. Locked access is in the handlers.
func (s *service) channelsPage(ctx context.Context, page, perPage int) ([]ChannelSummary, int, error) {
	if !channelReadAllowed(ctx) {
		return nil, 0, ErrChannelDenied
	}
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 20
	}
	if perPage > 100 {
		perPage = 100
	}
	var total int
	if e := s.d.DB.QueryRowContext(ctx, `SELECT count(*) FROM workagents_channels WHERE owner_id=?`, owner(ctx)).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := s.d.DB.QueryContext(ctx, channelSummarySelect+` WHERE c.owner_id=? ORDER BY c.created_at DESC, c.id DESC LIMIT ? OFFSET ?`, owner(ctx), perPage, (page-1)*perPage)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []ChannelSummary{}
	for rows.Next() {
		row, e := scanChannelSummary(rows)
		if e != nil {
			return nil, 0, e
		}
		out = append(out, row)
	}
	return out, total, rows.Err()
}

// channelDetail returns the owner's channel including its members.
func (s *service) channelDetail(ctx context.Context, id string) (Channel, error) {
	if !channelReadAllowed(ctx) {
		return Channel{}, ErrChannelDenied
	}
	var c Channel
	e := s.d.DB.QueryRowContext(ctx, `SELECT id,title,status,created_at,updated_at FROM workagents_channels WHERE id=? AND owner_id=?`, id, owner(ctx)).
		Scan(&c.ID, &c.Title, &c.Status, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return Channel{}, ErrChannelNotFound
	}
	if e != nil {
		return Channel{}, e
	}
	rows, e := s.d.DB.QueryContext(ctx, `SELECT member_kind,member_id,display_name,added_at FROM workagents_channel_members WHERE channel_id=? ORDER BY member_kind,display_name,member_id`, id)
	if e != nil {
		return Channel{}, e
	}
	defer rows.Close()
	c.Members = []ChannelMember{}
	for rows.Next() {
		var m ChannelMember
		if e = rows.Scan(&m.Kind, &m.MemberID, &m.DisplayName, &m.AddedAt); e != nil {
			return Channel{}, e
		}
		c.Members = append(c.Members, m)
	}
	return c, rows.Err()
}

// addChannelMember appends one of the owner's own agent keys (kind
// agent_key; the kind user row is fixed to the owner) to an open channel.
func (s *service) addChannelMember(ctx context.Context, channelID string, m ChannelMember) (Channel, error) {
	if !channelManageAllowed(ctx) {
		return Channel{}, ErrChannelDenied
	}
	keys, ok := kernel.Get[auth.KeyService](s.d, kernel.ServiceAPIKeys)
	if !ok {
		return Channel{}, errUnavailable
	}
	users, ok := kernel.Get[auth.Service](s.d, kernel.ServiceAuth)
	if !ok {
		return Channel{}, errUnavailable
	}
	var channelOwner, status string
	e := s.d.DB.QueryRowContext(ctx, `SELECT owner_id,status FROM workagents_channels WHERE id=? AND owner_id=?`, channelID, owner(ctx)).Scan(&channelOwner, &status)
	if errors.Is(e, sql.ErrNoRows) {
		return Channel{}, ErrChannelNotFound
	}
	if e != nil {
		return Channel{}, e
	}
	if status != "open" {
		return Channel{}, ErrChannelClosed
	}
	var display string
	now := db.FormatTime(s.now())
	m.Kind = strings.TrimSpace(m.Kind)
	switch m.Kind {
	case "user":
		if m.MemberID != channelOwner {
			return Channel{}, fmt.Errorf("%w: only the owning account joins as a user member", ErrChannelInvalid)
		}
		u, e := users.Get(ctx, m.MemberID)
		if e != nil || u.Status != auth.StatusActive {
			return Channel{}, ErrChannelMember
		}
		display = u.Name
	case "agent_key":
		k, e := s.agentKeyMember(ctx, keys, channelOwner, m.MemberID)
		if e != nil {
			return Channel{}, e
		}
		display = k.Name
	default:
		return Channel{}, fmt.Errorf("%w: member kind must be user or agent_key", ErrChannelInvalid)
	}
	e = db.InTx(ctx, s.d.DB, func(tx *sql.Tx) error {
		var n int
		if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM workagents_channel_members WHERE channel_id=? AND member_kind=? AND member_id=?`, channelID, m.Kind, m.MemberID).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return nil // already a member: idempotent add
		}
		var total int
		if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM workagents_channel_members WHERE channel_id=?`, channelID).Scan(&total); e != nil {
			return e
		}
		if total >= channelMemberCap {
			return fmt.Errorf("%w: at most %d members per channel", ErrChannelInvalid, channelMemberCap)
		}
		_, e := s.insertChannelMember(ctx, tx, channelID, m.Kind, m.MemberID, display, now)
		return e
	})
	if e != nil {
		return Channel{}, e
	}
	return s.channelDetail(ctx, channelID)
}

// isUniqueConstraint reports whether err is SQLite's primary/unique
// constraint failure (error strings are the stable contract in this repo).
func isUniqueConstraint(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}

// postChannelMessage appends one message authored by the acting principal.
// The author must already be a member of the open channel. A non-empty
// targetMemberID marks the meta as an advisory hand-off to that member; the
// hand-off only points at them — it never executes anything.
func (s *service) postChannelMessage(ctx context.Context, channelID, body, targetMemberID string) (ChannelMessage, error) {
	if !channelWriteAllowed(ctx) {
		return ChannelMessage{}, ErrChannelDenied
	}
	author, ok := s.postAuthor(ctx)
	if !ok {
		return ChannelMessage{}, ErrChannelDenied
	}
	body = strings.TrimSpace(body)
	if len(body) < 1 || len(body) > channelMessageCapBytes || !utf8.ValidString(body) {
		return ChannelMessage{}, fmt.Errorf("%w: message body must be 1-8192 bytes", ErrChannelInvalid)
	}
	var status string
	e := s.d.DB.QueryRowContext(ctx, `SELECT status FROM workagents_channels WHERE id=? AND owner_id=?`, channelID, owner(ctx)).Scan(&status)
	if errors.Is(e, sql.ErrNoRows) {
		return ChannelMessage{}, ErrChannelNotFound
	}
	if e != nil {
		return ChannelMessage{}, e
	}
	if status != "open" {
		return ChannelMessage{}, ErrChannelClosed
	}
	if !s.isChannelMember(ctx, channelID, author.Kind, author.ID) {
		return ChannelMessage{}, ErrChannelMember
	}
	meta := json.RawMessage(`{}`)
	handoff := false
	if target := strings.TrimSpace(targetMemberID); target != "" {
		var kind string
		if e := s.d.DB.QueryRowContext(ctx, `SELECT member_kind FROM workagents_channel_members WHERE channel_id=? AND member_id=?`, channelID, target).Scan(&kind); e != nil {
			return ChannelMessage{}, ErrChannelMember
		}
		payload, _ := json.Marshal(map[string]string{"type": "handoff", "target_member_id": target, "target_member_kind": kind})
		meta = payload
		handoff = true
	}
	m := ChannelMessage{ID: db.NewID(), ChannelID: channelID, AuthorKind: author.Kind, AuthorID: author.ID, AuthorName: author.Name, Body: body, Meta: meta, CreatedAt: db.FormatTime(s.now())}
	e = db.InTx(ctx, s.d.DB, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, `INSERT INTO workagents_channel_messages(id,channel_id,author_kind,author_id,author_name,body,meta_json,created_at)VALUES(?,?,?,?,?,?,?,?)`,
			m.ID, channelID, m.AuthorKind, m.AuthorID, m.AuthorName, m.Body, string(meta), m.CreatedAt); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, `UPDATE workagents_channels SET updated_at=? WHERE id=?`, m.CreatedAt, channelID); e != nil {
			return e
		}
		// The body is never copied into audit metadata.
		if s.d.Audit != nil {
			s.d.Audit.RecordTx(ctx, tx, "workagent.channel.message", "channel", channelID,
				map[string]any{"message_id": m.ID, "author_kind": m.AuthorKind, "handoff": handoff})
		}
		return nil
	})
	if e != nil {
		return ChannelMessage{}, e
	}
	return m, nil
}

// isChannelMember reports whether member (kind,id) is on the channel.
func (s *service) isChannelMember(ctx context.Context, channelID, kind, memberID string) bool {
	var n int
	if e := s.d.DB.QueryRowContext(ctx, `SELECT count(*) FROM workagents_channel_members WHERE channel_id=? AND member_kind=? AND member_id=?`, channelID, kind, memberID).Scan(&n); e != nil || n == 0 {
		return false
	}
	return true
}

// channelMessages reads a channel's messages ascending, optionally after a
// message id cursor, one page at a time with the total match count.
func (s *service) channelMessages(ctx context.Context, channelID, after string, page, perPage int) ([]ChannelMessage, int, error) {
	if !channelReadAllowed(ctx) {
		return nil, 0, ErrChannelDenied
	}
	var status string
	e := s.d.DB.QueryRowContext(ctx, `SELECT status FROM workagents_channels WHERE id=? AND owner_id=?`, channelID, owner(ctx)).Scan(&status)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, 0, ErrChannelNotFound
	}
	if e != nil {
		return nil, 0, e
	}
	conds, args := []string{"channel_id=?"}, []any{channelID}
	if after = strings.TrimSpace(after); after != "" {
		var at string
		if e := s.d.DB.QueryRowContext(ctx, `SELECT created_at FROM workagents_channel_messages WHERE id=? AND channel_id=?`, after, channelID).Scan(&at); errors.Is(e, sql.ErrNoRows) {
			return nil, 0, fmt.Errorf("%w: after must be a message of this channel", ErrChannelInvalid)
		} else if e != nil {
			return nil, 0, e
		}
		conds, args = append(conds, "(created_at,id) > (?,?)"), append(args, at, after)
	}
	where := strings.Join(conds, " AND ")
	var total int
	if e = s.d.DB.QueryRowContext(ctx, `SELECT count(*) FROM workagents_channel_messages WHERE `+where, args...).Scan(&total); e != nil {
		return nil, 0, e
	}
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 20
	}
	if perPage > 100 {
		perPage = 100
	}
	args = append(args, perPage, (page-1)*perPage)
	rows, e := s.d.DB.QueryContext(ctx, `SELECT id,channel_id,author_kind,author_id,author_name,body,meta_json,created_at FROM workagents_channel_messages WHERE `+where+` ORDER BY created_at, id LIMIT ? OFFSET ?`, args...)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []ChannelMessage{}
	for rows.Next() {
		var m ChannelMessage
		var meta string
		if e = rows.Scan(&m.ID, &m.ChannelID, &m.AuthorKind, &m.AuthorID, &m.AuthorName, &m.Body, &meta, &m.CreatedAt); e != nil {
			return nil, 0, e
		}
		if m.Meta = json.RawMessage(meta); !json.Valid(m.Meta) {
			m.Meta = json.RawMessage(`{}`)
		}
		out = append(out, m)
	}
	return out, total, rows.Err()
}

// closeChannel sets status='closed' on the owner's channel. Closing is
// idempotent: an already closed channel returns its current view. Closed
// channels deny new messages and members (channel_closed).
func (s *service) closeChannel(ctx context.Context, id string) (Channel, error) {
	if !channelManageAllowed(ctx) {
		return Channel{}, ErrChannelDenied
	}
	now := db.FormatTime(s.now())
	e := db.InTx(ctx, s.d.DB, func(tx *sql.Tx) error {
		res, e := tx.ExecContext(ctx, `UPDATE workagents_channels SET status='closed',updated_at=? WHERE id=? AND owner_id=?`, now, id, owner(ctx))
		if e != nil {
			return e
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrChannelNotFound
		}
		if s.d.Audit != nil {
			s.d.Audit.RecordTx(ctx, tx, "workagent.channel.closed", "channel", id, nil)
		}
		return nil
	})
	if e != nil {
		return Channel{}, e
	}
	return s.channelDetail(ctx, id)
}

// Channels is the agent-facing surface the MCP module consumes through the
// kernel service registry ("workagents.channels"). It is read/append only:
// agents cannot create channels, manage members or close threads with it,
// and nothing in it touches content, media or settings.
type Channels struct{ s *service }

// OpenChannels wires the bridge action name; List returns this owner's at
// most 100 open channels (ids and titles only — messages need read_channel_messages).
func (c *Channels) List(ctx context.Context) ([]ChannelSummary, error) {
	if !channelReadAllowed(ctx) {
		return nil, ErrChannelDenied
	}
	rows, e := c.s.d.DB.QueryContext(ctx, channelSummarySelect+` WHERE c.owner_id=? AND c.status='open' ORDER BY c.created_at DESC, c.id DESC LIMIT ?`, owner(ctx), channelListLimit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []ChannelSummary{}
	for rows.Next() {
		row, e := scanChannelSummary(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Messages returns the channel's recent messages ascending. limit defaults
// to 20 and is capped at 100; after is a message id cursor.
func (c *Channels) Messages(ctx context.Context, channelID, after string, limit int) ([]ChannelMessage, error) {
	if limit < 1 {
		limit = 20
	}
	if limit > channelListLimit {
		limit = channelListLimit
	}
	msgs, _, e := c.s.channelMessages(ctx, channelID, after, 1, limit)
	return msgs, e
}

// Post appends one message as the calling principal (an MCP API key posts
// as kind agent_key). It must be a member and the channel must be open; a
// non-empty target points an advisory hand-off at a member.
func (c *Channels) Post(ctx context.Context, channelID, body, targetMemberID string) (ChannelMessage, error) {
	return c.s.postChannelMessage(ctx, channelID, body, targetMemberID)
}

// channelHandoff parses a message's advisory meta hand-off, if any.
func channelHandoff(m ChannelMessage) *ChannelHandoff {
	var h struct {
		Type string `json:"type"`
		ChannelHandoff
	}
	if json.Unmarshal(m.Meta, &h) != nil || h.Type != "handoff" {
		return nil
	}
	if h.TargetMemberID == "" {
		return nil
	}
	return &ChannelHandoff{TargetMemberID: h.TargetMemberID, TargetMemberKind: h.TargetMemberKind}
}
