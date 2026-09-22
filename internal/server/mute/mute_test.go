package mute

import (
	"context"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/doctor"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

var now = time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)

func setup(t *testing.T) (*store.DB, project.Project, context.Context) {
	t.Helper()
	db := store.OpenTest(t)
	ctx := context.Background()
	p, err := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	return db, p, ctx
}

// A machine whose agent must stay installed (a staging box, a server the team does not own)
// stops asking for attention; its numbers are not touched here, only who muted it and why.
func TestAMachineIsMutedAndHeardAgain(t *testing.T) {
	db, p, ctx := setup(t)
	id, err := db.Q().InsertID(ctx, `INSERT INTO hosts (project_id, name, kind, version, os, sources, last_seen_at) VALUES (?, 'staging', 'agent', '0.1', 'linux', '[]', ?)`, p.ID, now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	if err := SetHost(ctx, db, p.ID, id, "ada@example.com", " staging, nobody fixes it ", now); err != nil {
		t.Fatal(err)
	}
	other, _ := project.Repo{DB: db}.Create(ctx, "Other", "UTC")
	if err := SetHost(ctx, db, other.ID, id, "eve@example.com", "", now); err != ErrNotFound {
		t.Fatalf("a machine of another project was muted: %v", err)
	}

	hosts, err := Hosts(ctx, db, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	h, ok := hosts[id]
	if !ok || h.At != now.Unix() || h.By != "ada@example.com" || h.Note != "staging, nobody fixes it" {
		t.Fatalf("muted machine %+v %v", h, ok)
	}

	if err := ClearHost(ctx, db, p.ID, id); err != nil {
		t.Fatal(err)
	}
	if hosts, _ := Hosts(ctx, db, p.ID); len(hosts) != 0 {
		t.Fatalf("still muted: %v", hosts)
	}
}

// Advice the team cannot follow (an index on a table it does not own) is muted for that query
// only: the same advice on another query, or on another table of the same query, still speaks.
func TestADiagnosisIsMutedForOneQueryAndOneTable(t *testing.T) {
	db, p, ctx := setup(t)
	m, err := AddDiagnosis(ctx, db, p.ID, Diagnosis{Hash: "aaaa", Code: "full_scan", Table: "orders", Note: "vendor table", CreatedBy: "ada@example.com"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID == 0 || m.CreatedAt != now.Unix() {
		t.Fatalf("stored %+v", m)
	}
	// Muting it twice changes the note, it does not make two.
	again, err := AddDiagnosis(ctx, db, p.ID, Diagnosis{Hash: "aaaa", Code: "full_scan", Table: "orders", Note: "vendor table, asked them"}, now)
	if err != nil || again.ID != m.ID || again.Note != "vendor table, asked them" {
		t.Fatalf("again %+v %v", again, err)
	}
	if _, err := AddDiagnosis(ctx, db, p.ID, Diagnosis{Hash: "aaaa"}, now); err == nil {
		t.Fatal("a diagnosis without a code was accepted")
	}

	all, err := Diagnoses(ctx, db, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := []doctor.Diagnosis{
		{Code: "full_scan", Severity: doctor.Critical, Table: "orders"},
		{Code: "full_scan", Severity: doctor.Critical, Table: "customers"},
		{Code: "select_star", Severity: doctor.Info},
	}
	active, muted := Split(found, all["aaaa"])
	if len(active) != 2 || active[0].Table != "customers" || active[1].Code != "select_star" {
		t.Fatalf("active %+v", active)
	}
	if len(muted) != 1 || muted[0].Table != "orders" || muted[0].Mute.Note != "vendor table, asked them" {
		t.Fatalf("muted %+v", muted)
	}
	if active, muted := Split(found, all["bbbb"]); len(active) != 3 || len(muted) != 0 {
		t.Fatalf("another query is muted too: %v %v", active, muted)
	}

	if err := RemoveDiagnosis(ctx, db, p.ID, m.ID); err != nil {
		t.Fatal(err)
	}
	if all, _ := Diagnoses(ctx, db, p.ID); len(all) != 0 {
		t.Fatalf("still muted: %v", all)
	}
}
