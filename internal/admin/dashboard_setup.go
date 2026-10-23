package admin

import (
	"context"
	"html/template"
	"net/http"
)

func (h *handlers) widgetSetupStatus(ctx context.Context, r *http.Request) (template.HTML, error) {
	if !can(r, PermSettings) {
		return "", nil
	}
	view, err := h.onboardingState(ctx, r)
	if err != nil {
		return "", err
	}
	return h.fragment(r, "pages/system/widgets/setup_status.html", view)
}
