package admin

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var errStudio = errors.New("studio: invalid configuration or provider response")
var studioID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,39}$`)
var studioPath = regexp.MustCompile(`^[A-Za-z0-9_]+(?:\.[A-Za-z0-9_]+){0,7}$`)

type studioBinding struct {
	ConnectionVersion string          `json:"connection_version,omitempty"`
	Endpoint          string          `json:"endpoint,omitempty"`
	ID                string          `json:"id"`
	Name              string          `json:"name"`
	Kind              string          `json:"kind"`
	ConnectionID      string          `json:"connection_id"`
	Tool              string          `json:"tool"`
	Arguments         json.RawMessage `json:"arguments"`
	TextPath          string          `json:"text_path,omitempty"`
	URLPath           string          `json:"url_path,omitempty"`
	JobPath           string          `json:"job_path,omitempty"`
	StatusTool        string          `json:"status_tool,omitempty"`
	StatusArguments   json.RawMessage `json:"status_arguments,omitempty"`
	StatusPath        string          `json:"status_path,omitempty"`
	ReadyValue        string          `json:"ready_value,omitempty"`
	FailedValue       string          `json:"failed_value,omitempty"`
}
type studioPreset struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Prompt string `json:"prompt"`
}
type studioConfig struct {
	ImageModel string          `json:"image_model"`
	Bindings   []studioBinding `json:"bindings"`
	Presets    []studioPreset  `json:"presets"`
}
type studioInput struct {
	RequestID  string `json:"request_id"`
	ReviewHash string `json:"review_hash"`
	Provider   string `json:"provider"`
	Kind       string `json:"kind"`
	Prompt     string `json:"prompt"`
	Title      string `json:"title"`
	Text       string `json:"text"`
	Preset     string `json:"preset"`
	Reviewed   bool   `json:"reviewed"`
}
type studioOutput struct {
	Text, URL, JobID, Status string
	Bytes                    []byte
}

func studioKind(s string) bool { return s == "text" || s == "image" || s == "video" || s == "render" }
func validateStudioBinding(b studioBinding) error {
	if !studioID.MatchString(b.ID) || b.ID == "native-text" || b.ID == "native-image" || strings.TrimSpace(b.Name) == "" || len(b.Name) > 100 || !studioKind(b.Kind) || !connectionIDPattern.MatchString(b.ConnectionID) || len(b.Tool) == 0 || len(b.Tool) > 128 {
		return errStudio
	}
	if _, e := studioArguments(b.Arguments, studioInput{}, ""); e != nil {
		return e
	}
	for _, p := range []string{b.TextPath, b.URLPath, b.JobPath, b.StatusPath} {
		if p != "" && !studioPath.MatchString(p) {
			return errStudio
		}
	}
	if b.Kind != "text" && b.Kind != "image" && b.URLPath == "" {
		return errStudio
	}
	if b.StatusTool != "" || b.JobPath != "" {
		if b.StatusTool == "" || len(b.StatusTool) > 128 || b.JobPath == "" || b.StatusPath == "" || b.ReadyValue == "" || b.FailedValue == "" || b.ReadyValue == b.FailedValue {
			return errStudio
		}
		if _, e := studioArguments(b.StatusArguments, studioInput{}, "job"); e != nil {
			return e
		}
		if !strings.Contains(string(b.StatusArguments), "{{job_id}}") {
			return errStudio
		}
	}
	return nil
}
func validateStudioConfig(c studioConfig) error {
	if c.ImageModel != "" && !aiModelRe.MatchString(c.ImageModel) {
		return errStudio
	}
	if len(c.Bindings) > 40 || len(c.Presets) > 40 {
		return errStudio
	}
	ids := map[string]bool{}
	for _, b := range c.Bindings {
		if ids[b.ID] {
			return errStudio
		}
		ids[b.ID] = true
		if e := validateStudioBinding(b); e != nil {
			return e
		}
	}
	ids = map[string]bool{}
	for _, p := range c.Presets {
		if !studioID.MatchString(p.ID) || ids[p.ID] || p.Name == "" || len(p.Name) > 100 || !studioKind(p.Kind) || len(p.Prompt) > 8000 {
			return errStudio
		}
		ids[p.ID] = true
	}
	return nil
}
func studioArguments(raw json.RawMessage, in studioInput, job string) (map[string]any, error) {
	if len(raw) > 32768 {
		return nil, errStudio
	}
	var args map[string]any
	if json.Unmarshal(raw, &args) != nil || args == nil {
		return nil, errStudio
	}
	replace := strings.NewReplacer("{{prompt}}", in.Prompt, "{{title}}", in.Title, "{{text}}", in.Text, "{{job_id}}", job)
	var walk func(any, int) (any, error)
	walk = func(v any, depth int) (any, error) {
		if depth > 12 {
			return nil, errStudio
		}
		switch x := v.(type) {
		case string:
			return replace.Replace(x), nil
		case map[string]any:
			for k, a := range x {
				z, e := walk(a, depth+1)
				if e != nil {
					return nil, e
				}
				x[k] = z
			}
		case []any:
			for k, a := range x {
				z, e := walk(a, depth+1)
				if e != nil {
					return nil, e
				}
				x[k] = z
			}
		}
		return v, nil
	}
	v, e := walk(args, 0)
	if e != nil {
		return nil, e
	}
	return v.(map[string]any), nil
}
func studioValue(v map[string]any, path string) string {
	if path == "" {
		return ""
	}
	var a any = v
	for _, k := range strings.Split(path, ".") {
		// Some MCP servers return structured JSON inside a text block. Traverse it only
		// when an owner explicitly configured a deeper output path.
		if raw, ok := a.(string); ok {
			if len(raw) > 1<<20 || json.Unmarshal([]byte(raw), &a) != nil {
				return ""
			}
		}
		switch value := a.(type) {
		case map[string]any:
			a = value[k]
		case []any:
			index, err := strconv.Atoi(k)
			if err != nil || index < 0 || index >= len(value) || strconv.Itoa(index) != k {
				return ""
			}
			a = value[index]
		default:
			return ""
		}
	}
	s, _ := a.(string)
	return s
}
func studioMCPOutput(b studioBinding, raw json.RawMessage) (studioOutput, error) {
	var v map[string]any
	var out studioOutput
	if json.Unmarshal(raw, &v) != nil || v["isError"] == true {
		return out, errStudio
	}
	// MCP image content is an explicit image result, not text metadata or an arbitrary URL.
	if b.Kind == "image" {
		if blocks, ok := v["content"].([]any); ok {
			for _, block := range blocks {
				item, ok := block.(map[string]any)
				if !ok || item["type"] != "image" {
					continue
				}
				if len(out.Bytes) != 0 {
					return studioOutput{}, errStudio
				}
				data, _ := item["data"].(string)
				mime, _ := item["mimeType"].(string)
				if mime != "image/png" && mime != "image/jpeg" && mime != "image/webp" {
					return studioOutput{}, errStudio
				}
				if len(data) == 0 || len(data) > base64.StdEncoding.EncodedLen(8<<20) {
					return studioOutput{}, errStudio
				}
				decoded, err := base64.StdEncoding.Strict().DecodeString(data)
				if err != nil || len(decoded) == 0 || http.DetectContentType(decoded) != mime {
					return studioOutput{}, errStudio
				}
				out.Bytes = decoded
			}
		}
	}
	out.Text = studioValue(v, b.TextPath)
	out.URL = studioValue(v, b.URLPath)
	out.JobID = studioValue(v, b.JobPath)
	out.Status = studioValue(v, b.StatusPath)
	if out.Status == b.FailedValue && b.FailedValue != "" {
		return out, errStudio
	}
	if b.Kind == "text" && out.Text == "" {
		if blocks, ok := v["content"].([]any); ok {
			for _, a := range blocks {
				if block, ok := a.(map[string]any); ok && block["type"] == "text" {
					if s, ok := block["text"].(string); ok {
						out.Text += s + "\n"
					}
				}
			}
		}
	}
	if len(out.Text) > 100000 || len(out.URL) > 4096 || len(out.JobID) > 512 {
		return studioOutput{}, errStudio
	}
	if len(out.Bytes) > 0 || b.Kind == "text" && out.Text != "" || out.URL != "" || out.JobID != "" || out.Status != "" {
		return out, nil
	}
	return out, errStudio
}

func studioReviewHash(args map[string]any) string {
	raw, _ := json.Marshal(args)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
