package theme

import (
	"slices"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

// maxShownComments caps the comments a page renders.
const maxShownComments = 200

// CommentsData is what a comments component renders for an item. It never holds a
// commenter's email.
type CommentsData struct {
	Open      bool   // the item takes new comments
	ContentID string // posted as content_id
	Action    string // the form's action URL
	Back      string // posted as _back: the item's path (with its locale prefix)
	Count     int
	Items     []CommentNode // approved top-level comments, oldest first, with their replies
}

// CommentNode is one approved comment.
type CommentNode struct {
	ID, AuthorName, Body, CreatedAt string
	Replies                         []CommentNode
}

// comments returns the comments of an item whose type supports them, or nil (no section).
func (f *funcs) comments(v any) *CommentsData {
	it := itemOf(v)
	svc, ok := kernel.Get[content.CommentService](f.s.d, content.ServiceComments)
	cs, ok2 := kernel.Get[content.Service](f.s.d, kernel.ServiceContent)
	if it == nil || !ok || !ok2 || (!content.PubliclyReadable(f.st.ctx, cs, it.ID) && !content.DirectPageReadAllowed(f.st.ctx, it.ID)) {
		return nil
	}
	t, err := cs.GetType(f.st.ctx, it.Type)
	if err != nil || !t.Public || !slices.Contains(t.Supports, "comments") {
		return nil
	}
	d := &CommentsData{Open: it.Status == "published", ContentID: it.ID, Action: f.st.tc.Site.BaseURL + "/forms/comment",
		Back: f.itemPath(it)}
	list, err := svc.ApprovedComments(f.st.ctx, it.ID, maxShownComments)
	if err != nil {
		f.s.log.Error("theme: comments", "id", it.ID, "err", err)
		return d
	}
	d.Count = len(list)
	index := map[string]int{}
	for _, c := range list {
		n := CommentNode{ID: c.ID, AuthorName: c.AuthorName, Body: c.Body, CreatedAt: c.CreatedAt}
		if i, ok := index[c.ParentID]; ok && c.ParentID != "" {
			d.Items[i].Replies = append(d.Items[i].Replies, n)
			continue
		}
		if c.ParentID == "" {
			index[c.ID] = len(d.Items)
			d.Items = append(d.Items, n)
		}
	}
	return d
}
