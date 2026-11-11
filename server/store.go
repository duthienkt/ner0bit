package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Store là kho SQLite của server (WAL, một file).
type Store struct{ db *sql.DB }

// schemaV1 gồm toàn bộ bảng ở spec §6.2; các task sau chỉ thêm truy vấn.
var schemaV1 = []string{
	`CREATE TABLE heartbeats (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		device_id TEXT NOT NULL,
		received_at TEXT NOT NULL,
		raw_json TEXT NOT NULL)`,
	`CREATE INDEX heartbeats_received ON heartbeats(received_at)`,
	`CREATE TABLE events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		device_id TEXT NOT NULL,
		at TEXT NOT NULL,
		type TEXT NOT NULL,
		source TEXT NOT NULL DEFAULT '',
		reason TEXT NOT NULL DEFAULT '',
		received_at TEXT NOT NULL)`,
	`CREATE INDEX events_at ON events(at)`,
	`CREATE TABLE rain_snapshots (
		data_ts TEXT PRIMARY KEY,
		fetched_at TEXT NOT NULL,
		vday TEXT NOT NULL,
		sum_depth REAL NOT NULL,
		level TEXT)`,
	`CREATE INDEX rain_snapshots_vday ON rain_snapshots(vday)`,
	`CREATE TABLE metar_obs (
		obs_time TEXT PRIMARY KEY,
		temp REAL,
		dewp REAL,
		clouds_json TEXT NOT NULL DEFAULT '[]',
		raw TEXT NOT NULL DEFAULT '')`,
	`CREATE TABLE days (
		vday TEXT PRIMARY KEY,
		carry_in REAL NOT NULL DEFAULT 0,
		used_carry REAL NOT NULL DEFAULT 0,
		used_rain REAL NOT NULL DEFAULT 0,
		used_sun REAL NOT NULL DEFAULT 0,
		sun_s REAL,
		et REAL,
		final_sum REAL,
		closed_at TEXT)`,
	`CREATE TABLE blocks (
		vday TEXT NOT NULL,
		slot_s INTEGER NOT NULL,
		phase TEXT NOT NULL,
		run_s INTEGER NOT NULL,
		state TEXT NOT NULL,
		decided_at TEXT,
		inputs_json TEXT NOT NULL DEFAULT '{}',
		reason TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (vday, slot_s))`,
	`CREATE TABLE sessions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		date TEXT NOT NULL,
		start_s INTEGER NOT NULL,
		run_s INTEGER NOT NULL,
		source TEXT NOT NULL,
		state TEXT NOT NULL,
		vday TEXT,
		slot_s INTEGER,
		note TEXT NOT NULL DEFAULT '',
		reason TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL)`,
	`CREATE INDEX sessions_date ON sessions(date)`,
	`CREATE TABLE schedule_daily (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		line TEXT NOT NULL,
		updated_at TEXT NOT NULL)`,
	`CREATE TABLE settings (
		key TEXT PRIMARY KEY,
		value_json TEXT NOT NULL)`,
	`CREATE TABLE warnings (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		at TEXT NOT NULL,
		source TEXT NOT NULL,
		message TEXT NOT NULL)`,
	`CREATE INDEX warnings_at ON warnings(at)`,
}

// migrations[i] nâng DB từ user_version i lên i+1.
var migrations = [][]string{schemaV1}

// OpenStore mở (và tạo nếu cần) DB tại path, bật WAL và chạy migration.
func OpenStore(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("mở DB: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite một writer; tránh SQLITE_BUSY giữa các kết nối
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return fmt.Errorf("đọc user_version: %w", err)
	}
	if v > len(migrations) {
		return fmt.Errorf("DB có user_version=%d, mới hơn bản server này (%d)", v, len(migrations))
	}
	for ; v < len(migrations); v++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		for _, stmt := range migrations[v] {
			if _, err := tx.Exec(stmt); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d: %w", v+1, err)
			}
		}
		// PRAGMA không nhận tham số bind; v+1 là số nguyên nội bộ.
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", v+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", v+1, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

const settingsParamsKey = "params"

// Params trả tham số đã lưu; chưa có thì trả DefaultParams().
func (s *Store) Params() (Params, error) { return paramsQ(s.db) }

// paramsQ đọc tham số qua q — DB hoặc transaction.
func paramsQ(q querier) (Params, error) {
	var raw string
	err := q.QueryRow("SELECT value_json FROM settings WHERE key=?", settingsParamsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultParams(), nil
	}
	if err != nil {
		return Params{}, fmt.Errorf("đọc tham số: %w", err)
	}
	p := DefaultParams()
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return Params{}, fmt.Errorf("tham số trong DB hỏng: %w", err)
	}
	return p, nil
}

// SetParams kiểm tra rồi lưu tham số.
func (s *Store) SetParams(p Params) error {
	if err := p.Validate(); err != nil {
		return err
	}
	return setParamsQ(s.db, p)
}

// setParamsQ ghi tham số (không kiểm tra) qua x — DB hoặc transaction.
func setParamsQ(x execer, p Params) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = x.Exec(`INSERT INTO settings(key, value_json) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json`, settingsParamsKey, string(raw))
	if err != nil {
		return fmt.Errorf("ghi tham số: %w", err)
	}
	return nil
}

// Daily trả lịch dự phòng (dòng 1); "" nếu chưa đặt.
func (s *Store) Daily() (string, error) {
	var line string
	err := s.db.QueryRow("SELECT line FROM schedule_daily WHERE id=1").Scan(&line)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("đọc lịch dự phòng: %w", err)
	}
	return line, nil
}

// SetDaily chuẩn hoá và lưu lịch dự phòng; phải có ít nhất 1 mốc.
func (s *Store) SetDaily(line string) error {
	norm, err := normalizeLine(line)
	if err != nil {
		return err
	}
	if norm == "" {
		return fmt.Errorf("lịch dự phòng phải có ít nhất 1 mốc")
	}
	_, err = s.db.Exec(`INSERT INTO schedule_daily(id, line, updated_at) VALUES(1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET line=excluded.line, updated_at=excluded.updated_at`,
		norm, time.Now().In(vietnam).Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("ghi lịch dự phòng: %w", err)
	}
	return nil
}
