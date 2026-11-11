package workagents

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

// REST API and admin screen for agent channels. Channel data is
// owner-scoped: list/detail/message reads need workagents.read, thread
// writes need the workagents.channels scope, and member management plus
// closing need workagents.manage. Nothing here touches content, media or
// provider calls, and message bodies are never copied into audit rows.

const (
	channelBodyLimit      = 1 << 20 // API request bodies, matching the contracts
	channelThreadSchedule = 200     // admin thread view window (oldest first)
)

type channelCreateInput struct {
	Title   string             `json:"title" doc:"1-120 characters; unique per owner"`
	Members []channelMemberAdd `json:"members" doc:"Optional agent_key members; the owner user is always added"`
}
type channelMemberAdd struct {
	Kind     string `json:"kind" enum:"user,agent_key"`
	MemberID string `json:"member_id"`
}
type channelMessageAdd struct {
	Body           string `json:"body" doc:"1-8192 bytes of plain text"`
	TargetMemberID string `json:"target_member_id,omitempty" doc:"Marks an advisory hand-off to this channel member (no execution)"`
}
type channelActionsInput struct{}

// channelsAPI registers REST operations with explicit per-operation scopes
// (the api module enforces auth.Can for each scope before the handler runs).
func (h *handlers) channelsAPI(d *kernel.Deps) {
	errs := []kernel.APIErrorResponse{
		{Status: 401, Code: "unauthorized", Description: "API key or session required"},
		{Status: 403, Code: "forbidden", Description: "Missing channel scope or permission"},
		{Status: 404, Code: "workagents_failed", Description: "Channel not found in this owner scope"},
		{Status: 422, Code: "invalid", Description: "Invalid JSON or oversized request"},
		{Status: 422, Code: "workagents_failed", Description: "Invalid title, body, member or hand-off target (member_not_allowed)"},
		{Status: 422, Code: "workagents_failed", Description: "Closed channels deny messages and members (channel_closed)"},
		{Status: 429, Code: "workagents_failed", Description: "Owner already has 20 open channels"},
	}
	add := func(method, path, summary, scope string, request, response any, status int, fn http.HandlerFunc) {
		d.API.Add(kernel.APIOperation{Method: method, Path: "/v1/workagents/channels" + path, Summary: summary,
			Tags: []string{"workagents"}, Scopes: []string{scope}, Request: request, Response: response,
			Contract: &kernel.APIContract{SuccessStatuses: []int{status}, MaxBodyBytes: channelBodyLimit, Errors: errs},
			Handler: func(w http.ResponseWriter, r *http.Request) {
				if owner(r.Context()) == "" {
					httpx.Error(w, 401, "unauthorized", "active account required", nil)
					return
				}
				fn(w, r)
			}})
	}
	add(http.MethodPost, "", "Create an owner-scoped agent channel", "workagents.channels", channelCreateInput{}, Channel{}, 201, func(w http.ResponseWriter, r *http.Request) {
		var in channelCreateInput
		if !decodeChannel(w, r, &in) {
			return
		}
		members := make([]ChannelMember, 0, len(in.Members))
		for _, m := range in.Members {
			members = append(members, ChannelMember{Kind: m.Kind, MemberID: m.MemberID})
		}
		v, e := h.s.createChannel(r.Context(), in.Title, members)
		if e != nil {
			h.failure(w, e)
			return
		}
		httpx.JSON(w, http.StatusCreated, v, nil)
	})
	add(http.MethodGet, "", "List this owner's channels newest first", "workagents.read", nil, []ChannelSummary{}, 200, func(w http.ResponseWriter, r *http.Request) {
		page, perPage := api.Pagination(r)
		items, total, e := h.s.channelsPage(r.Context(), page, perPage)
		if e != nil {
			h.failure(w, e)
			return
		}
		api.WriteList(w, items, total, page, perPage)
	})
	add(http.MethodPost, "/{id}/members", "Add one of the owner's agent keys as a member", "workagents.manage", channelMemberAdd{}, Channel{}, 201, func(w http.ResponseWriter, r *http.Request) {
		var in channelMemberAdd
		if !decodeChannel(w, r, &in) {
			return
		}
		v, e := h.s.addChannelMember(r.Context(), chi.URLParam(r, "id"), ChannelMember{Kind: in.Kind, MemberID: in.MemberID})
		if e != nil {
			h.failure(w, e)
			return
		}
		httpx.JSON(w, http.StatusCreated, v, nil)
	})
	add(http.MethodGet, "/{id}", "Read channel detail with members", "workagents.read", nil, Channel{}, 200, func(w http.ResponseWriter, r *http.Request) {
		v, e := h.s.channelDetail(r.Context(), chi.URLParam(r, "id"))
		if e != nil {
			h.failure(w, e)
			return
		}
		httpx.JSON(w, http.StatusOK, v, nil)
	})
	add(http.MethodGet, "/{id}/messages", "Read messages ascending, optional after cursor", "workagents.read", nil, []ChannelMessage{}, 200, func(w http.ResponseWriter, r *http.Request) {
		page, perPage := api.Pagination(r)
		items, total, e := h.s.channelMessages(r.Context(), chi.URLParam(r, "id"), r.URL.Query().Get("after"), page, perPage)
		if e != nil {
			h.failure(w, e)
			return
		}
		api.WriteList(w, items, total, page, perPage)
	})
	add(http.MethodPost, "/{id}/messages", "Post a message (optional advisory hand-off target)", "workagents.channels", channelMessageAdd{}, ChannelMessage{}, 201, func(w http.ResponseWriter, r *http.Request) {
		var in channelMessageAdd
		if !decodeChannel(w, r, &in) {
			return
		}
		m, e := h.s.postChannelMessage(r.Context(), chi.URLParam(r, "id"), in.Body, in.TargetMemberID)
		if e != nil {
			h.failure(w, e)
			return
		}
		httpx.JSON(w, http.StatusCreated, m, nil)
	})
	add(http.MethodPost, "/{id}/close", "Close the channel: reads keep working, posts stop", "workagents.manage", channelActionsInput{}, Channel{}, 200, func(w http.ResponseWriter, r *http.Request) {
		v, e := h.s.closeChannel(r.Context(), chi.URLParam(r, "id"))
		if e != nil {
			h.failure(w, e)
			return
		}
		httpx.JSON(w, http.StatusOK, v, nil)
	})
}

// decodeChannel is the module's decode with the channels body limit (1 MiB,
// matching the declared contracts) and strict unknown-field rejection.
func decodeChannel(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, channelBodyLimit))
	dec.DisallowUnknownFields()
	if dec.Decode(v) != nil || dec.Decode(new(struct{})) != io.EOF {
		httpx.Error(w, 422, "invalid", "invalid or oversized JSON", nil)
		return false
	}
	return true
}

func (h *handlers) registerChannels(d *kernel.Deps) {
	d.Admin.AddNav(kernel.NavItem{ID: "workagents.channels", LabelKey: "workagents.channels.title", Icon: "message-square",
		Href: "/admin/workagents/channels", Permission: "workagents.manage", Group: "system", Position: 30})
	d.Router.Admin.With(auth.RequirePerm("workagents.manage")).Get("/workagents/channels", h.channelsPage)
	d.Router.Admin.With(auth.RequirePerm("workagents.manage")).Get("/workagents/channels/{id}", h.channelThreadPage)
	d.Router.Admin.With(auth.RequirePerm("workagents.channels")).Post("/workagents/channels/create", h.channelCreateForm)
	d.Router.Admin.With(auth.RequirePerm("workagents.channels")).Post("/workagents/channels/{id}/messages", h.channelMessageForm)
	d.Router.Admin.With(auth.RequirePerm("workagents.manage")).Post("/workagents/channels/{id}/members", h.channelMemberForm)
	d.Router.Admin.With(auth.RequirePerm("workagents.manage")).Post("/workagents/channels/{id}/close", h.channelCloseForm)
	h.channelsAPI(d)
}

// ---- Admin screen ---------------------------------------------------------

type channelAgentKeyOption struct {
	ID, Name string
}

type channelMessageView struct {
	ChannelMessage
	// HandoffTarget is the member's display name (or raw member id when the
	// member row is gone); empty when the message carries no hand-off.
	HandoffTarget string
}

type channelsPageData struct {
	Channels  []ChannelSummary
	AgentKeys []channelAgentKeyOption
}

type channelThreadPageData struct {
	Channel  Channel
	Messages []channelMessageView
	// AgentKeys are the owner's live agent keys; AvailableKeys are the ones
	// not in the channel yet (the add-member choices).
	AgentKeys     []channelAgentKeyOption
	AvailableKeys []channelAgentKeyOption
	CanClose      bool
	CanPost       bool
}

// nonMemberKeys returns the agent keys that are not channel members yet.
func nonMemberKeys(keys []channelAgentKeyOption, members []ChannelMember) []channelAgentKeyOption {
	in := map[string]bool{}
	for _, m := range members {
		if m.Kind == "agent_key" {
			in[m.MemberID] = true
		}
	}
	var out []channelAgentKeyOption
	for _, k := range keys {
		if !in[k.ID] {
			out = append(out, k)
		}
	}
	return out
}

// channelsPage is the channel list with a create form (own agent keys are
// selectable from the account's live agent-key IDs).
func (h *handlers) channelsPage(w http.ResponseWriter, r *http.Request) {
	channels, _, e := h.s.channelsPage(r.Context(), 1, channelListLimit)
	if e != nil {
		h.failure(w, e)
		return
	}
	data := channelsPageData{Channels: channels, AgentKeys: h.s.ownerAgentKeys(r.Context())}
	w.Header().Set("Cache-Control", "no-store")
	h.adminTpl.Render(w, r, "pages/workagents/channels.html", data)
}

// channelThreadPage renders the messages and the human post box.
func (h *handlers) channelThreadPage(w http.ResponseWriter, r *http.Request) {
	c, e := h.s.channelDetail(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		h.failure(w, e)
		return
	}
	msgs, _, e := h.s.channelMessages(r.Context(), c.ID, "", 1, channelThreadSchedule)
	if e != nil {
		h.failure(w, e)
		return
	}
	keys := h.s.ownerAgentKeys(r.Context())
	data := channelThreadPageData{Channel: c, Messages: messageViews(c.Members, msgs), AgentKeys: keys, AvailableKeys: nonMemberKeys(keys, c.Members)}
	author, ok := h.s.postAuthor(r.Context())
	data.CanPost = ok && c.Status == "open" && h.s.isChannelMember(r.Context(), c.ID, author.Kind, author.ID)
	data.CanClose = channelManageAllowed(r.Context()) && c.Status == "open"
	w.Header().Set("Cache-Control", "no-store")
	h.adminTpl.Render(w, r, "pages/workagents/channel_thread.html", data)
}

// messageViews resolves hand-off targets to their member display name.
func messageViews(members []ChannelMember, msgs []ChannelMessage) []channelMessageView {
	name := map[string]string{}
	for _, m := range members {
		name[m.MemberID] = m.DisplayName
	}
	out := make([]channelMessageView, 0, len(msgs))
	for _, m := range msgs {
		v := channelMessageView{ChannelMessage: m}
		if h := channelHandoff(m); h != nil {
			if n := name[h.TargetMemberID]; n != "" {
				v.HandoffTarget = n
			} else {
				v.HandoffTarget = h.TargetMemberID
			}
		}
		out = append(out, v)
	}
	return out
}

func (h *handlers) channelCreateForm(w http.ResponseWriter, r *http.Request) {
	if e := r.ParseForm(); e != nil {
		h.failure(w, errInvalid)
		return
	}
	var members []ChannelMember
	for _, id := range r.Form["agent_keys"] {
		members = append(members, ChannelMember{Kind: "agent_key", MemberID: id})
	}
	v, e := h.s.createChannel(r.Context(), r.FormValue("title"), members)
	if e != nil {
		h.failure(w, e)
		return
	}
	http.Redirect(w, r, "/admin/workagents/channels/"+v.ID, http.StatusSeeOther)
}

func (h *handlers) channelMemberForm(w http.ResponseWriter, r *http.Request) {
	if e := r.ParseForm(); e != nil {
		h.failure(w, errInvalid)
		return
	}
	id := chi.URLParam(r, "id")
	if _, e := h.s.addChannelMember(r.Context(), id, ChannelMember{Kind: r.FormValue("kind"), MemberID: r.FormValue("member_id")}); e != nil {
		h.failure(w, e)
		return
	}
	http.Redirect(w, r, "/admin/workagents/channels/"+id, http.StatusSeeOther)
}

func (h *handlers) channelMessageForm(w http.ResponseWriter, r *http.Request) {
	if e := r.ParseForm(); e != nil {
		h.failure(w, errInvalid)
		return
	}
	id := chi.URLParam(r, "id")
	if _, e := h.s.postChannelMessage(r.Context(), id, r.FormValue("body"), r.FormValue("target_member_id")); e != nil {
		h.failure(w, e)
		return
	}
	http.Redirect(w, r, "/admin/workagents/channels/"+id, http.StatusSeeOther)
}

func (h *handlers) channelCloseForm(w http.ResponseWriter, r *http.Request) {
	if e := r.ParseForm(); e != nil {
		h.failure(w, errInvalid)
		return
	}
	id := chi.URLParam(r, "id")
	if _, e := h.s.closeChannel(r.Context(), id); e != nil {
		h.failure(w, e)
		return
	}
	http.Redirect(w, r, "/admin/workagents/channels", http.StatusSeeOther)
}

// ownerAgentKeys lists the owner's live agent keys for the member dropdown.
func (s *service) ownerAgentKeys(ctx context.Context) []channelAgentKeyOption {
	if !channelManageAllowed(ctx) {
		return nil
	}
	keys, ok := kernel.Get[auth.KeyService](s.d, kernel.ServiceAPIKeys)
	if !ok {
		return nil
	}
	all, e := keys.List(ctx)
	if e != nil {
		return nil
	}
	var out []channelAgentKeyOption
	for _, k := range all {
		if k.Kind != auth.KeyKindAgent || k.UserID != owner(ctx) || k.RevokedAt != "" {
			continue
		}
		if k.ExpiresAt != "" {
			exp, pe := db.ParseTime(k.ExpiresAt)
			if pe != nil || !time.Now().Before(exp) {
				continue
			}
		}
		out = append(out, channelAgentKeyOption{ID: k.ID, Name: k.Name})
	}
	return out
}
