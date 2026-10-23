package identitylogo

import (
	"context"
	"errors"
	"testing"
)

// DraftStore is the media-import/CAS boundary. Invalid client revisions must
// not reach it, even when the caller supplies the current revision of a newer
// draft to try to rebase a candidate approved against an older draft.
type selectionBoundary struct {
	*memoryDraft
	calls int
}

func (d *selectionBoundary) SelectPNG(ctx context.Context, theme, baseline, slot string, data []byte) error {
	d.calls++
	return d.memoryDraft.SelectPNG(ctx, theme, baseline, slot, data)
}

func TestCandidateSelectionRequiresOriginalClientBindingNotLatestDraftRevision(t *testing.T) {
	for _, scenario := range []string{"missing", "different", "whitespace", "newer-current-draft"} {
		t.Run(scenario, func(t *testing.T) {
			s, d, _ := newTestService(t)
			boundary := &selectionBoundary{memoryDraft: d}
			s.drafts = boundary
			ctx := context.Background()
			review := preview(t, s, "owner")
			candidate, err := s.Generate(ctx, "owner", review.Token, true)
			if err != nil {
				t.Fatal(err)
			}
			supplied := ""
			switch scenario {
			case "different":
				supplied = "unreviewed-revision"
			case "whitespace":
				supplied = candidate.Revision + " "
			case "newer-current-draft":
				d.revision = "new-human-draft"
				supplied, err = boundary.Revision(ctx, candidate.Theme)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Select(ctx, "owner", candidate.ID, "site.logo", supplied); !errors.Is(err, ErrChanged) {
				t.Fatalf("revision %q accepted: %v", supplied, err)
			}
			if boundary.calls != 0 || d.writes != 0 {
				t.Fatalf("unreviewed revision reached import boundary: calls=%d writes=%d", boundary.calls, d.writes)
			}
			retained, _, err := s.Candidate(ctx, "owner", candidate.ID)
			if err != nil || retained.Revision != candidate.Revision {
				t.Fatalf("rejection changed private candidate: %+v %v", retained, err)
			}
			// Rejection must not consume the proposal. Its original client token stays
			// usable even when an unrelated newer draft revision remains current.
			if err := s.Select(ctx, "owner", candidate.ID, "site.logo", candidate.Revision); err != nil {
				t.Fatal(err)
			}
			if boundary.calls != 1 || d.writes != 1 {
				t.Fatalf("valid original revision failed: calls=%d writes=%d", boundary.calls, d.writes)
			}
		})
	}
}
