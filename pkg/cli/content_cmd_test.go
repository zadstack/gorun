package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestContentList(t *testing.T) {
	dir := t.TempDir()
	themes, _ := filepath.Abs("../../themes")
	t.Setenv("GORUN_ENV", "dev")
	t.Setenv("GORUN_THEMES", themes)
	data := []string{"--data", dir}
	var out bytes.Buffer
	if err := starterCmd(data, &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := contentCmd(append([]string{"list"}, data...), &out); err != nil {
		t.Fatal(err)
	}
	var res contentList
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	counts := map[string]int{}
	for _, it := range res.Items {
		counts[it.Type]++
		if it.Status == "published" && !strings.HasPrefix(it.URL, "http") {
			t.Fatalf("published item without a link: %+v", it)
		}
	}
	if counts["post"] != 5 || counts["page"] != 4 {
		t.Fatalf("counts %v (%d items)", counts, len(res.Items))
	}
	out.Reset()
	if err := contentCmd(append([]string{"list", "--type", "page", "--limit", "2"}, data...), &out); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(out.Bytes(), &res)
	if len(res.Items) != 2 || res.Items[0].Type != "page" {
		t.Fatalf("filtered %+v", res.Items)
	}
	if err := contentCmd(append([]string{"list", "--limit", "0"}, data...), &out); err == nil {
		t.Fatal("bad limit accepted")
	}
}
