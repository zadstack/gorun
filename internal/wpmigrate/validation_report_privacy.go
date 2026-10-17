package wpmigrate

func (r *ValidateReport) sanitizeStoredReferences() {
	for i := range r.BrokenLinks {
		r.BrokenLinks[i].URL = safeSourceReference(r.BrokenLinks[i].URL)
	}
	for i := range r.MissingMedia {
		r.MissingMedia[i].URL = safeSourceReference(r.MissingMedia[i].URL)
	}
	for _, issues := range [][]ValidationIssue{r.Errors, r.Warnings} {
		for i := range issues {
			issue := &issues[i]
			switch issue.Kind {
			case "render", "import":
				issue.OldRef = safeSourceReference(issue.OldRef)
				issue.NewRef = safeReference(issue.NewRef)
			case "shortcode", "block", "slug":
				issue.OldRef = safeReference(issue.OldRef)
				issue.NewRef = safeReference(issue.NewRef)
			default:
				issue.OldRef = safeSourceReference(issue.OldRef)
				issue.NewRef = safeSourceReference(issue.NewRef)
			}
		}
	}
	for i := range r.UnverifiedChecks {
		issue := &r.UnverifiedChecks[i]
		if issue.Kind == "render" {
			issue.Path = safeReference(issue.Path)
			issue.OldRef = safeSourceReference(issue.OldRef)
			issue.NewRef = safeReference(issue.NewRef)
		} else {
			issue.Path = safeSourceReference(issue.Path)
			issue.OldRef = safeSourceReference(issue.OldRef)
			issue.NewRef = safeSourceReference(issue.NewRef)
		}
	}
	for i := range r.RenderFailures {
		r.RenderFailures[i].Path = safeReference(r.RenderFailures[i].Path)
	}
	for i := range r.SlugChanges {
		r.SlugChanges[i].Path = safeReference(r.SlugChanges[i].Path)
	}
	if r.Readiness == "" || len(r.Scope) == 0 {
		r.Readiness = "unverified"
		r.Scope = []string{"stored_legacy_report"}
		r.Limitations = append(r.Limitations, "legacy_report_checks_not_recorded")
		r.UnverifiedChecks = append(r.UnverifiedChecks, CheckIssue{Kind: "report", Code: "legacy_report_checks_not_recorded", Reason: "legacy_report_checks_not_recorded", Outcome: "skipped", CheckedAt: r.GeneratedAt})
	}
	// Cached Markdown may predate reference masking; rebuild from safe fields.
	r.Markdown = r.markdown()
}
