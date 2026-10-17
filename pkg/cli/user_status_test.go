package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestUserStatusAndInfoSettings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORUN_ENV", "dev")
	data := []string{"--data", dir}
	for _, u := range [][]string{{"a@example.test", "--admin"}, {"e@example.test"}} {
		args := append([]string{"--email", u[0], "--password", "a-long-test-password"}, u[1:]...)
		if err := userCreate(append(args, data...)); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := userStatus(append([]string{"--email", "a@example.test", "--status", "disabled"}, data...), &out); err == nil || !strings.Contains(err.Error(), "last active administrator") {
		t.Fatalf("last admin disabled: %v", err)
	}
	if err := userStatus(append([]string{"--email", "e@example.test", "--status", "disabled"}, data...), &out); err != nil {
		t.Fatal(err)
	}
	if err := userStatus(append([]string{"--email", "e@example.test", "--status", "maybe"}, data...), &out); err == nil {
		t.Fatal("bad status accepted")
	}
	for k, v := range map[string]string{"site.maintenance": "on", "seo.robots_txt": "User-agent: *\nDisallow: /", "site.timezone": "Africa/Cairo"} {
		if err := settingsCmd([]string{"set", k, v, "--data", dir}, &bytes.Buffer{}); err != nil {
			t.Fatalf("%s: %v", k, err)
		}
	}
	if err := settingsCmd([]string{"set", "site.maintenance", "maybe", "--data", dir}, &bytes.Buffer{}); err == nil {
		t.Fatal("bad maintenance value accepted")
	}
	out.Reset()
	if err := infoCmd(data, &out); err != nil {
		t.Fatal(err)
	}
	var info siteInfo
	json.Unmarshal(out.Bytes(), &info)
	if !info.Maintenance || !info.HiddenFromSearch || info.Timezone != "Africa/Cairo" || !info.FormsNotify || info.PostsPerPage != 10 {
		t.Fatalf("info %+v", info)
	}
	for _, u := range info.Users {
		if u.Email == "e@example.test" && u.Status != "disabled" {
			t.Fatalf("status %+v", u)
		}
	}
}
