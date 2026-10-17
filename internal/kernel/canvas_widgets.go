package kernel

import (
	"fmt"
	"net/http"
	"slices"
)

// CanvasWidget is an optional module-owned read-only projection. The admin
// validates board ownership, permission and visible navigation before Load.
type CanvasWidget struct {
	ID, Href, Permission, NavID string
	Load                        func(*http.Request) (CanvasWidgetData, error)
}
type CanvasMetric struct {
	Label string `json:"label"`
	Value string `json:"value"`
}
type CanvasWidgetRow struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Href   string `json:"href"`
}
type CanvasWidgetData struct {
	ID        string            `json:"id"`
	Title     string            `json:"title"`
	Href      string            `json:"href"`
	Metrics   []CanvasMetric    `json:"metrics"`
	Rows      []CanvasWidgetRow `json:"rows"`
	UpdatedAt string            `json:"updated_at"`
}

func (a *AdminRegistry) AddCanvasWidget(widget CanvasWidget) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if widget.ID == "" || widget.Load == nil || widget.Permission == "" || widget.Href == "" {
		panic("kernel: incomplete canvas widget")
	}
	if slices.ContainsFunc(a.canvas, func(w CanvasWidget) bool { return w.ID == widget.ID }) {
		panic(fmt.Sprintf("kernel: duplicate canvas widget %q", widget.ID))
	}
	a.canvas = append(a.canvas, widget)
}
func (a *AdminRegistry) CanvasWidgets() []CanvasWidget {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]CanvasWidget(nil), a.canvas...)
}
