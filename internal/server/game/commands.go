package game

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

// evaluateCommandOverlaps finds the same scheduled command running at the same
// time on two different hosts: a forgotten crontab on an old server.
func (e *Engine) evaluateCommandOverlaps(ctx context.Context, p project.Project) error {
	now := e.Now()
	rows, err := e.DB.Q().Query(ctx, `SELECT host, name, started_at, finished_at FROM command_runs WHERE project_id = ? AND started_at >= ? ORDER BY name, started_at`,
		p.ID, now.Add(-3*time.Hour).Unix())
	if err != nil {
		return err
	}
	type run struct {
		host, name string
		start, end int64
	}
	byName := map[string][]run{}
	for rows.Next() {
		var r run
		var finished sql.NullInt64
		if err := rows.Scan(&r.host, &r.name, &r.start, &finished); err != nil {
			rows.Close()
			return err
		}
		r.end = now.Unix()
		if finished.Valid {
			r.end = finished.Int64
		}
		byName[r.name] = append(byName[r.name], r)
	}
	rows.Close()

	for name, runs := range byName {
		for i := 0; i < len(runs); i++ {
			for j := i + 1; j < len(runs); j++ {
				a, b := runs[i], runs[j]
				if a.host == b.host || !(a.start < b.end && b.start < a.end) {
					continue
				}
				day := time.Unix(a.start, 0).In(p.Location()).Format("2006-01-02")
				err := e.DB.Tx(ctx, func(q store.Q) error {
					first, err := e.once(ctx, q, fmt.Sprintf("overlap:%d:%s:%s", p.ID, name, day))
					if err != nil || !first {
						return err
					}
					return e.activity(ctx, q, p.ID, "command_overlap", "👯", fmt.Sprintf("%s runs on %s and %s at the same time", name, a.host, b.host),
						map[string]any{"command": name, "hosts": []string{a.host, b.host}})
				})
				if err != nil {
					return err
				}
				goto next
			}
		}
	next:
	}
	return nil
}
