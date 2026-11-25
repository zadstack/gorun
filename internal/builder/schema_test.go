package builder

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/mcp"
)

// The schema is the one table the validator, the editor and the API read:
// it must name exactly the kinds the library has, and the validator must
// agree with it on every field, option and entry field.
func TestSchemaMatchesValidator(t *testing.T) {
	sc := Schema("en")
	var fromSchema []string
	for _, k := range sc.Native {
		fromSchema = append(fromSchema, k.Kind)
		if k.Label == "" {
			t.Errorf("%s: no label", k.Kind)
		}
	}
	if !slices.Equal(fromSchema, nativeKinds) {
		t.Fatalf("schema kinds %v != nativeKinds %v", fromSchema, nativeKinds)
	}
	if !slices.Equal(sc.NativeOnly, nativeOnlyKinds) || !slices.Equal(sc.Kinds, kinds) {
		t.Fatal("native-only or kind list drifted")
	}
	for _, k := range sc.Native {
		for _, kind := range kinds {
			if kind == k.Kind {
				continue
			}
			for _, v := range k.Variants {
				if validNativeVariant(kind, v) && !slices.Contains(mustSchema(t, kind).Variants, v) {
					t.Errorf("%s accepts %s's variant %s", kind, k.Kind, v)
				}
			}
		}
		for _, v := range k.Variants {
			if !validNativeVariant(k.Kind, v) {
				t.Errorf("%s: schema variant %s rejected", k.Kind, v)
			}
		}
		if validNativeVariant(k.Kind, "nope") {
			t.Errorf("%s accepts an unknown variant", k.Kind)
		}
	}
	// Each entry field and native option is accepted exactly where the
	// schema says.
	base := func(kind string) Section {
		s := DefaultSection(kind, "en")
		s.Library = SectionLibrary
		if s.Native == nil {
			s.Native = &NativeOptions{}
		}
		if kind == "directory" {
			s.Native.ListType = "post"
		}
		return s
	}
	valid := func(s Section) bool { return validateNative(s) == nil }
	for _, k := range sc.Native {
		if !valid(base(k.Kind)) {
			t.Errorf("%s: default native section invalid: %v", k.Kind, validateNative(base(k.Kind)))
			continue
		}
		s := base(k.Kind)
		s.Image, s.ImageAlt, s.Native.Illustration = "/i.png", "alt", false
		if valid(s) != k.Accepts("image") {
			t.Errorf("%s: image accepted=%v, schema says %v", k.Kind, valid(s), k.Accepts("image"))
		}
		if k.Items {
			for _, f := range []string{"image", "link", "price", "icon", "featured"} {
				s := base(k.Kind)
				e := Entry{Title: "T", Link: "/x"}
				switch f {
				case "image":
					e.Image, e.ImageAlt = "/i.png", "alt"
				case "link":
					e.Link = "/go"
				case "price":
					e.Price = "9"
				case "icon":
					e.Icon = "bolt"
				case "featured":
					e.Featured = true
				}
				if f != "link" && !k.Accepts("item.link") {
					e.Link = ""
				}
				s.Items = []Entry{e}
				if got, want := valid(s), k.Accepts("item."+f); got != want {
					t.Errorf("%s: item.%s accepted=%v, schema says %v (%v)", k.Kind, f, got, want, validateNative(s))
				}
			}
		} else {
			s := base(k.Kind)
			s.Items = []Entry{{Title: "T"}}
			if valid(s) {
				t.Errorf("%s: items accepted but schema has none", k.Kind)
			}
		}
		for _, f := range []string{"eyebrow", "secondary_link", "facts", "caption", "illustration", "columns"} {
			s := base(k.Kind)
			switch f {
			case "eyebrow":
				s.Native.Eyebrow = "Hi"
			case "secondary_link":
				s.Native.SecondaryLink, s.Native.SecondaryLabel = "/x", "More"
			case "facts":
				s.Native.Facts = []SectionFact{{Label: "A", Value: "1"}}
			case "caption":
				s.Native.Caption = "c"
			case "illustration":
				s.Native.Caption, s.Native.Illustration, s.Image = "c", true, ""
			case "columns":
				s.Native.Columns, s.Native.Rows = []string{"A", "B"}, nil
			}
			if got, want := valid(s), k.Accepts("native."+f); got != want {
				t.Errorf("%s: native.%s accepted=%v, schema says %v (%v)", k.Kind, f, got, want, validateNative(s))
			}
		}
	}
	if sc.Limits["sections"] != maxSections || sc.Limits["items"] != maxRepeatedEntries || !slices.Equal(sc.Icons, sectionIcons) || !slices.Equal(sc.Backgrounds, sectionBackgrounds) {
		t.Error("limits or enums drifted")
	}
	if !strings.Contains(Schema("ar").Native[0].Label, "واجهة") {
		t.Error("Arabic labels missing")
	}
}

func mustSchema(t *testing.T, kind string) KindSchema {
	t.Helper()
	k, ok := nativeSchema(kind)
	if !ok {
		t.Fatalf("no schema for %s", kind)
	}
	return k
}

// The schema is served to API keys with content.view and to MCP clients
// through get_section_schema, in the requested locale.
func TestSchemaServedOverRESTAndMCP(t *testing.T) {
	f := newLayoutFixture(t, mcp.New())
	viewer := f.user(t, "contributor")
	token := f.token(t, viewer, []string{"content.view"})
	status, raw := f.request(t, &http.Client{}, http.MethodGet, "/api/v1/builder/schema", token, "", nil)
	if status != 200 {
		t.Fatalf("GET /v1/builder/schema: %d %s", status, raw)
	}
	var envelope struct {
		OK   bool          `json:"ok"`
		Data SectionSchema `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || !envelope.OK {
		t.Fatalf("decode: %v %s", err, raw)
	}
	if len(envelope.Data.Native) != len(nativeKinds) || envelope.Data.Limits["sections"] != maxSections || envelope.Data.Legacy.Kind != "*" {
		t.Fatalf("schema body: %+v", envelope.Data)
	}
	if status, _ := f.request(t, &http.Client{}, http.MethodGet, "/api/v1/builder/schema", f.token(t, viewer, []string{"media.upload"}), "", nil); status != 403 {
		t.Fatalf("wrong scope: %d", status)
	}

	var tool kernel.MCPTool
	for _, candidate := range f.app.Deps.MCP.Tools() {
		if candidate.Name == "get_section_schema" {
			tool = candidate
		}
	}
	if tool.Name == "" || !tool.ReadOnly || tool.Sensitive || tool.Toolset != kernel.MCPToolsetBuilder {
		t.Fatalf("get_section_schema registration: %+v", tool)
	}
	client := layoutSDK(t, f, f.token(t, viewer, []string{"content.view"}))
	envelopeMCP, isError := layoutSDKCall(t, client, "get_section_schema", schemaInput{Locale: "ar"})
	if isError || envelopeMCP["ok"] != true {
		t.Fatalf("get_section_schema: %v", envelopeMCP)
	}
	data, _ := envelopeMCP["data"].(map[string]any)
	native, _ := data["native"].([]any)
	first, _ := native[0].(map[string]any)
	if len(native) != len(nativeKinds) || !strings.Contains(first["label"].(string), "واجهة") {
		t.Fatalf("mcp schema: %v", data)
	}
}
