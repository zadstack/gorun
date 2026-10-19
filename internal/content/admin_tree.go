package content

// contentTreeNode only contains rows already authorized by the list query.
// Parents outside the filtered/bounded set stay outside it; no ancestor fetch
// can reveal another author's page. Cycles and excessive nesting become roots.
type contentTreeNode struct {
	Row                      listRow
	Children                 []*contentTreeNode
	CanSelect, ParentMissing bool
	Path                     string
}

func contentTree(rows []listRow, canSelect bool) []*contentTreeNode {
	nodes := make(map[string]*contentTreeNode, len(rows))
	for _, row := range rows {
		nodes[row.ID] = &contentTreeNode{Row: row, CanSelect: canSelect}
	}
	roots := []*contentTreeNode{}
	for _, row := range rows {
		node := nodes[row.ID]
		parent := nodes[row.ParentID]
		seen := map[string]bool{row.ID: true}
		parts := []string{}
		cursor := row.ParentID
		valid := true
		for cursor != "" {
			ancestor := nodes[cursor]
			if ancestor == nil {
				node.ParentMissing = true
				break
			}
			if seen[cursor] || len(seen) >= 16 {
				valid = false
				break
			}
			seen[cursor] = true
			parts = append([]string{ancestor.Row.Title}, parts...)
			cursor = ancestor.Row.ParentID
		}
		if valid {
			for i, title := range parts {
				if i > 0 {
					node.Path += " / "
				}
				node.Path += title
			}
		}
		if parent == nil || !valid {
			roots = append(roots, node)
		} else {
			parent.Children = append(parent.Children, node)
		}
	}
	return roots
}
