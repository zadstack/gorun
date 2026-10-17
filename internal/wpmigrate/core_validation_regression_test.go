package wpmigrate

import "testing"

func TestValidationDetectsMissingInlineLocalMedia(t *testing.T) {
	e := newEnv(t)
	id := e.wxrMigration(Options{})
	e.run(id)
	it := e.item(id, "content", "post", "70")
	if _, err := e.svc.db.Exec(`UPDATE content SET body=? WHERE id=?`, `<picture><source srcset="/media/missing-variant.png 2x"><img src="/media/missing.png"></picture>`, it.LocalID); err != nil {
		t.Fatal(err)
	}
	rep, err := e.svc.Validate(e.ctx(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.MissingMedia) != 2 {
		t.Fatalf("missing media=%d want2: %+v", len(rep.MissingMedia), rep)
	}
}
