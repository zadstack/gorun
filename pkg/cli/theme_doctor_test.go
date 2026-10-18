package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestThemeDoctorCLI(t *testing.T) {
	themes, err := filepath.Abs("../../themes")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GORUN_ENV", "dev")
	t.Setenv("GORUN_THEMES", themes)
	dir := t.TempDir()
	var out bytes.Buffer
	if err := themeCmd([]string{"doctor", "default", "--data", dir}, strings.NewReader(""), &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "default ok") {
		t.Fatalf("output: %s", out.String())
	}
	out.Reset()
	if err := themeCmd([]string{"doctor", "default", "--json", "--data", dir}, strings.NewReader(""), &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	var reports []doctorReport
	if err := json.Unmarshal(out.Bytes(), &reports); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if len(reports) != 1 || reports[0].Slug != "default" || !reports[0].OK {
		t.Fatalf("%+v", reports)
	}
	out.Reset()
	if err := themeCmd([]string{"doctor", "--warnings", "default", "--data", dir}, strings.NewReader(""), &out); err != nil {
		t.Fatalf("warnings mode: %v\n%s", err, out.String())
	}
	out.Reset()
	if err := themeCmd([]string{"doctor", "no-such-theme", "--data", dir}, strings.NewReader(""), &out); err == nil {
		t.Fatal("expected missing theme error")
	}
}
