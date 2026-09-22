// Package mute keeps what the team chose to stop hearing about: a machine whose agent stays
// installed (staging, a server somebody else runs) and one piece of the doctor's advice on one
// query that the team cannot follow (an index on a vendor's table, a schema it does not own).
// Nothing leaves the numbers: a muted machine keeps counting in the score and the debt, and a
// muted piece of advice is shown apart with who muted it and why, until somebody unmutes it.
package mute

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/doctor"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

var ErrNotFound = errors.New("not found")

const maxNote = 500

func cleanNote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxNote {
		s = s[:maxNote]
	}
	return s
}

// Host is a muted machine: since when, by whom and why.
type Host struct {
	At   int64  `json:"at"`
	By   string `json:"by"`
	Note string `json:"note"`
}

// SetHost mutes a machine of the project, or changes the note of one already muted.
func SetHost(ctx context.Context, db *store.DB, projectID, hostID int64, by, note string, now time.Time) error {
	res, err := db.Q().Exec(ctx, `UPDATE hosts SET muted_at = ?, muted_by = ?, muted_note = ? WHERE id = ? AND project_id = ?`,
		now.Unix(), by, cleanNote(note), hostID, projectID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ClearHost lets a machine speak again.
func ClearHost(ctx context.Context, db *store.DB, projectID, hostID int64) error {
	_, err := db.Q().Exec(ctx, `UPDATE hosts SET muted_at = NULL, muted_by = '', muted_note = '' WHERE id = ? AND project_id = ?`, hostID, projectID)
	return err
}

// Hosts returns the muted machines of the project, by host id.
func Hosts(ctx context.Context, db *store.DB, projectID int64) (map[int64]Host, error) {
	rows, err := db.Q().Query(ctx, `SELECT id, muted_at, muted_by, muted_note FROM hosts WHERE project_id = ? AND muted_at IS NOT NULL`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]Host{}
	for rows.Next() {
		var id int64
		var h Host
		if err := rows.Scan(&id, &h.At, &h.By, &h.Note); err != nil {
			return nil, err
		}
		out[id] = h
	}
	return out, rows.Err()
}

// Diagnosis is one piece of advice muted on one query. The table is part of it: a full scan
// muted on a vendor's table still speaks about the team's own table in the same query.
type Diagnosis struct {
	ID        int64  `json:"id"`
	Hash      string `json:"hash"`
	Code      string `json:"code"`
	Table     string `json:"table"`
	Note      string `json:"note"`
	CreatedBy string `json:"created_by"`
	CreatedAt int64  `json:"created_at"`
}

// AddDiagnosis mutes a piece of advice on a query, or changes the note of one already muted.
func AddDiagnosis(ctx context.Context, db *store.DB, projectID int64, d Diagnosis, now time.Time) (Diagnosis, error) {
	d.Hash, d.Code, d.Table = strings.TrimSpace(d.Hash), strings.TrimSpace(d.Code), strings.TrimSpace(d.Table)
	if d.Hash == "" || d.Code == "" || len(d.Hash) > 64 || len(d.Code) > 64 || len(d.Table) > 191 {
		return d, errors.New("query and diagnosis required")
	}
	d.Note = cleanNote(d.Note)
	if _, err := db.Q().Exec(ctx, `INSERT INTO muted_diagnoses (project_id, hash, code, table_name, note, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (project_id, hash, code, table_name) DO UPDATE SET note = excluded.note`,
		projectID, d.Hash, d.Code, d.Table, d.Note, d.CreatedBy, now.Unix()); err != nil {
		return d, err
	}
	err := db.Q().Row(ctx, `SELECT id, note, created_by, created_at FROM muted_diagnoses WHERE project_id = ? AND hash = ? AND code = ? AND table_name = ?`,
		projectID, d.Hash, d.Code, d.Table).Scan(&d.ID, &d.Note, &d.CreatedBy, &d.CreatedAt)
	return d, err
}

// RemoveDiagnosis lets a piece of advice speak again.
func RemoveDiagnosis(ctx context.Context, db *store.DB, projectID, id int64) error {
	_, err := db.Q().Exec(ctx, `DELETE FROM muted_diagnoses WHERE project_id = ? AND id = ?`, projectID, id)
	return err
}

// Diagnoses returns the muted advice of the project, by query hash. Read it before looping on
// queries: SQLite has a single connection and a nested query would wait forever.
func Diagnoses(ctx context.Context, db *store.DB, projectID int64) (map[string][]Diagnosis, error) {
	return diagnoses(ctx, db, `SELECT id, hash, code, table_name, note, created_by, created_at FROM muted_diagnoses WHERE project_id = ? ORDER BY id`, projectID)
}

// DiagnosesOf returns the muted advice of one query.
func DiagnosesOf(ctx context.Context, db *store.DB, projectID int64, hash string) ([]Diagnosis, error) {
	all, err := diagnoses(ctx, db, `SELECT id, hash, code, table_name, note, created_by, created_at FROM muted_diagnoses WHERE project_id = ? AND hash = ? ORDER BY id`, projectID, hash)
	return all[hash], err
}

func diagnoses(ctx context.Context, db *store.DB, query string, args ...any) (map[string][]Diagnosis, error) {
	rows, err := db.Q().Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]Diagnosis{}
	for rows.Next() {
		var d Diagnosis
		if err := rows.Scan(&d.ID, &d.Hash, &d.Code, &d.Table, &d.Note, &d.CreatedBy, &d.CreatedAt); err != nil {
			return nil, err
		}
		out[d.Hash] = append(out[d.Hash], d)
	}
	return out, rows.Err()
}

// Muted is a piece of advice the doctor still gives, with the mute that keeps it quiet.
type Muted struct {
	doctor.Diagnosis
	Mute Diagnosis `json:"mute"`
}

// Split separates the advice that still speaks from the muted one. A mute whose advice the
// doctor no longer gives (the problem went away) shows nowhere.
func Split(ds []doctor.Diagnosis, muted []Diagnosis) ([]doctor.Diagnosis, []Muted) {
	active := []doctor.Diagnosis{}
	quiet := []Muted{}
	for _, d := range ds {
		if m, ok := find(muted, d); ok {
			quiet = append(quiet, Muted{Diagnosis: d, Mute: m})
			continue
		}
		active = append(active, d)
	}
	return active, quiet
}

func find(muted []Diagnosis, d doctor.Diagnosis) (Diagnosis, bool) {
	for _, m := range muted {
		if m.Code == d.Code && m.Table == d.Table {
			return m, true
		}
	}
	return Diagnosis{}, false
}

// HostMuted tells whether one machine is muted, for pages that read a single host.
func HostMuted(ctx context.Context, db *store.DB, projectID, hostID int64) (*Host, error) {
	var at sql.NullInt64
	var h Host
	err := db.Q().Row(ctx, `SELECT muted_at, muted_by, muted_note FROM hosts WHERE id = ? AND project_id = ?`, hostID, projectID).Scan(&at, &h.By, &h.Note)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !at.Valid) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	h.At = at.Int64
	return &h, nil
}
