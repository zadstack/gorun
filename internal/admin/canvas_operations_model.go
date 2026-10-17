package admin

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
)

var errCanvasConsent = errors.New("canvas: consent changed")

func canvasInterval(s string) time.Duration {
	return map[string]time.Duration{"hourly": time.Hour, "daily": 24 * time.Hour, "weekly": 168 * time.Hour}[s]
}
func validateCanvasEdges(cards []canvasCard, edges []canvasEdge) error {
	if len(edges) > 200 {
		return errCanvasInvalid
	}
	nodes := map[string]canvasCard{}
	for _, c := range cards {
		nodes[c.ID] = c
	}
	seen := map[canvasEdge]bool{}
	schedules := map[string]bool{}
	for _, e := range edges {
		a, aok := nodes[e.From]
		b, bok := nodes[e.To]
		if !aok || !bok || e.From == e.To || seen[e] || !slices.Contains([]string{"provider", "agent"}, b.Kind) || !slices.Contains([]string{"source", "content", "prompt", "note", "schedule"}, a.Kind) {
			return errCanvasInvalid
		}
		if a.Kind == "schedule" {
			if schedules[a.ID] {
				return errCanvasInvalid
			}
			schedules[a.ID] = true
		}
		seen[e] = true
	}
	return nil
}

type canvasPrepared struct {
	AgentModel   *agentModelConfig `json:"agent_model,omitempty"`
	SourceTokens map[string]string `json:"source_tokens,omitempty"`
	RemoteJob    string            `json:"remote_job,omitempty"`
	ScheduleID   string            `json:"schedule_id,omitempty"`
	BoardID      string            `json:"board_id"`
	OwnerID      string            `json:"owner_id"`
	NodeID       string            `json:"node_id"`
	Version      int64             `json:"version"`
	MediaSize    int64             `json:"media_size,omitempty"`
	MediaMIME    string            `json:"media_mime,omitempty"`
	MediaFile    string            `json:"media_file,omitempty"`
	Candidate    studioCandidate   `json:"candidate"`
	KeyID        string            `json:"key_id,omitempty"`
	Context      string            `json:"context"`
	Prompt       string            `json:"prompt"`
	GraphHash    string            `json:"graph_hash"`
	ReviewHash   string            `json:"review_hash"`
	Provider     map[string]string `json:"provider"`
	Nodes        []string          `json:"nodes"`
}

func (h *handlers) canvasPrepare(ctx context.Context, b *canvasBoard, nodeID string) (canvasPrepared, error) {
	p := canvasPrepared{BoardID: b.ID, OwnerID: auth.UserFromContext(ctx).ID, NodeID: nodeID, Version: b.Version, SourceTokens: map[string]string{}}
	if !auth.Can(auth.WithUser(ctx, auth.UserFromContext(ctx)), "content.edit") || auth.ActorFromContext(ctx).Kind != "api_key" && !auth.Can(ctx, "content.edit") || auth.ActorFromContext(ctx).Kind == "api_key" && !auth.Can(ctx, "content.view") {
		return p, errCanvasConsent
	}
	var target *canvasCard
	for i := range b.Cards {
		if b.Cards[i].ID == nodeID {
			target = &b.Cards[i]
		}
	}
	if target == nil || !slices.Contains([]string{"provider", "agent"}, target.Kind) {
		return p, errCanvasInvalid
	}
	connected := map[string]bool{}
	for _, e := range b.Edges {
		if e.To == nodeID {
			connected[e.From] = true
		}
	}
	graph := []canvasCard{}
	source := []string{}
	prompts := []string{}
	for _, c := range b.Cards {
		if c.ID != nodeID && !connected[c.ID] {
			continue
		}
		copy := c
		copy.X = 0
		copy.Y = 0
		graph = append(graph, copy)
		if slices.Contains([]string{"source", "content", "prompt", "note"}, c.Kind) {
			p.Nodes = append(p.Nodes, c.ID)
		}
		switch c.Kind {
		case "prompt":
			prompts = append(prompts, c.Body)
		case "note":
			source = append(source, c.Title+"\n"+c.Body)
		case "content":
			text, token, e := h.canvasSourceText(ctx, c.ContentID, "")
			if e != nil {
				return p, e
			}
			source = append(source, text)
			p.SourceTokens[c.ContentID] = token
		case "source":
			cs, ok := h.contentService()
			if !ok {
				return p, errCanvasInvalid
			}
			ids := c.SourceIDs
			typ, e := cs.GetType(ctx, c.SourceType)
			if e != nil || !typ.Public {
				return p, errCanvasConsent
			}
			if len(ids) == 0 {
				q := content.Query{Type: c.SourceType, PerPage: c.SourceLimit, PublicOnly: true, Order: []content.Order{{By: "updated_at", Dir: "desc"}}}
				u := auth.UserFromContext(ctx)
				if u.Role == "author" || u.Role == "contributor" {
					q.AuthorID = u.ID
				}
				result, e := cs.Query(ctx, q)
				if e != nil {
					return p, e
				}
				for _, it := range result.Items {
					ids = append(ids, it.ID)
				}
			}
			for _, id := range ids {
				text, token, e := h.canvasSourceText(ctx, id, c.SourceType)
				if e != nil {
					return p, e
				}
				source = append(source, text)
				p.SourceTokens[id] = token
			}
		}
	}
	p.Prompt = strings.Join(prompts, "\n\n")
	p.Context = strings.Join(source, "\n\n")
	if strings.TrimSpace(p.Prompt) == "" || len(p.Prompt) > 16000 || len(p.Context) > 64000 {
		return p, errCanvasInvalid
	}
	kind := target.GenerationKind
	if kind == "" {
		kind = "text"
	}
	if kind != "text" && !auth.Can(ctx, "media.upload") {
		return p, errCanvasConsent
	}
	if target.PresetID != "" {
		cfg, e := h.studioConfig(ctx)
		if e != nil {
			return p, e
		}
		found := false
		for _, preset := range cfg.Presets {
			if preset.ID == target.PresetID && preset.Kind == kind {
				p.Prompt = preset.Prompt + "\n\n" + p.Prompt
				found = true
			}
		}
		if !found {
			return p, errCanvasConsent
		}
	}
	p.Candidate = studioCandidate{Input: studioInput{Kind: kind, Provider: target.ProviderID, Prompt: p.Prompt, Text: p.Context, Title: target.Title}}
	if target.Kind == "agent" {
		if _, e := h.canvasAgentKey(ctx, target.AgentID); e != nil {
			return p, e
		}
		p.KeyID = target.AgentID
		model, e := h.loadAgentModel(ctx, target.AgentID)
		if e != nil {
			return p, e
		}
		p.AgentModel = &model
		if model.Mode == "managed" && kind != "text" {
			return p, errCanvasInvalid
		}
		p.Provider = map[string]string{"id": target.AgentID, "title": target.Title, "endpoint": "Named agent key"}
		if model.ConnectionID != "" {
			conn, e := h.loadConnection(ctx, model.ConnectionID)
			if e != nil || conn.Version != model.ConnectionVersion || conn.Kind != "ai" {
				return p, errCanvasConsent
			}
			p.Provider["model"] = model.Model
			p.Provider["mode"] = model.Mode
			if model.Mode == "managed" {
				p.Provider["endpoint"] = conn.Endpoint
				if _, e := h.agentModelRuntime(ctx, p); e != nil {
					return p, e
				}
			}
		}

		if !h.canvasGrantAllows(ctx, b.ID, p.KeyID, "run", p.Nodes) {
			return p, errCanvasConsent
		}
	} else if target.ProviderID == "native-text" || target.ProviderID == "native-image" {
		cfg, e := h.loadAIConfig(ctx)
		if e != nil {
			return p, errStudio
		}
		model := cfg.Model
		if target.ProviderID == "native-image" {
			sc, e := h.studioConfig(ctx)
			if e != nil || kind != "image" || cfg.Provider != AIProviderOpenAI || sc.ImageModel == "" {
				return p, errStudio
			}
			model = sc.ImageModel
		} else if kind != "text" {
			return p, errStudio
		}
		p.Candidate.Native = &studioNative{cfg.Provider, cfg.BaseURL, model}
		p.Provider = map[string]string{"id": target.ProviderID, "title": model, "endpoint": cfg.BaseURL}
	} else {
		cfg, e := h.studioConfig(ctx)
		if e != nil {
			return p, e
		}
		for _, binding := range cfg.Bindings {
			if binding.ID != target.ProviderID || binding.Kind != kind {
				continue
			}
			conn, e := h.loadConnection(ctx, binding.ConnectionID)
			if e != nil || conn.Version != binding.ConnectionVersion || conn.Endpoint != binding.Endpoint {
				return p, errStudio
			}
			p.Candidate.Binding = &binding
			p.Provider = map[string]string{"id": binding.ID, "title": binding.Name, "endpoint": binding.Endpoint}
			break
		}
		if p.Candidate.Binding == nil {
			return p, errStudio
		}
	}
	// Recurring consent binds selection, prompt and destination; current source text may evolve.
	hashInput := map[string]any{"nodes": graph, "provider": p.Provider, "binding": p.Candidate.Binding, "native": p.Candidate.Native, "prompt": p.Prompt}
	if p.AgentModel != nil {
		hashInput["agent_model"] = p.AgentModel
	}
	p.GraphHash = studioReviewHash(hashInput)
	p.ReviewHash = studioReviewHash(map[string]any{"graph": p.GraphHash, "source": p.Context, "versions": p.SourceTokens})
	return p, nil
}
func (h *handlers) canvasSourceText(ctx context.Context, id, typ string) (string, string, error) {
	if _, ok := h.canvasReference(ctx, id); !ok {
		return "", "", errCanvasConsent
	}
	cs, _ := h.contentService()
	it, e := cs.Get(ctx, id)
	if e != nil || typ != "" && it.Type != typ {
		return "", "", errCanvasConsent
	}
	text := plainText.Sanitize(it.Title + "\n" + it.Body)
	if len(text) > 12000 {
		text = string([]rune(text)[:min(3000, len([]rune(text)))])
	}
	return text, content.EditToken(it), nil
}
func canvasEncode(v any) string { b, _ := json.Marshal(v); return string(b) }
