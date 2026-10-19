package admin

import "database/sql"

// Content is optional. Install history guards only when its migrations exist.
// The predicates invalidate boards referencing the changed item or its source
// type, rather than every tool run on the site.
func installManagedSourceTriggers(d *sql.DB) error {
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN('content','content_types')`).Scan(&n); err != nil {
		return err
	}
	if n != 2 {
		return nil
	}
	_, err := d.Exec(`
 CREATE TRIGGER IF NOT EXISTS canvas_tool_content_update AFTER UPDATE ON content BEGIN
 UPDATE admin_canvas_tool_authority SET epoch=epoch+1 WHERE board_id IN(
 SELECT b.id FROM admin_canvases b,json_each(b.document_json) c WHERE json_extract(c.value,'$.content_id') IN(OLD.id,NEW.id) OR json_extract(c.value,'$.source_type') IN(OLD.type,NEW.type));
 END;
 CREATE TRIGGER IF NOT EXISTS canvas_tool_content_delete AFTER DELETE ON content BEGIN
 UPDATE admin_canvas_tool_authority SET epoch=epoch+1 WHERE board_id IN(
 SELECT b.id FROM admin_canvases b,json_each(b.document_json) c WHERE json_extract(c.value,'$.content_id')=OLD.id OR json_extract(c.value,'$.source_type')=OLD.type);
 END;
 CREATE TRIGGER IF NOT EXISTS canvas_tool_content_insert AFTER INSERT ON content BEGIN
 UPDATE admin_canvas_tool_authority SET epoch=epoch+1 WHERE board_id IN(
 SELECT b.id FROM admin_canvases b,json_each(b.document_json) c WHERE json_extract(c.value,'$.content_id')=NEW.id OR json_extract(c.value,'$.source_type')=NEW.type);
 END;
 CREATE TRIGGER IF NOT EXISTS canvas_tool_type_update AFTER UPDATE ON content_types BEGIN
 UPDATE admin_canvas_tool_authority SET epoch=epoch+1 WHERE board_id IN(
 SELECT b.id FROM admin_canvases b,json_each(b.document_json) c WHERE json_extract(c.value,'$.source_type') IN(OLD.slug,NEW.slug) OR json_extract(c.value,'$.content_id') IN(SELECT id FROM content WHERE type IN(OLD.slug,NEW.slug)));
 END;
 `)
	return err
}
