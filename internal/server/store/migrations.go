package store

import (
	"context"
	"fmt"
	"strings"
)

// Each migration is written once; {id}, {real} and {bool} are replaced per dialect.
var migrations = []string{
	`CREATE TABLE users (
		id {id},
		email TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL,
		password_hash TEXT NOT NULL,
		created_at BIGINT NOT NULL
	);
	CREATE TABLE sessions (
		token_hash TEXT PRIMARY KEY,
		user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		expires_at BIGINT NOT NULL
	);
	CREATE TABLE app_settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);
	CREATE TABLE projects (
		id {id},
		name TEXT NOT NULL,
		slug TEXT NOT NULL UNIQUE,
		token TEXT NOT NULL UNIQUE,
		timezone TEXT NOT NULL DEFAULT 'UTC',
		settings TEXT NOT NULL DEFAULT '{}',
		created_at BIGINT NOT NULL
	);
	CREATE TABLE hosts (
		id {id},
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		name TEXT NOT NULL,
		kind TEXT NOT NULL DEFAULT 'agent',
		version TEXT NOT NULL DEFAULT '',
		os TEXT NOT NULL DEFAULT '',
		sources TEXT NOT NULL DEFAULT '[]',
		last_seen_at BIGINT NOT NULL,
		UNIQUE (project_id, name, kind)
	);
	CREATE TABLE ingest_batches (
		id TEXT PRIMARY KEY,
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		created_at BIGINT NOT NULL
	);
	CREATE INDEX ingest_batches_created ON ingest_batches (created_at);

	CREATE TABLE http_minutes (
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		source TEXT NOT NULL,
		bucket BIGINT NOT NULL,
		n BIGINT NOT NULL DEFAULT 0, sum {real} NOT NULL DEFAULT 0, max {real} NOT NULL DEFAULT 0,
		n1 BIGINT NOT NULL DEFAULT 0, n2 BIGINT NOT NULL DEFAULT 0, n5 BIGINT NOT NULL DEFAULT 0,
		e4 BIGINT NOT NULL DEFAULT 0, e5 BIGINT NOT NULL DEFAULT 0,
		h0 BIGINT NOT NULL DEFAULT 0, h1 BIGINT NOT NULL DEFAULT 0, h2 BIGINT NOT NULL DEFAULT 0, h3 BIGINT NOT NULL DEFAULT 0,
		h4 BIGINT NOT NULL DEFAULT 0, h5 BIGINT NOT NULL DEFAULT 0, h6 BIGINT NOT NULL DEFAULT 0, h7 BIGINT NOT NULL DEFAULT 0,
		h8 BIGINT NOT NULL DEFAULT 0, h9 BIGINT NOT NULL DEFAULT 0, h10 BIGINT NOT NULL DEFAULT 0, h11 BIGINT NOT NULL DEFAULT 0,
		h12 BIGINT NOT NULL DEFAULT 0, h13 BIGINT NOT NULL DEFAULT 0, h14 BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY (project_id, source, bucket)
	);
	CREATE INDEX http_minutes_bucket ON http_minutes (project_id, bucket);

	CREATE TABLE endpoint_hours (
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		source TEXT NOT NULL,
		endpoint TEXT NOT NULL,
		hour BIGINT NOT NULL,
		n BIGINT NOT NULL DEFAULT 0, sum {real} NOT NULL DEFAULT 0, max {real} NOT NULL DEFAULT 0,
		slow BIGINT NOT NULL DEFAULT 0, slow_sum {real} NOT NULL DEFAULT 0, e5 BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY (project_id, source, endpoint, hour)
	);
	CREATE INDEX endpoint_hours_hour ON endpoint_hours (project_id, hour);

	CREATE TABLE query_fingerprints (
		id {id},
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		hash TEXT NOT NULL,
		fingerprint TEXT NOT NULL,
		sample TEXT,
		sample_seconds {real} NOT NULL DEFAULT 0,
		db_user TEXT NOT NULL DEFAULT '',
		db_name TEXT NOT NULL DEFAULT '',
		explain TEXT,
		explain_error TEXT,
		explained_at BIGINT,
		explain_requested_at BIGINT,
		origin TEXT NOT NULL DEFAULT '',
		n_plus_one BIGINT NOT NULL DEFAULT 0,
		first_seen_at BIGINT NOT NULL,
		last_seen_at BIGINT NOT NULL,
		UNIQUE (project_id, hash)
	);

	CREATE TABLE query_hours (
		fingerprint_id BIGINT NOT NULL REFERENCES query_fingerprints(id) ON DELETE CASCADE,
		source TEXT NOT NULL,
		hour BIGINT NOT NULL,
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		n BIGINT NOT NULL DEFAULT 0, sum {real} NOT NULL DEFAULT 0, max {real} NOT NULL DEFAULT 0, rows BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY (fingerprint_id, source, hour)
	);
	CREATE INDEX query_hours_hour ON query_hours (project_id, hour);

	CREATE TABLE db_minutes (
		host_id BIGINT NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
		bucket BIGINT NOT NULL,
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		slow_n BIGINT NOT NULL DEFAULT 0, slow_sum {real} NOT NULL DEFAULT 0, slow_max {real} NOT NULL DEFAULT 0,
		qps {real}, rows_read_ps {real}, threads_running BIGINT, threads_connected BIGINT,
		PRIMARY KEY (host_id, bucket)
	);
	CREATE INDEX db_minutes_bucket ON db_minutes (project_id, bucket);

	CREATE TABLE php_stall_hours (
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		source TEXT NOT NULL,
		hour BIGINT NOT NULL,
		frame TEXT NOT NULL,
		code TEXT NOT NULL,
		n BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY (project_id, source, hour, frame, code)
	);
	CREATE INDEX php_stall_hours_hour ON php_stall_hours (project_id, hour);

	CREATE TABLE host_minutes (
		host_id BIGINT NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
		bucket BIGINT NOT NULL,
		cpu {real}, load1 {real}, mem_used BIGINT, mem_total BIGINT, disk_used BIGINT, disk_total BIGINT,
		PRIMARY KEY (host_id, bucket)
	);

	CREATE TABLE route_hours (
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		route TEXT NOT NULL,
		hour BIGINT NOT NULL,
		n BIGINT NOT NULL DEFAULT 0, sum {real} NOT NULL DEFAULT 0, max {real} NOT NULL DEFAULT 0,
		queries BIGINT NOT NULL DEFAULT 0, db_sum {real} NOT NULL DEFAULT 0, e5 BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY (project_id, route, hour)
	);

	CREATE TABLE route_queries (
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		route TEXT NOT NULL,
		fingerprint_id BIGINT NOT NULL REFERENCES query_fingerprints(id) ON DELETE CASCADE,
		n BIGINT NOT NULL DEFAULT 0, sum {real} NOT NULL DEFAULT 0, requests BIGINT NOT NULL DEFAULT 0,
		last_seen_at BIGINT NOT NULL,
		PRIMARY KEY (project_id, route, fingerprint_id)
	);

	CREATE TABLE job_hours (
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		kind TEXT NOT NULL,
		name TEXT NOT NULL,
		hour BIGINT NOT NULL,
		n BIGINT NOT NULL DEFAULT 0, failed BIGINT NOT NULL DEFAULT 0, sum {real} NOT NULL DEFAULT 0, max {real} NOT NULL DEFAULT 0,
		PRIMARY KEY (project_id, kind, name, hour)
	);

	CREATE TABLE command_runs (
		id {id},
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		host TEXT NOT NULL,
		name TEXT NOT NULL,
		started_at BIGINT NOT NULL,
		finished_at BIGINT,
		exit_code BIGINT,
		UNIQUE (project_id, host, name, started_at)
	);
	CREATE INDEX command_runs_started ON command_runs (project_id, started_at);

	CREATE TABLE deploys (
		id {id},
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		version TEXT NOT NULL DEFAULT '',
		commit_sha TEXT NOT NULL DEFAULT '',
		author TEXT NOT NULL DEFAULT '',
		message TEXT NOT NULL DEFAULT '',
		url TEXT NOT NULL DEFAULT '',
		deployed_at BIGINT NOT NULL,
		impact TEXT
	);
	CREATE INDEX deploys_at ON deploys (project_id, deployed_at);

	CREATE TABLE score_days (
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		day TEXT NOT NULL,
		score {real},
		partial {bool} NOT NULL DEFAULT FALSE,
		closed {bool} NOT NULL DEFAULT FALSE,
		hours {real} NOT NULL DEFAULT 0,
		components TEXT NOT NULL DEFAULT '[]',
		metrics TEXT NOT NULL DEFAULT '{}',
		updated_at BIGINT NOT NULL,
		PRIMARY KEY (project_id, day)
	);

	CREATE TABLE unlocked_badges (
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		badge TEXT NOT NULL,
		day TEXT NOT NULL,
		created_at BIGINT NOT NULL,
		PRIMARY KEY (project_id, badge)
	);

	CREATE TABLE missions (
		id {id},
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		type TEXT NOT NULL,
		kind TEXT NOT NULL,
		target TEXT NOT NULL,
		label TEXT NOT NULL,
		baseline {real} NOT NULL,
		current {real},
		points BIGINT NOT NULL,
		status TEXT NOT NULL,
		good_windows BIGINT NOT NULL DEFAULT 0,
		deploy_id BIGINT REFERENCES deploys(id) ON DELETE SET NULL,
		person TEXT NOT NULL DEFAULT '',
		opened_at BIGINT NOT NULL,
		due_at BIGINT,
		checked_at BIGINT NOT NULL,
		closed_at BIGINT
	);
	CREATE INDEX missions_status ON missions (project_id, status);

	CREATE TABLE point_entries (
		id {id},
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		points BIGINT NOT NULL,
		reason TEXT NOT NULL,
		person TEXT NOT NULL DEFAULT '',
		subject_type TEXT NOT NULL DEFAULT '',
		subject_id BIGINT NOT NULL DEFAULT 0,
		created_at BIGINT NOT NULL
	);
	CREATE INDEX point_entries_created ON point_entries (project_id, created_at);

	CREATE TABLE activities (
		id {id},
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		type TEXT NOT NULL,
		icon TEXT NOT NULL DEFAULT '',
		title TEXT NOT NULL,
		data TEXT NOT NULL DEFAULT '{}',
		notified {bool} NOT NULL DEFAULT FALSE,
		created_at BIGINT NOT NULL
	);
	CREATE INDEX activities_created ON activities (project_id, created_at);`,

	`CREATE TABLE metric_minutes (
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		name TEXT NOT NULL,
		bucket BIGINT NOT NULL,
		kind TEXT NOT NULL,
		count BIGINT NOT NULL DEFAULT 0, sum {real} NOT NULL DEFAULT 0, min {real} NOT NULL DEFAULT 0, max {real} NOT NULL DEFAULT 0, last {real} NOT NULL DEFAULT 0,
		h0 BIGINT NOT NULL DEFAULT 0, h1 BIGINT NOT NULL DEFAULT 0, h2 BIGINT NOT NULL DEFAULT 0, h3 BIGINT NOT NULL DEFAULT 0,
		h4 BIGINT NOT NULL DEFAULT 0, h5 BIGINT NOT NULL DEFAULT 0, h6 BIGINT NOT NULL DEFAULT 0, h7 BIGINT NOT NULL DEFAULT 0,
		h8 BIGINT NOT NULL DEFAULT 0, h9 BIGINT NOT NULL DEFAULT 0, h10 BIGINT NOT NULL DEFAULT 0, h11 BIGINT NOT NULL DEFAULT 0,
		h12 BIGINT NOT NULL DEFAULT 0, h13 BIGINT NOT NULL DEFAULT 0, h14 BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY (project_id, name, bucket)
	);
	CREATE INDEX metric_minutes_bucket ON metric_minutes (project_id, bucket);`,

	`CREATE TABLE ingest_queue (
		id {id},
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		payload {blob} NOT NULL,
		gzipped {bool} NOT NULL DEFAULT TRUE,
		received_at BIGINT NOT NULL,
		attempts BIGINT NOT NULL DEFAULT 0,
		available_at BIGINT NOT NULL,
		dead {bool} NOT NULL DEFAULT FALSE,
		error TEXT
	);
	CREATE INDEX ingest_queue_ready ON ingest_queue (dead, available_at, id);`,

	`-- if-missing-column: host_minutes.cpu_iowait
	ALTER TABLE host_minutes ADD COLUMN cpu_iowait {real};
	ALTER TABLE host_minutes ADD COLUMN cpu_steal {real};
	ALTER TABLE host_minutes ADD COLUMN load5 {real};
	ALTER TABLE host_minutes ADD COLUMN load15 {real};
	ALTER TABLE host_minutes ADD COLUMN cpus BIGINT;
	ALTER TABLE host_minutes ADD COLUMN swap_used BIGINT;
	ALTER TABLE host_minutes ADD COLUMN swap_total BIGINT;
	ALTER TABLE host_minutes ADD COLUMN swap_out_pages BIGINT;
	ALTER TABLE host_minutes ADD COLUMN oom_kills BIGINT;
	ALTER TABLE host_minutes ADD COLUMN disk_read_bps {real};
	ALTER TABLE host_minutes ADD COLUMN disk_write_bps {real};
	ALTER TABLE host_minutes ADD COLUMN net_rx_bps {real};
	ALTER TABLE host_minutes ADD COLUMN net_tx_bps {real};
	ALTER TABLE host_minutes ADD COLUMN top_processes TEXT;`,

	`-- if-missing-column: hosts.addresses
	ALTER TABLE hosts ADD COLUMN addresses TEXT NOT NULL DEFAULT '[]';
	ALTER TABLE hosts ADD COLUMN listening TEXT NOT NULL DEFAULT '[]';
	ALTER TABLE hosts ADD COLUMN network_at BIGINT;
	CREATE TABLE host_connections (
		host_id BIGINT NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
		direction TEXT NOT NULL,
		remote_ip TEXT NOT NULL,
		port BIGINT NOT NULL,
		service TEXT NOT NULL DEFAULT '',
		count BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY (host_id, direction, remote_ip, port)
	);`,

	`CREATE TABLE mission_tasks (
		id {id},
		mission_id BIGINT NOT NULL REFERENCES missions(id) ON DELETE CASCADE,
		integration TEXT NOT NULL,
		provider TEXT NOT NULL,
		external_id TEXT NOT NULL DEFAULT '',
		url TEXT NOT NULL DEFAULT '',
		assignee TEXT NOT NULL DEFAULT '',
		created_at BIGINT NOT NULL
	);
	CREATE INDEX mission_tasks_mission ON mission_tasks (mission_id);`,

	`CREATE TABLE access_tokens (
		id {id},
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		kind TEXT NOT NULL,
		name TEXT NOT NULL,
		prefix TEXT NOT NULL,
		token_hash TEXT NOT NULL UNIQUE,
		user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
		created_at BIGINT NOT NULL,
		last_used_at BIGINT,
		revoked_at BIGINT
	);
	CREATE INDEX access_tokens_project ON access_tokens (project_id, kind);
	ALTER TABLE hosts ADD COLUMN token_id BIGINT;
	ALTER TABLE ingest_queue ADD COLUMN token_id BIGINT;`,

	`-- if-missing-column: missions.effort
	ALTER TABLE missions ADD COLUMN effort TEXT NOT NULL DEFAULT '';`,
	// What each source of a machine can do, as last reported by its agent.
	`CREATE TABLE host_checks (
		host_id BIGINT NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
		source TEXT NOT NULL,
		items TEXT NOT NULL,
		facts TEXT NOT NULL DEFAULT '{}',
		checked_at BIGINT NOT NULL,
		PRIMARY KEY (host_id, source)
	);`,
	// These steps are written to be safe on a database where they ran under another number:
	// a migration that already ran must never be edited, and this one was (the lesson stays here).
	// Which process holds each connection ("php-fpm:www", "nginx", "php artisan queue:work"):
	// part of the key, since two services of a machine often talk to the same address. The table
	// holds the last snapshot of every agent and is rewritten within a minute.
	`DROP TABLE IF EXISTS host_connections;`,
	`CREATE TABLE IF NOT EXISTS host_connections (
		host_id BIGINT NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
		direction TEXT NOT NULL,
		remote_ip TEXT NOT NULL,
		port BIGINT NOT NULL,
		process TEXT NOT NULL DEFAULT '',
		service TEXT NOT NULL DEFAULT '',
		count BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY (host_id, direction, remote_ip, port, process)
	);`,
	// Rebuilt with the process in the key: a migration already applied elsewhere must never be
	// edited, so the change lives in its own step. The table holds the last snapshot of every
	// agent and is rewritten within a minute.
	`DROP TABLE IF EXISTS host_connections;`,
	`CREATE TABLE host_connections (
		host_id BIGINT NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
		direction TEXT NOT NULL,
		remote_ip TEXT NOT NULL,
		port BIGINT NOT NULL,
		process TEXT NOT NULL DEFAULT '',
		service TEXT NOT NULL DEFAULT '',
		count BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY (host_id, direction, remote_ip, port, process)
	);`,
	// A metric belongs to the machine that reported it: four Redis servers called "cache" are
	// four series, not one sum. Rows written before this migration keep host_id 0 (unknown
	// machine) instead of being thrown away: retention removes them within two weeks.
	`-- if-missing-column: metric_minutes.host_id
	CREATE TABLE metric_minutes_v2 (
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		host_id BIGINT NOT NULL DEFAULT 0,
		name TEXT NOT NULL,
		bucket BIGINT NOT NULL,
		kind TEXT NOT NULL,
		count BIGINT NOT NULL DEFAULT 0, sum {real} NOT NULL DEFAULT 0, min {real} NOT NULL DEFAULT 0, max {real} NOT NULL DEFAULT 0, last {real} NOT NULL DEFAULT 0,
		h0 BIGINT NOT NULL DEFAULT 0, h1 BIGINT NOT NULL DEFAULT 0, h2 BIGINT NOT NULL DEFAULT 0, h3 BIGINT NOT NULL DEFAULT 0,
		h4 BIGINT NOT NULL DEFAULT 0, h5 BIGINT NOT NULL DEFAULT 0, h6 BIGINT NOT NULL DEFAULT 0, h7 BIGINT NOT NULL DEFAULT 0,
		h8 BIGINT NOT NULL DEFAULT 0, h9 BIGINT NOT NULL DEFAULT 0, h10 BIGINT NOT NULL DEFAULT 0, h11 BIGINT NOT NULL DEFAULT 0,
		h12 BIGINT NOT NULL DEFAULT 0, h13 BIGINT NOT NULL DEFAULT 0, h14 BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY (project_id, host_id, name, bucket)
	);
	INSERT INTO metric_minutes_v2 (project_id, host_id, name, bucket, kind, count, sum, min, max, last,
		h0, h1, h2, h3, h4, h5, h6, h7, h8, h9, h10, h11, h12, h13, h14)
		SELECT project_id, 0, name, bucket, kind, count, sum, min, max, last,
		h0, h1, h2, h3, h4, h5, h6, h7, h8, h9, h10, h11, h12, h13, h14 FROM metric_minutes;
	DROP TABLE metric_minutes;
	ALTER TABLE metric_minutes_v2 RENAME TO metric_minutes;
	CREATE INDEX metric_minutes_bucket ON metric_minutes (project_id, bucket);`,
	// Names the team gives to machines and services on the infrastructure map.
	`CREATE TABLE IF NOT EXISTS node_labels (
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		node_id TEXT NOT NULL,
		label TEXT NOT NULL,
		updated_at BIGINT NOT NULL,
		PRIMARY KEY (project_id, node_id)
	);`,
	// Where the team put a node on the infrastructure map. NULL means the automatic layout.
	`-- if-missing-column: node_labels.x
	ALTER TABLE node_labels ADD COLUMN x {real};
	ALTER TABLE node_labels ADD COLUMN y {real};`,
	// The same rebuild again, at the end of the list, for the databases that had already
	// recorded this version number when the one above was inserted in the middle of the list
	// (2026-09-18, found in production). It does nothing where the column is already there.
	`-- if-missing-column: metric_minutes.host_id
	DROP TABLE IF EXISTS metric_minutes_v2;
	CREATE TABLE metric_minutes_v2 (
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		host_id BIGINT NOT NULL DEFAULT 0,
		name TEXT NOT NULL,
		bucket BIGINT NOT NULL,
		kind TEXT NOT NULL,
		count BIGINT NOT NULL DEFAULT 0, sum {real} NOT NULL DEFAULT 0, min {real} NOT NULL DEFAULT 0, max {real} NOT NULL DEFAULT 0, last {real} NOT NULL DEFAULT 0,
		h0 BIGINT NOT NULL DEFAULT 0, h1 BIGINT NOT NULL DEFAULT 0, h2 BIGINT NOT NULL DEFAULT 0, h3 BIGINT NOT NULL DEFAULT 0,
		h4 BIGINT NOT NULL DEFAULT 0, h5 BIGINT NOT NULL DEFAULT 0, h6 BIGINT NOT NULL DEFAULT 0, h7 BIGINT NOT NULL DEFAULT 0,
		h8 BIGINT NOT NULL DEFAULT 0, h9 BIGINT NOT NULL DEFAULT 0, h10 BIGINT NOT NULL DEFAULT 0, h11 BIGINT NOT NULL DEFAULT 0,
		h12 BIGINT NOT NULL DEFAULT 0, h13 BIGINT NOT NULL DEFAULT 0, h14 BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY (project_id, host_id, name, bucket)
	);
	INSERT INTO metric_minutes_v2 (project_id, host_id, name, bucket, kind, count, sum, min, max, last,
		h0, h1, h2, h3, h4, h5, h6, h7, h8, h9, h10, h11, h12, h13, h14)
		SELECT project_id, 0, name, bucket, kind, count, sum, min, max, last,
		h0, h1, h2, h3, h4, h5, h6, h7, h8, h9, h10, h11, h12, h13, h14 FROM metric_minutes;
	DROP TABLE metric_minutes;
	ALTER TABLE metric_minutes_v2 RENAME TO metric_minutes;
	CREATE INDEX metric_minutes_bucket ON metric_minutes (project_id, bucket);`,
	// A slow query belongs to the machine that saw it: the same statement on two database
	// servers is two rows, so "slow things" can be read one machine at a time. The project
	// totals do not change: they still group by fingerprint and source, across machines.
	`-- if-missing-column: query_hours.host_id
	DROP TABLE IF EXISTS query_hours_v2;
	CREATE TABLE query_hours_v2 (
		fingerprint_id BIGINT NOT NULL REFERENCES query_fingerprints(id) ON DELETE CASCADE,
		source TEXT NOT NULL,
		host_id BIGINT NOT NULL DEFAULT 0,
		hour BIGINT NOT NULL,
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		n BIGINT NOT NULL DEFAULT 0, sum {real} NOT NULL DEFAULT 0, max {real} NOT NULL DEFAULT 0, rows BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY (fingerprint_id, source, host_id, hour)
	);
	INSERT INTO query_hours_v2 (fingerprint_id, source, host_id, hour, project_id, n, sum, max, rows)
		SELECT fingerprint_id, source, 0, hour, project_id, n, sum, max, rows FROM query_hours;
	DROP TABLE query_hours;
	ALTER TABLE query_hours_v2 RENAME TO query_hours;
	CREATE INDEX query_hours_hour ON query_hours (project_id, hour);
	CREATE INDEX query_hours_host ON query_hours (project_id, host_id, hour);`,
	// The names MySQL writes with backticks around them were stored that way: a database is
	// called contract_service, not `contract_service`.
	`UPDATE query_fingerprints SET db_name = REPLACE(db_name, '` + "`" + `', ''), db_user = REPLACE(db_user, '` + "`" + `', '');`,
	// Which host each source answers for. A web server on one machine and the application on
	// another report the same requests, and only by the host can they be recognised as the same.
	`CREATE TABLE IF NOT EXISTS source_sites (
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		source TEXT NOT NULL,
		site TEXT NOT NULL,
		last_seen_at BIGINT NOT NULL,
		PRIMARY KEY (project_id, source, site)
	);`,
	// Each endpoint's own histogram: the speed index is read from histograms, and taking an
	// excluded endpoint out of it needs to know how its requests were spread. Rows written
	// before have zeros and are estimated from their totals.
	`-- if-missing-column: endpoint_hours.h0
	ALTER TABLE endpoint_hours ADD COLUMN h0 BIGINT NOT NULL DEFAULT 0;
	ALTER TABLE endpoint_hours ADD COLUMN h1 BIGINT NOT NULL DEFAULT 0;
	ALTER TABLE endpoint_hours ADD COLUMN h2 BIGINT NOT NULL DEFAULT 0;
	ALTER TABLE endpoint_hours ADD COLUMN h3 BIGINT NOT NULL DEFAULT 0;
	ALTER TABLE endpoint_hours ADD COLUMN h4 BIGINT NOT NULL DEFAULT 0;
	ALTER TABLE endpoint_hours ADD COLUMN h5 BIGINT NOT NULL DEFAULT 0;
	ALTER TABLE endpoint_hours ADD COLUMN h6 BIGINT NOT NULL DEFAULT 0;
	ALTER TABLE endpoint_hours ADD COLUMN h7 BIGINT NOT NULL DEFAULT 0;
	ALTER TABLE endpoint_hours ADD COLUMN h8 BIGINT NOT NULL DEFAULT 0;
	ALTER TABLE endpoint_hours ADD COLUMN h9 BIGINT NOT NULL DEFAULT 0;
	ALTER TABLE endpoint_hours ADD COLUMN h10 BIGINT NOT NULL DEFAULT 0;
	ALTER TABLE endpoint_hours ADD COLUMN h11 BIGINT NOT NULL DEFAULT 0;
	ALTER TABLE endpoint_hours ADD COLUMN h12 BIGINT NOT NULL DEFAULT 0;
	ALTER TABLE endpoint_hours ADD COLUMN h13 BIGINT NOT NULL DEFAULT 0;
	ALTER TABLE endpoint_hours ADD COLUMN h14 BIGINT NOT NULL DEFAULT 0;`,
	// Endpoints the team took out of the score, the debt and the missions, with the reason.
	// until is NULL for "until we change our mind".
	`CREATE TABLE IF NOT EXISTS exclusions (
		id {id},
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		endpoint TEXT NOT NULL,
		reason TEXT NOT NULL,
		note TEXT NOT NULL DEFAULT '',
		created_by TEXT NOT NULL DEFAULT '',
		created_at BIGINT NOT NULL,
		until BIGINT
	);
	CREATE UNIQUE INDEX exclusions_endpoint ON exclusions (project_id, endpoint);`,
	// Time the requests of a route spent waiting on outbound HTTP calls, in total and per host.
	`-- if-missing-column: route_hours.ext_sum
	ALTER TABLE route_hours ADD COLUMN ext_sum {real} NOT NULL DEFAULT 0;
	ALTER TABLE route_hours ADD COLUMN ext_requests BIGINT NOT NULL DEFAULT 0;
	CREATE TABLE IF NOT EXISTS route_calls (
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		route TEXT NOT NULL,
		host TEXT NOT NULL,
		hour BIGINT NOT NULL,
		n BIGINT NOT NULL DEFAULT 0, sum {real} NOT NULL DEFAULT 0, max {real} NOT NULL DEFAULT 0,
		errors BIGINT NOT NULL DEFAULT 0, requests BIGINT NOT NULL DEFAULT 0,
		origin TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (project_id, route, host, hour)
	);
	CREATE INDEX route_calls_hour ON route_calls (project_id, hour);`,
	// The life of a mission after it is opened: who took it and since when, when the fix went
	// online and with which deploy, and after it is completed whether it held (confirmed) or came
	// back (relapsed, with the boss that reopened it).
	`-- if-missing-column: missions.claimed_at
	ALTER TABLE missions ADD COLUMN claimed_at BIGINT;
	ALTER TABLE missions ADD COLUMN shipped_at BIGINT;
	ALTER TABLE missions ADD COLUMN shipped_deploy_id BIGINT;
	ALTER TABLE missions ADD COLUMN verdict TEXT NOT NULL DEFAULT '';
	ALTER TABLE missions ADD COLUMN verdict_at BIGINT;
	ALTER TABLE missions ADD COLUMN held_windows BIGINT NOT NULL DEFAULT 0;
	ALTER TABLE missions ADD COLUMN bad_windows BIGINT NOT NULL DEFAULT 0;
	ALTER TABLE missions ADD COLUMN relapse_of BIGINT;`,
	// A mission is judged per request on the project's busy hours: its starting point and its
	// latest reading in seconds per request, next to the seconds per window shown as its cost.
	`-- if-missing-column: missions.baseline_rate
	ALTER TABLE missions ADD COLUMN baseline_rate {real};
	ALTER TABLE missions ADD COLUMN current_rate {real};`,
	// What the team chose to stop hearing about: a machine that stays installed and keeps
	// counting, and one piece of the doctor's advice on one query (code and table).
	`-- if-missing-column: hosts.muted_at
	ALTER TABLE hosts ADD COLUMN muted_at BIGINT;
	ALTER TABLE hosts ADD COLUMN muted_by TEXT NOT NULL DEFAULT '';
	ALTER TABLE hosts ADD COLUMN muted_note TEXT NOT NULL DEFAULT '';
	CREATE TABLE IF NOT EXISTS muted_diagnoses (
		id {id},
		project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		hash TEXT NOT NULL,
		code TEXT NOT NULL,
		table_name TEXT NOT NULL DEFAULT '',
		note TEXT NOT NULL DEFAULT '',
		created_by TEXT NOT NULL DEFAULT '',
		created_at BIGINT NOT NULL
	);
	CREATE UNIQUE INDEX muted_diagnoses_key ON muted_diagnoses (project_id, hash, code, table_name);`,
	// Past the first day the minutes of a machine become one row per hour: cpu keeps the hour's
	// average and cpu_peak its highest minute, so the charts still show the spikes.
	`-- if-missing-column: host_minutes.cpu_peak
	ALTER TABLE host_minutes ADD COLUMN cpu_peak {real};`,
	// Anything that costs time can be left out of the score, not only an endpoint: a query of a
	// library, a PHP frame inside a vendor's code. And a regression can be waved away on its own,
	// without taking its target out: quiet_until keeps it from coming back the next day.
	`-- if-missing-column: exclusions.kind
	ALTER TABLE exclusions RENAME COLUMN endpoint TO target;
	ALTER TABLE exclusions ADD COLUMN kind TEXT NOT NULL DEFAULT 'endpoint';
	DROP INDEX exclusions_endpoint;
	CREATE UNIQUE INDEX exclusions_target ON exclusions (project_id, kind, target);
	ALTER TABLE missions ADD COLUMN quiet_until BIGINT;`,
}

func (d *DB) Migrate(ctx context.Context) error {
	if _, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version BIGINT PRIMARY KEY)`); err != nil {
		return err
	}
	for i, m := range migrations {
		version := i + 1
		var exists int
		_ = d.QueryRowContext(ctx, d.Rebind(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`), version).Scan(&exists)
		if exists > 0 {
			continue
		}
		done, err := d.alreadyDone(ctx, m)
		if err != nil {
			return err
		}
		if done {
			if _, err := d.ExecContext(ctx, d.Rebind(`INSERT INTO schema_migrations (version) VALUES (?)`), version); err != nil {
				return err
			}
			continue
		}
		err = d.Tx(ctx, func(q Q) error {
			for _, stmt := range strings.Split(d.expand(m), ";") {
				if strings.TrimSpace(stmt) == "" {
					continue
				}
				if _, err := q.Exec(ctx, stmt); err != nil {
					return fmt.Errorf("migration %d: %w\n%s", version, err, stmt)
				}
			}
			_, err := q.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES (?)`, version)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// alreadyDone answers for a migration that starts with "-- if-missing-column: table.column":
// it is recorded and skipped when that column is already there. Versions are positions in the
// list, so a migration inserted in the middle takes a number that older databases have already
// recorded and is never run on them; the repair is a copy of it at the end of the list, which
// must then do nothing on the databases that did run the original.
func (d *DB) alreadyDone(ctx context.Context, migration string) (bool, error) {
	const marker = "-- if-missing-column:"
	i := strings.Index(migration, marker)
	if i < 0 {
		return false, nil
	}
	fields := strings.Fields(migration[i+len(marker):])
	if len(fields) == 0 {
		return false, fmt.Errorf("%s needs table.column", marker)
	}
	table, column, ok := strings.Cut(fields[0], ".")
	if !ok {
		return false, fmt.Errorf("%s needs table.column, got %q", marker, fields[0])
	}
	q := `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`
	if d.Dialect == Postgres {
		q = `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = ? AND column_name = ?`
	}
	var n int
	if err := d.QueryRowContext(ctx, d.Rebind(q), table, column).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

func (d *DB) expand(sql string) string {
	r := strings.NewReplacer("{id}", "INTEGER PRIMARY KEY AUTOINCREMENT", "{real}", "REAL", "{bool}", "BOOLEAN", "{blob}", "BLOB")
	if d.Dialect == Postgres {
		r = strings.NewReplacer("{id}", "BIGSERIAL PRIMARY KEY", "{real}", "DOUBLE PRECISION", "{bool}", "BOOLEAN", "{blob}", "BYTEA")
	}
	return r.Replace(sql)
}
