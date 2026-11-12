package cli

import (
	"bytes"
	"encoding/json"
	"os"
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

func TestContentExport(t *testing.T) {
	dir := t.TempDir()
	themes, _ := filepath.Abs("../../themes")
	t.Setenv("GORUN_ENV", "dev")
	t.Setenv("GORUN_THEMES", themes)
	data := []string{"--data", dir}
	var out bytes.Buffer
	if err := starterCmd(data, &out); err != nil {
		t.Fatal(err)
	}
	// JSON to stdout: every non-trashed item, no limit.
	out.Reset()
	if err := contentCmd(append([]string{"export", "--format", "json", "--type", "post"}, data...), &out); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Format string `json:"format"`
		Count  int    `json:"count"`
		Items  []struct {
			Type string `json:"type"`
			Body string `json:"body"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if doc.Format != "gorun.content-export" || doc.Count != 5 || len(doc.Items) != 5 || doc.Items[0].Type != "post" || doc.Items[0].Body == "" {
		t.Fatalf("json export: %+v", doc)
	}
	// CSV to a file with chosen columns; stdout gets a one-line summary.
	file := filepath.Join(t.TempDir(), "pages.csv")
	out.Reset()
	if err := contentCmd(append([]string{"export", "--format", "csv", "--type", "page", "--columns", "id,title,status", "--out", file}, data...), &out); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(strings.TrimPrefix(string(raw), "\xef\xbb\xbf")), "\n")
	if strings.TrimSpace(lines[0]) != "id,title,status" || len(lines) != 5 {
		t.Fatalf("csv file: %q", lines)
	}
	if !strings.Contains(out.String(), "4 items") {
		t.Fatalf("summary: %q", out.String())
	}
	for _, bad := range [][]string{{"export", "--format", "xml"}, {"export", "--type", "nope"}, {"export", "--from", "soon"}} {
		if err := contentCmd(append(bad, data...), &out); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
}
