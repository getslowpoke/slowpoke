// Package project holds projects, their tokens and settings.
package project

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/store"
)

type Project struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	Slug      string   `json:"slug"`
	Token     string   `json:"token,omitempty"`
	Timezone  string   `json:"timezone"`
	Settings  Settings `json:"settings"`
	CreatedAt int64    `json:"created_at"`
}

var ErrNotFound = errors.New("project not found")

func (p Project) Location() *time.Location {
	if loc, err := time.LoadLocation(p.Timezone); err == nil {
		return loc
	}
	return time.UTC
}

func NewToken() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return "spk_" + hex.EncodeToString(b)
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func Slugify(name string) string {
	s := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-"), "-")
	if s == "" {
		b := make([]byte, 3)
		_, _ = rand.Read(b)
		return "project-" + hex.EncodeToString(b)
	}
	return s
}

type Repo struct{ DB *store.DB }

const columns = "id, name, slug, token, timezone, settings, created_at"

func scan(row interface{ Scan(...any) error }) (Project, error) {
	var p Project
	var settings string
	if err := row.Scan(&p.ID, &p.Name, &p.Slug, &p.Token, &p.Timezone, &settings, &p.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return p, ErrNotFound
		}
		return p, err
	}
	p.Settings, _ = ParseSettings(settings)
	return p, nil
}

func (r Repo) Create(ctx context.Context, name, timezone string) (Project, error) {
	if _, err := time.LoadLocation(timezone); err != nil || timezone == "" {
		timezone = "UTC"
	}
	base := Slugify(name)
	slug := base
	for i := 2; ; i++ {
		var n int
		if err := r.DB.Q().Row(ctx, "SELECT COUNT(*) FROM projects WHERE slug = ?", slug).Scan(&n); err != nil {
			return Project{}, err
		}
		if n == 0 {
			break
		}
		slug = base + "-" + strconv.Itoa(i)
	}
	p := Project{Name: strings.TrimSpace(name), Slug: slug, Token: NewToken(), Timezone: timezone, Settings: Defaults(), CreatedAt: time.Now().Unix()}
	id, err := r.DB.Q().InsertID(ctx, "INSERT INTO projects (name, slug, token, timezone, settings, created_at) VALUES (?, ?, ?, ?, '{}', ?)", p.Name, p.Slug, p.Token, p.Timezone, p.CreatedAt)
	p.ID = id
	return p, err
}

func (r Repo) List(ctx context.Context) ([]Project, error) {
	rows, err := r.DB.Q().Query(ctx, "SELECT "+columns+" FROM projects ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		p, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r Repo) BySlug(ctx context.Context, slug string) (Project, error) {
	return scan(r.DB.Q().Row(ctx, "SELECT "+columns+" FROM projects WHERE slug = ?", slug))
}

func (r Repo) ByToken(ctx context.Context, token string) (Project, error) {
	if !strings.HasPrefix(token, "spk_") {
		return Project{}, ErrNotFound
	}
	return scan(r.DB.Q().Row(ctx, "SELECT "+columns+" FROM projects WHERE token = ?", token))
}

func (r Repo) ByID(ctx context.Context, id int64) (Project, error) {
	return scan(r.DB.Q().Row(ctx, "SELECT "+columns+" FROM projects WHERE id = ?", id))
}

func (r Repo) Update(ctx context.Context, p Project) error {
	if err := p.Settings.Validate(); err != nil {
		return err
	}
	if _, err := time.LoadLocation(p.Timezone); err != nil {
		return errors.New("invalid timezone")
	}
	raw, _ := jsonMarshal(p.Settings)
	_, err := r.DB.Q().Exec(ctx, "UPDATE projects SET name = ?, timezone = ?, settings = ? WHERE id = ?", strings.TrimSpace(p.Name), p.Timezone, raw, p.ID)
	return err
}

func (r Repo) RotateToken(ctx context.Context, id int64) (string, error) {
	token := NewToken()
	_, err := r.DB.Q().Exec(ctx, "UPDATE projects SET token = ? WHERE id = ?", token, id)
	return token, err
}

func (r Repo) Delete(ctx context.Context, id int64) error {
	_, err := r.DB.Q().Exec(ctx, "DELETE FROM projects WHERE id = ?", id)
	return err
}

// ObservedDays is how many days of data a window really holds: a project installed an hour
// ago has one hour, not seven days, to spread its costs over. Minute tables give the start;
// the hourly query table is only a fallback because it rounds down to the hour.
// It lives here, not in metrics, so ingestion can use it without an import cycle.
func (r Repo) ObservedDays(ctx context.Context, projectID int64, window time.Duration, now time.Time) float64 {
	var first int64
	_ = r.DB.Q().Row(ctx, `SELECT COALESCE(MIN(t), 0) FROM (
		SELECT MIN(bucket) AS t FROM http_minutes WHERE project_id = ?
		UNION ALL SELECT MIN(bucket) FROM db_minutes WHERE project_id = ?) x WHERE t IS NOT NULL`, projectID, projectID).Scan(&first)
	if first == 0 {
		_ = r.DB.Q().Row(ctx, `SELECT COALESCE(MIN(hour), 0) FROM query_hours WHERE project_id = ?`, projectID).Scan(&first)
	}
	observed := window
	if first > 0 {
		observed = min(window, max(now.Sub(time.Unix(first, 0)), 15*time.Minute))
	}
	return observed.Hours() / 24
}
