package content

import (
	"strings"
	"testing"
)

func TestContentTreeBoundsCyclesAndFilteredParents(t *testing.T) {
	rows := []listRow{{ID: "a", Title: "Parent"}, {ID: "b", ParentID: "a", Title: "Child"}, {ID: "c", ParentID: "b", Title: "Grandchild"}, {ID: "outside", ParentID: "private", Title: "Visible child"}}
	roots := contentTree(rows, true)
	if len(roots) != 2 || len(roots[0].Children) != 1 || roots[0].Children[0].Children[0].Path != "Parent / Child" || !roots[1].ParentMissing {
		t.Fatal("tree/path shape incorrect")
	}
	if !roots[0].CanSelect {
		t.Fatal("selection permission dropped")
	}
	cycle := contentTree([]listRow{{ID: "a", ParentID: "b"}, {ID: "b", ParentID: "a"}}, false)
	if len(cycle) != 2 || len(cycle[0].Children) != 0 || len(cycle[1].Children) != 0 || cycle[0].CanSelect {
		t.Fatal("cycle created recursive output")
	}
	deep := []listRow{}
	for n := 1; n <= 40; n++ {
		id := strings.Repeat("x", n)
		parent := ""
		if n > 1 {
			parent = strings.Repeat("x", n-1)
		}
		deep = append(deep, listRow{ID: id, ParentID: parent})
	}
	if roots := contentTree(deep, false); len(roots) < 2 {
		t.Fatal("deep rendering not bounded")
	}
}
