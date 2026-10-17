package admin

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"testing"
)

func TestStudioMCPInlineImage(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	block := map[string]any{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(buf.Bytes())}
	encode := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	b := studioBinding{Kind: "image"}
	out, err := studioMCPOutput(b, encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": "metadata"}, block}}))
	if err != nil || !bytes.Equal(out.Bytes, buf.Bytes()) {
		t.Fatalf("inline image lost: %v", err)
	}
	for _, v := range []any{
		map[string]any{"isError": true, "content": []any{block}},
		map[string]any{"content": []any{block, block}},
		map[string]any{"content": []any{map[string]any{"type": "image", "mimeType": "image/svg+xml", "data": block["data"]}}},
		map[string]any{"content": []any{map[string]any{"type": "image", "mimeType": "image/png", "data": "not-base64"}}},
		map[string]any{"content": []any{map[string]any{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString([]byte("not an image"))}}},
	} {
		if _, err := studioMCPOutput(b, encode(v)); err == nil {
			t.Fatal("unsafe image accepted")
		}
	}
}

func TestStudioExplicitNestedTextOutput(t *testing.T) {
	b := studioBinding{Kind: "image", URLPath: "content.0.text.outputs.0.url"}
	raw := json.RawMessage(`{"content":[{"type":"text","text":"{\"outputs\":[{\"url\":\"https://media.example/image.png\"}]}"}]}`)
	out, err := studioMCPOutput(b, raw)
	if err != nil || out.URL != "https://media.example/image.png" {
		t.Fatalf("nested output missing: %v", err)
	}
	for _, path := range []string{"content.-1.text", "content.9.text", "content.01.text", "content.0.text.outputs.9.url"} {
		b.URLPath = path
		if _, err := studioMCPOutput(b, raw); err == nil {
			t.Fatal("invalid path accepted", path)
		}
	}
}
