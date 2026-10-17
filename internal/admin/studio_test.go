package admin

import (
	"encoding/json"
	"testing"
)

func TestStudioTemplateSubstitutionCannotInjectJSON(t *testing.T) {
	got, err := studioArguments(json.RawMessage(`{"prompt":"{{prompt}}","source":{"title":"{{title}}","text":"{{text}}"},"publish":false}`), studioInput{Prompt: `x", "publish":true`, Title: "Current title", Text: "unsaved text"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got["publish"] != false || got["prompt"] != `x", "publish":true` {
		t.Fatalf("unsafe substitution %#v", got)
	}
	if got["source"].(map[string]any)["text"] != "unsaved text" {
		t.Fatal("source missing")
	}
}
func TestStudioBindingRequiresExplicitCapabilityAndAsyncContract(t *testing.T) {
	b := studioBinding{ID: "video", Name: "Video", Kind: "video", ConnectionID: "01M4QBA2J8ZQA5ARC42V1PCQHA", Tool: "render", Arguments: json.RawMessage(`{"prompt":"{{prompt}}"}`), URLPath: "structuredContent.url"}
	if err := validateStudioBinding(b); err != nil {
		t.Fatal(err)
	}
	b.StatusTool = "check"
	if validateStudioBinding(b) == nil {
		t.Fatal("accepted incomplete polling contract")
	}
	b.StatusTool = ""
	b.Kind = "publish"
	if validateStudioBinding(b) == nil {
		t.Fatal("accepted unknown capability")
	}
}
func TestStudioResultRejectsToolErrorsAndUnmappedVideo(t *testing.T) {
	b := studioBinding{Kind: "video", URLPath: "structuredContent.url"}
	if _, err := studioMCPOutput(b, json.RawMessage(`{"isError":true,"content":[{"type":"text","text":"secret upstream"}]}`)); err == nil {
		t.Fatal("tool error passed")
	}
	if _, err := studioMCPOutput(b, json.RawMessage(`{"content":[{"type":"text","text":"done"}]}`)); err == nil {
		t.Fatal("fake video passed")
	}
	out, err := studioMCPOutput(b, json.RawMessage(`{"structuredContent":{"url":"https://media.example.com/movie.mp4"}}`))
	if err != nil || out.URL != "https://media.example.com/movie.mp4" {
		t.Fatalf("%+v %v", out, err)
	}
}
