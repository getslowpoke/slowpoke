package game

import (
	"context"
	"fmt"
	"strings"

	"github.com/getslowpoke/slowpoke/internal/server/store"
)

// Life is where a mission stands, the same for the panel, the lists and the coding assistant:
// nobody has to guess whether somebody is on it or whether the fix is already online.
type Life struct {
	// Stage, in the order a mission goes through them: open (nobody on it), improving (the cost
	// is going down and nobody said why), claimed, review (a pull request is linked), shipped (the
	// fix is online and being verified), then holding, confirmed or relapsed once completed.
	// Bosses, expired and excluded missions keep their status as their stage.
	Stage         string        `json:"stage"`
	ClaimedAt     *int64        `json:"claimed_at"`
	ShippedAt     *int64        `json:"shipped_at"`
	ShippedDeploy string        `json:"shipped_deploy"`
	Verdict       string        `json:"verdict"`
	VerdictAt     *int64        `json:"verdict_at"`
	HeldWindows   int           `json:"held_windows"`
	WatchWindows  int           `json:"watch_windows"`
	RelapseOf     *int64        `json:"relapse_of"`    // a boss: the mission whose fix came back
	RelapsedBy    *int64        `json:"relapsed_by"`   // a mission: the boss that reopened it
	PullRequests  []PullRequest `json:"pull_requests"`
	// Seconds per request on the busy hours: where it started and the latest judged reading.
	BaselineRate *float64 `json:"baseline_rate"`
	CurrentRate  *float64 `json:"current_rate"`
}

type PullRequest struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

func stageOf(typ, status, verdict string, l Life, goodWindows int) string {
	switch status {
	case "open":
		if typ == "boss" {
			return "boss"
		}
		switch {
		case l.ShippedAt != nil:
			return "shipped"
		case len(l.PullRequests) > 0:
			return "review"
		case l.ClaimedAt != nil:
			return "claimed"
		case goodWindows > 0:
			return "improving"
		}
		return "open"
	case "completed":
		switch verdict {
		case "confirmed", "relapsed":
			return verdict
		}
		return "holding"
	}
	return status
}

// Lives reads where each of the given missions stands.
func Lives(ctx context.Context, db *store.DB, ids []int64) (map[int64]Life, error) {
	out := make(map[int64]Life, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	type row struct {
		typ, status, verdict string
		good                 int
		life                 Life
	}
	rows, err := db.Q().Query(ctx, `SELECT m.id, m.type, m.status, m.verdict, m.good_windows, m.claimed_at, m.shipped_at,
		COALESCE(NULLIF(d.version, ''), d.commit_sha, ''), m.verdict_at, m.held_windows, m.relapse_of,
		(SELECT MAX(b.id) FROM missions b WHERE b.relapse_of = m.id), m.baseline_rate, m.current_rate
		FROM missions m LEFT JOIN deploys d ON d.id = m.shipped_deploy_id WHERE m.id IN (`+marks+`)`, args...)
	if err != nil {
		return nil, err
	}
	all := map[int64]*row{}
	for rows.Next() {
		var id int64
		r := &row{}
		if err := rows.Scan(&id, &r.typ, &r.status, &r.verdict, &r.good, &r.life.ClaimedAt, &r.life.ShippedAt, &r.life.ShippedDeploy,
			&r.life.VerdictAt, &r.life.HeldWindows, &r.life.RelapseOf, &r.life.RelapsedBy, &r.life.BaselineRate, &r.life.CurrentRate); err != nil {
			rows.Close()
			return nil, err
		}
		r.life.Verdict, r.life.WatchWindows, r.life.PullRequests = r.verdict, watchWindows, []PullRequest{}
		all[id] = r
	}
	rows.Close()
	prs, err := db.Q().Query(ctx, `SELECT mission_id, external_id, url FROM mission_tasks WHERE provider = 'pull_request' AND mission_id IN (`+marks+`) ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	for prs.Next() {
		var id int64
		var pr PullRequest
		if err := prs.Scan(&id, &pr.Title, &pr.URL); err != nil {
			prs.Close()
			return nil, err
		}
		if r := all[id]; r != nil {
			r.life.PullRequests = append(r.life.PullRequests, pr)
		}
	}
	prs.Close()
	for id, r := range all {
		r.life.Stage = stageOf(r.typ, r.status, r.verdict, r.life, r.good)
		out[id] = r.life
	}
	return out, nil
}

// StageInWords is the stage as a sentence for the coding assistant and the brief.
func StageInWords(l Life, person string, needWindows, goodWindows int) string {
	who := person
	if who == "" {
		who = "somebody"
	}
	switch l.Stage {
	case "open":
		return "nobody is on it"
	case "improving":
		return fmt.Sprintf("nobody took it, but the cost is already going down (%d of %d windows under the target)", goodWindows, needWindows)
	case "claimed":
		return "taken by " + who
	case "review":
		return fmt.Sprintf("taken by %s, pull request linked (%s)", who, l.PullRequests[len(l.PullRequests)-1].URL)
	case "shipped":
		deploy := ""
		if l.ShippedDeploy != "" {
			deploy = " with deploy " + l.ShippedDeploy
		}
		return fmt.Sprintf("the fix by %s is online%s: being verified, %d of %d windows under the target", who, deploy, goodWindows, needWindows)
	case "holding":
		return fmt.Sprintf("completed, still watched: holding for %d of %d windows", l.HeldWindows, l.WatchWindows)
	case "confirmed":
		return "completed and confirmed: it held for the whole watch"
	case "relapsed":
		return "completed, but the cost came back: see the boss that reopened it"
	}
	return l.Stage
}
