package store

// schemaSQL is the idempotent DDL applied on Open. All tables are CREATEd IF
// NOT EXISTS so reopening an existing database (the recovery path) preserves
// persisted state.
const schemaSQL = `
CREATE TABLE IF NOT EXISTS meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS crew_members (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	name      TEXT NOT NULL,
	role      TEXT NOT NULL,
	home_base TEXT NOT NULL DEFAULT '',
	home_tz   TEXT NOT NULL,
	active    INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL,
	UNIQUE(name, role)
);
CREATE INDEX IF NOT EXISTS idx_crew_role ON crew_members(role);
CREATE INDEX IF NOT EXISTS idx_crew_active ON crew_members(active);

CREATE TABLE IF NOT EXISTS aircraft_types (
	code                TEXT PRIMARY KEY,
	has_rest_facility   INTEGER NOT NULL DEFAULT 0,
	rest_facility_class TEXT NOT NULL DEFAULT 'NONE'
);

CREATE TABLE IF NOT EXISTS trips (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	crew_id    INTEGER NOT NULL,
	planned    INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL,
	FOREIGN KEY (crew_id) REFERENCES crew_members(id)
);
CREATE INDEX IF NOT EXISTS idx_trips_crew ON trips(crew_id);

CREATE TABLE IF NOT EXISTS flight_segments (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	trip_id        INTEGER NOT NULL,
	aircraft_type  TEXT NOT NULL,
	dep_airport    TEXT NOT NULL DEFAULT '',
	arr_airport    TEXT NOT NULL DEFAULT '',
	scheduled_dep  TEXT NOT NULL,
	scheduled_arr  TEXT NOT NULL,
	actual_dep     TEXT,
	actual_arr     TEXT,
	block_time_min INTEGER NOT NULL,
	FOREIGN KEY (trip_id) REFERENCES trips(id),
	FOREIGN KEY (aircraft_type) REFERENCES aircraft_types(code)
);
CREATE INDEX IF NOT EXISTS idx_segments_trip ON flight_segments(trip_id);
CREATE INDEX IF NOT EXISTS idx_segments_sdep ON flight_segments(scheduled_dep);

CREATE TABLE IF NOT EXISTS duty_periods (
	id                       INTEGER PRIMARY KEY AUTOINCREMENT,
	crew_id                  INTEGER NOT NULL,
	trip_id                  INTEGER NOT NULL,
	report_time              TEXT NOT NULL,
	release_time             TEXT NOT NULL,
	is_augmented             INTEGER NOT NULL DEFAULT 0,
	split_break_min          INTEGER NOT NULL DEFAULT 0,
	status                   TEXT NOT NULL DEFAULT 'OPEN',
	unforeseen_extension_min INTEGER NOT NULL DEFAULT 0,
	owe_augmented_rest       INTEGER NOT NULL DEFAULT 0,
	FOREIGN KEY (crew_id) REFERENCES crew_members(id),
	FOREIGN KEY (trip_id) REFERENCES trips(id)
);
CREATE INDEX IF NOT EXISTS idx_duty_crew ON duty_periods(crew_id);
CREATE INDEX IF NOT EXISTS idx_duty_status ON duty_periods(status);
CREATE INDEX IF NOT EXISTS idx_duty_report ON duty_periods(report_time);

CREATE TABLE IF NOT EXISTS duty_period_segments (
	duty_period_id INTEGER NOT NULL,
	segment_id     INTEGER NOT NULL,
	seq            INTEGER NOT NULL,
	PRIMARY KEY (duty_period_id, segment_id)
);
CREATE INDEX IF NOT EXISTS idx_dps_segment ON duty_period_segments(segment_id);

CREATE TABLE IF NOT EXISTS rest_periods (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	crew_id       INTEGER NOT NULL,
	start         TEXT NOT NULL,
	end           TEXT NOT NULL,
	rest_type     TEXT NOT NULL,
	duration_min  INTEGER NOT NULL,
	covers_weekly INTEGER NOT NULL DEFAULT 0,
	FOREIGN KEY (crew_id) REFERENCES crew_members(id)
);
CREATE INDEX IF NOT EXISTS idx_rest_crew ON rest_periods(crew_id);
CREATE INDEX IF NOT EXISTS idx_rest_start ON rest_periods(start);

CREATE TABLE IF NOT EXISTS compliance_events (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	crew_id     INTEGER NOT NULL,
	ts          TEXT NOT NULL,
	kind        TEXT NOT NULL,
	payload_json TEXT NOT NULL DEFAULT '',
	FOREIGN KEY (crew_id) REFERENCES crew_members(id)
);
CREATE INDEX IF NOT EXISTS idx_events_crew_ts ON compliance_events(crew_id, ts);

CREATE TABLE IF NOT EXISTS legality_evaluations (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	crew_id       INTEGER NOT NULL,
	trip_id       INTEGER,      -- nullable: 0/NULL for evaluations of proposed (non-persisted) trips
	evaluated_at TEXT NOT NULL,
	verdict       TEXT NOT NULL,
	violations_json TEXT NOT NULL DEFAULT '[]',
	metrics_json   TEXT NOT NULL DEFAULT '{}',
	FOREIGN KEY (crew_id) REFERENCES crew_members(id),
	FOREIGN KEY (trip_id) REFERENCES trips(id)
);
CREATE INDEX IF NOT EXISTS idx_eval_crew ON legality_evaluations(crew_id);
CREATE INDEX IF NOT EXISTS idx_eval_trip ON legality_evaluations(trip_id);

CREATE TABLE IF NOT EXISTS cumulative_snapshots (
	crew_id      INTEGER NOT NULL,
	window_kind  TEXT NOT NULL,
	as_of        TEXT NOT NULL,
	cumulative_min INTEGER NOT NULL,
	PRIMARY KEY (crew_id, window_kind)
);
`

// Meta keys.
const (
	MetaKeySchemaVersion = "schema_version"
)

// WindowKind enumerates the rolling-window snapshot kinds (R4/R5/R6).
const (
	WindowKind28d  = "28d"
	WindowKind168h = "168h"
	WindowKind365d = "365d"
)
