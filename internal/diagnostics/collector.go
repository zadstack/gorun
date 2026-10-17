// Package diagnostics stores bounded classifications, never raw log messages or attributes.
package diagnostics

import (
	"context"
	"database/sql"
	"embed"
	"io/fs"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

func Migrations() fs.FS { return migrations }

const ServiceName = "diagnostics.collector"
const queueSize = 128
const maxRows = 1000
const retention = 30 * 24 * time.Hour

type event struct {
	category, code string
	status         int
	at             time.Time
}
type Collector struct {
	db                   *sql.DB
	queue                chan event
	stop, done           chan struct{}
	startOnce, closeOnce sync.Once
	mu                   sync.RWMutex
	closed               bool
	dropped              atomic.Uint64
	failures             atomic.Uint64
	lastSuccessfulWrite  atomic.Int64
}

func New(d *sql.DB) *Collector {
	return &Collector{db: d, queue: make(chan event, queueSize), stop: make(chan struct{}), done: make(chan struct{})}
}
func (c *Collector) Start() { c.startOnce.Do(func() { go c.run() }) }
func (c *Collector) Close() {
	c.closeOnce.Do(func() { c.mu.Lock(); c.closed = true; close(c.stop); c.mu.Unlock() })
	c.Start()
	<-c.done
}
func (c *Collector) Dropped() uint64         { return c.dropped.Load() }
func (c *Collector) StorageFailures() uint64 { return c.failures.Load() }
func (c *Collector) LastSuccessfulWrite() time.Time {
	stamp := c.lastSuccessfulWrite.Load()
	if stamp == 0 {
		return time.Time{}
	}
	return time.Unix(0, stamp).UTC()
}
func (c *Collector) observe(category, code string, status int, at time.Time) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return
	}
	select {
	case c.queue <- event{category, code, status, at.UTC()}:
	default:
		c.dropped.Add(1)
	}
}
func (c *Collector) run() {
	defer close(c.done)
	timer := time.NewTicker(250 * time.Millisecond)
	defer timer.Stop()
	maintenance := time.NewTicker(time.Hour)
	defer maintenance.Stop()
	c.flush(true)
	for {
		select {
		case <-c.stop:
			c.flush(false)
			return
		case <-timer.C:
			c.flush(false)
		case <-maintenance.C:
			c.flush(true)
		}
	}
}
func (c *Collector) flush(prune bool) {
	batch := make([]event, 0, queueSize)
	for len(batch) < queueSize {
		select {
		case e := <-c.queue:
			batch = append(batch, e)
		default:
			goto write
		}
	}
write:
	if len(batch) == 0 && !prune {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := db.InTx(ctx, c.db, func(tx *sql.Tx) error {
		for _, e := range batch {
			at := db.FormatTime(e.at)
			_, err := tx.ExecContext(ctx, `INSERT INTO diagnostic_events(day,category,code,http_status,first_at,last_at,count) VALUES(?,?,?,?,?,?,1) ON CONFLICT(day,category,code,http_status) DO UPDATE SET count=MIN(diagnostic_events.count+1,2147483647),first_at=MIN(diagnostic_events.first_at,excluded.first_at),last_at=MAX(diagnostic_events.last_at,excluded.last_at)`, e.at.Format(time.DateOnly), e.category, e.code, e.status, at, at)
			if err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM diagnostic_events WHERE last_at < ?`, db.FormatTime(time.Now().Add(-retention))); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM diagnostic_events WHERE rowid IN(SELECT rowid FROM diagnostic_events ORDER BY last_at DESC,rowid DESC LIMIT -1 OFFSET ?)`, maxRows)
		return err
	})
	if err != nil {
		c.failures.Add(1)
		c.dropped.Add(uint64(len(batch)))
	} else {
		c.lastSuccessfulWrite.Store(time.Now().UTC().UnixNano())
	}
}
