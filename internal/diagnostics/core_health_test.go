package diagnostics

import (
	"strconv"
	"testing"
	"time"
)

func TestCollectorSuccessfulWriteTimeSurvivesFullDatabase(t *testing.T) {
	c := testCollector(t)
	c.db.SetMaxOpenConns(1)
	if !c.LastSuccessfulWrite().IsZero() {
		t.Fatal("unwritten collector claimed success")
	}
	c.observe("http", "http_5xx", 500, time.Now())
	c.flush(false)
	first := c.LastSuccessfulWrite()
	if first.IsZero() || first.Location() != time.UTC {
		t.Fatal("successful transaction timestamp missing or non-UTC")
	}
	var pages int
	if err := c.db.QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Exec(`PRAGMA max_page_count = ` + strconv.Itoa(pages)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		c.observe("http", "http_5xx", 500+i, time.Now())
	}
	c.flush(false)
	if c.StorageFailures() != 1 || c.Dropped() != 100 {
		t.Fatal("full database lost failure/drop accounting", c.StorageFailures(), c.Dropped())
	}
	if !c.LastSuccessfulWrite().Equal(first) {
		t.Fatal("failed write advanced successful time")
	}
}
