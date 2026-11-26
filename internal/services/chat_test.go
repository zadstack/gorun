package services

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestChatRejectsBaseURLWithPath(t *testing.T) {
	d := testDeps(t)
	r := NewRegistry(d, Chat(d))
	ctx := context.Background()

	for _, bad := range []string{
		"https://chat.example.com/x",
		"http://chat.example.com",
		"https://chat.example.com?x=1",
		"https://chat.example.com/#x",
		"https://user@chat.example.com",
		"https://chat.example.com:99999",
		"https://chat.example.com 'unsafe-inline'",
		"https://*.example.com",
		"chat.example.com",
	} {
		_, fields, err := r.Set(ctx, "chat", map[string]string{"provider": "chatwoot", "base_url": bad, "website_token": "AbC123xyz789"}, nil, "cli")
		if err != nil {
			t.Fatal(err)
		}
		if fields["base_url"] == "" {
			t.Errorf("%q accepted: %v", bad, fields)
		}
		if v := d.Settings.GetString(ctx, "chat.base_url", ""); v != "" {
			t.Fatalf("%q: refused set left chat.base_url=%q", bad, v)
		}
	}

	_, fields, err := r.Set(ctx, "chat", map[string]string{"provider": "chatwoot", "base_url": "https://chat.example.com", "website_token": "short"}, nil, "cli")
	if err != nil || fields["website_token"] == "" {
		t.Fatalf("short token: fields %v err %v", fields, err)
	}
	_, fields, _ = r.Set(ctx, "chat", map[string]string{"provider": "intercom"}, nil, "cli")
	if fields["provider"] == "" {
		t.Errorf("unknown provider accepted: %v", fields)
	}

	st, fields, err := r.Set(ctx, "chat", map[string]string{"provider": "chatwoot", "base_url": "https://Chat.Example.com:8443/", "website_token": "AbC123xyz789"}, nil, "cli")
	if err != nil || len(fields) > 0 {
		t.Fatalf("good set: fields %v err %v", fields, err)
	}
	if st.State != StateConfigured || st.Provider != "chatwoot" || st.Fields["base_url"] != "https://chat.example.com:8443" || st.Fields["website_token"] != "AbC123xyz789" {
		t.Fatalf("status %+v", st)
	}
	if st.SecretSet {
		t.Error("chat has no secret")
	}

	// A partial set keeps the other fields.
	st, fields, _ = r.Set(ctx, "chat", map[string]string{"website_token": "NewToken12345"}, nil, "cli")
	if len(fields) > 0 || st.Fields["base_url"] != "https://chat.example.com:8443" || st.Fields["website_token"] != "NewToken12345" {
		t.Fatalf("partial set: fields %v status %+v", fields, st)
	}
	b, _ := json.Marshal(st)
	if !strings.Contains(string(b), `"website_token":"NewToken12345"`) {
		t.Errorf("status hides the public token: %s", b)
	}

	if err := r.Off(ctx, "chat", "cli"); err != nil {
		t.Fatal(err)
	}
	st, _ = Chat(d).Status(ctx)
	if st.State != StateOff {
		t.Errorf("after off: %+v", st)
	}
}
