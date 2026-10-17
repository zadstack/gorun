package wpmigrate

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/zadstack/gorun/internal/httpx"
)

// Wizard steps 6–7: live import progress, controls, validation report.

type kindBar struct {
	Kind                                      string
	Total, Done, Failed, Skipped, Pending, Pc int
}

// progressPage fills the import/progress data.
func (w *wizard) progressPage(r *http.Request, v *MigrationView) (page, error) {
	st, err := w.s.Status(r.Context(), v.ID)
	if err != nil {
		return page{}, err
	}
	logs, err := w.s.Log(r.Context(), v.ID, 15)
	if err != nil {
		return page{}, err
	}
	p := page{Steps: w.steps(v.ID, "import", v.Status), M: v, Status: st, Log: logs}
	for _, k := range []string{"user", "term", "media", "content", "product", "menu", "redirect", "rewrite"} {
		c := st.Counts[k]
		if len(c) == 0 {
			continue
		}
		b := kindBar{Kind: k, Done: c[ItemDone] + c[ItemPreview], Failed: c[ItemFailed], Skipped: c[ItemSkipped], Pending: c[ItemPending]}
		b.Total = b.Done + b.Failed + b.Skipped + b.Pending
		if b.Total > 0 {
			b.Pc = (b.Total - b.Pending) * 100 / b.Total
		}
		p.Kinds = append(p.Kinds, b)
	}
	p.Polling = st.Status == StatusRunning
	p.CanPause = st.Status == StatusRunning
	p.CanResume = st.Status == StatusPaused
	p.CanRetry = st.Failed > 0 && st.Status != StatusRunning
	p.CanStart = st.Status != StatusRunning && st.Status != StatusPaused
	return p, nil
}

func (w *wizard) importPage(rw http.ResponseWriter, r *http.Request) {
	v, _, ok := w.load(rw, r)
	if !ok {
		return
	}
	p, err := w.progressPage(r, v)
	if err != nil {
		w.fail(rw, r, err)
		return
	}
	w.render(rw, r, http.StatusOK, "wizard_import", p)
}

func (w *wizard) progress(rw http.ResponseWriter, r *http.Request) {
	v, _, ok := w.load(rw, r)
	if !ok {
		return
	}
	p, err := w.progressPage(r, v)
	if err != nil {
		w.fail(rw, r, err)
		return
	}
	w.tpl.RenderWith(rw, r, http.StatusOK, "", "pages/migrations/progress.html", p)
}

func (w *wizard) control(action string) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) { w.runControl(rw, r, action) }
}

func (w *wizard) runControl(rw http.ResponseWriter, r *http.Request, action string) {
	v, _, ok := w.load(rw, r)
	if !ok {
		return
	}
	var err error
	switch action {
	case "start":
		err = w.s.Start(r.Context(), v.ID)
	case "pause":
		err = w.s.Pause(r.Context(), v.ID)
	case "resume":
		err = w.s.Resume(r.Context(), v.ID)
	case "retry":
		err = w.s.RetryFailed(r.Context(), v.ID)
	}
	if err != nil && !errors.Is(err, ErrBadState) {
		if errors.Is(err, ErrAuthFailed) || errors.Is(err, ErrUnreachable) {
			httpx.SetFlash(rw, "error", "wpmigrate.scan.unreachable")
			http.Redirect(rw, r, "/admin/migrations/"+v.ID+"/import", http.StatusSeeOther)
			return
		}
		w.fail(rw, r, err)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		v, _ = w.s.Migration(r.Context(), v.ID)
		p, perr := w.progressPage(r, v)
		if perr != nil {
			w.fail(rw, r, perr)
			return
		}
		w.tpl.RenderWith(rw, r, http.StatusOK, "", "pages/migrations/progress.html", p)
		return
	}
	http.Redirect(rw, r, "/admin/migrations/"+v.ID+"/import", http.StatusSeeOther)
}

func (w *wizard) validatePage(rw http.ResponseWriter, r *http.Request) {
	v, _, ok := w.load(rw, r)
	if !ok {
		return
	}
	p := page{Steps: w.steps(v.ID, "validate", v.Status), M: v}
	p.Validate, _ = w.s.report(r.Context(), v.ID)
	w.render(rw, r, http.StatusOK, "wizard_validate", p)
}

func (w *wizard) validate(rw http.ResponseWriter, r *http.Request) {
	v, _, ok := w.load(rw, r)
	if !ok {
		return
	}
	rep, err := w.s.Validate(r.Context(), v.ID)
	if err != nil {
		w.fail(rw, r, err)
		return
	}
	w.render(rw, r, http.StatusOK, "wizard_validate", page{Steps: w.steps(v.ID, "validate", v.Status), M: v, Validate: rep})
}

func (w *wizard) reportMarkdown(rw http.ResponseWriter, r *http.Request) {
	v, _, ok := w.load(rw, r)
	if !ok {
		return
	}
	rep, err := w.s.report(r.Context(), v.ID)
	if errors.Is(err, ErrNotFound) {
		w.tpl.RenderError(rw, r, http.StatusNotFound)
		return
	}
	if err != nil {
		w.fail(rw, r, err)
		return
	}
	rw.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	rw.Header().Set("Content-Disposition", `attachment; filename="migration-`+strings.ToLower(v.ID)+`.md"`)
	_, _ = io.WriteString(rw, rep.Markdown)
}
