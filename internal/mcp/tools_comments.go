package mcp

import (
	"context"
	"encoding/json"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

const scopeComments = content.PermModerateComments

type listCommentsInput struct {
	Status    string `json:"status,omitempty" jsonschema:"pending, approved, spam or trash"`
	ContentID string `json:"content_id,omitempty"`
	Page      int    `json:"page,omitempty"`
	PerPage   int    `json:"per_page,omitempty" jsonschema:"default 20, max 100"`
}

type moderateCommentInput struct {
	ID     string `json:"id"`
	Status string `json:"status" jsonschema:"pending, approved, spam or trash"`
}

// agentComment is a comment as agents see it: never the commenter's email.
type agentComment struct {
	ID           string `json:"id"`
	ContentID    string `json:"content_id"`
	ContentTitle string `json:"content_title,omitempty"`
	ParentID     string `json:"parent_id,omitempty"`
	AuthorName   string `json:"author_name"`
	Body         string `json:"body"`
	Status       string `json:"status"`
	CreatedAt    string `json:"created_at"`
}

func toAgentComment(c content.Comment) agentComment {
	return agentComment{ID: c.ID, ContentID: c.ContentID, ContentTitle: c.ContentTitle, ParentID: c.ParentID,
		AuthorName: c.AuthorName, Body: c.Body, Status: c.Status, CreatedAt: c.CreatedAt}
}

func commentTools(svc content.CommentService) []kernel.MCPTool {
	return []kernel.MCPTool{
		{Name: "list_comments", Scopes: []string{scopeComments}, Input: listCommentsInput{},
			Description: "List visitor comments, newest first (filter by status and content_id). Commenter emails are not included.",
			Handler: bind(func(ctx context.Context, in listCommentsInput) (any, error) {
				page, per := paging(in.Page, in.PerPage)
				p, err := svc.Comments(ctx, content.CommentQuery{Status: in.Status, ContentID: in.ContentID, Page: page, PerPage: per})
				if err != nil {
					return nil, err
				}
				out := make([]agentComment, 0, len(p.Items))
				for _, c := range p.Items {
					out = append(out, toAgentComment(c))
				}
				return newPage(out, p.Total, p.Page, p.PerPage), nil
			})},
		{Name: "moderate_comment", Scopes: []string{scopeComments}, Input: moderateCommentInput{},
			Description: "Set a comment's status. Approving publishes the visitor's text and needs human approval; spam, trash and pending do not.",
			Handler: bind(func(ctx context.Context, in moderateCommentInput) (any, error) {
				if err := svc.SetCommentStatus(ctx, in.ID, in.Status); err != nil {
					return nil, err
				}
				c, err := svc.GetComment(ctx, in.ID)
				if err != nil {
					return nil, err
				}
				return toAgentComment(*c), nil
			})},
		{Name: "delete_comment", Scopes: []string{scopeComments}, Sensitive: true, Input: idInput{},
			Description: "Delete a comment permanently.",
			Handler: bind(func(ctx context.Context, in idInput) (any, error) {
				if err := svc.DeleteComment(ctx, in.ID); err != nil {
					return nil, err
				}
				return map[string]any{"id": in.ID, "deleted": true}, nil
			})},
	}
}

// approvingComment makes moderate_comment sensitive when it would publish a comment.
func approvingComment(_ context.Context, args json.RawMessage) bool {
	var in moderateCommentInput
	return json.Unmarshal(normalizeArgs(args), &in) == nil && in.Status == content.CommentApproved
}
