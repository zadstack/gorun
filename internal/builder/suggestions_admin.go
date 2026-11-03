package builder

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/kernel"
)

const layoutAdvisorService = "builder.layout-advisor"
const suggestionBodyLimit = 64 << 10
const layoutSuggestionTimeout = 20 * time.Second

// The later admin module supplies the configured provider without making AI a
// boot dependency of the builder. No content or URL belongs in its input.
type layoutAdvisor interface {
	DescribeLayout(context.Context) (string, string, error)
	SuggestLayout(context.Context, string, string) (string, error)
}

type suggestionRequest struct {
	Section   Section `json:"section"`
	Mode      string  `json:"mode"`
	AIBinding string  `json:"ai_binding,omitempty"`
}
type suggestionResponse struct {
	Source        string             `json:"source"`
	Candidates    []layoutSuggestion `json:"candidates"`
	AIDestination string             `json:"ai_destination,omitempty"`
	AIBinding     string             `json:"ai_binding,omitempty"`
}

func decodeSuggestion(w http.ResponseWriter, r *http.Request) (suggestionRequest, error) {
	var input suggestionRequest
	r.Body = http.MaxBytesReader(w, r.Body, suggestionBodyLimit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil {
		return input, err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return input, errors.New("extra suggestion input")
	}
	if input.Mode != "local" && input.Mode != "ai" {
		return input, errors.New("unsupported suggestion mode")
	}
	return input, Validate(Document{Version: 1, Sections: []Section{input.Section}})
}
func (h *adminHandlers) suggest(w http.ResponseWriter, r *http.Request) {
	if _, err := h.svc.editable(r.Context(), chi.URLParam(r, "id")); err != nil {
		h.fail(w, r, err)
		return
	}
	input, err := decodeSuggestion(w, r)
	if err != nil {
		var oversized *http.MaxBytesError
		status := 400
		if errors.As(err, &oversized) {
			status = 413
		}
		builderJSON(w, status, map[string]string{"error": "suggest_invalid"})
		return
	}
	response := suggestionResponse{Source: "local", Candidates: suggestLayouts(input.Section)}
	advisor, available := kernel.Get[layoutAdvisor](h.d, layoutAdvisorService)
	if input.Mode == "local" {
		if available {
			destination, binding, err := advisor.DescribeLayout(r.Context())
			if err == nil {
				response.AIDestination, response.AIBinding = destination, binding
			}
		}
		builderJSON(w, 200, response)
		return
	}
	if !available || input.AIBinding == "" {
		builderJSON(w, 503, map[string]string{"error": "suggest_ai_unavailable"})
		return
	}
	if len(response.Candidates) == 0 {
		builderJSON(w, 422, map[string]string{"error": "suggest_empty"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), layoutSuggestionTimeout)
	defer cancel()
	metadata, err := suggestionMetadata(input.Section, response.Candidates)
	if err != nil {
		builderJSON(w, 500, map[string]string{"error": "suggest_invalid"})
		return
	}
	output, err := advisor.SuggestLayout(ctx, input.AIBinding, metadata)
	if err != nil {
		builderJSON(w, 503, map[string]string{"error": "suggest_ai_unavailable"})
		return
	}
	selected, ok := chosenSuggestion(output, response.Candidates)
	if !ok {
		builderJSON(w, 502, map[string]string{"error": "suggest_ai_invalid"})
		return
	}
	response.Source = "ai"
	response.Candidates = []layoutSuggestion{selected}
	builderJSON(w, 200, response)
}
