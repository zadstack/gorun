package wpmigrate

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestWXRPublicationGMTAndFutureVisibility(t *testing.T) {
	e := newEnv(t)
	xml := `<?xml version="1.0"?><rss version="2.0" xmlns:wp="http://wordpress.org/export/1.2/" xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel><title>Date fixture</title><link>https://source.test</link><wp:wxr_version>1.2</wp:wxr_version>`
	for _, item := range []struct{ id, status, date string }{{"1", "future", "2099-02-03"}, {"2", "publish", "2020-02-03"}} {
		xml += fmt.Sprintf(`<item><title>Date %s</title><wp:post_id>%s</wp:post_id><wp:post_type>post</wp:post_type><wp:post_name>gmt-%s</wp:post_name><wp:status>%s</wp:status><wp:post_date>%s 15:00:00</wp:post_date><wp:post_date_gmt>%s 12:00:00</wp:post_date_gmt><content:encoded><![CDATA[<p>Timezone fixture only</p>]]></content:encoded></item>`, item.id, item.id, item.id, item.status, item.date, item.date)
	}
	xml += `</channel></rss>`
	file := filepath.Join(t.TempDir(), "dates.xml")
	if err := os.WriteFile(file, []byte(xml), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := ImportWXRFile(e.ctx(), e.app.Deps, file, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []struct {
		id, status, date string
		httpStatus       int
	}{{"1", "scheduled", "2099-02-03T12:00:00Z", http.StatusNotFound}, {"2", "published", "2020-02-03T12:00:00Z", http.StatusOK}} {
		row := e.item(result.MigrationID, "content", "post", expected.id)
		item, err := e.contentSvc().Get(e.ctx(), row.LocalID)
		if err != nil {
			t.Fatal(err)
		}
		if string(item.Status) != expected.status || item.PublishAt != expected.date {
			t.Errorf("GMT/status not preserved: status=%s publish_at=%s", item.Status, item.PublishAt)
		}
		if expected.status == "scheduled" && item.PublishedAt != "" {
			t.Error("future import claimed publication")
		}
		if expected.status == "published" && item.PublishedAt != expected.date {
			t.Errorf("past published date changed: %s", item.PublishedAt)
		}
		response, err := e.srv.Client().Get(e.srv.URL + row.LocalURL)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != expected.httpStatus {
			t.Errorf("public visibility of %s: got%d want%d", expected.status, response.StatusCode, expected.httpStatus)
		}
	}
}
