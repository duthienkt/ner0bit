package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// RecordHeartbeat ghi heartbeat nguyên bản và từng event của nó trong một transaction:
// lỗi giữa chừng ⇒ không ghi gì (ESP giữ events và gửi lại).
func (s *Store) RecordHeartbeat(receivedAt time.Time, id string, raw []byte, events []json.RawMessage) error {
	at := receivedAt.In(vietnam).Format(time.RFC3339)
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("ghi heartbeat: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO heartbeats(device_id, received_at, raw_json) VALUES(?, ?, ?)`,
		id, at, string(raw)); err != nil {
		return fmt.Errorf("ghi heartbeat: %w", err)
	}
	for _, raw := range events {
		var ev deviceEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			// Không đọc được: vẫn giữ dấu vết thay vì mất event.
			ev = deviceEvent{Type: "unparsable", At: at, Reason: string(raw)}
		}
		if _, err := tx.Exec(`INSERT INTO events(device_id, at, type, source, reason, received_at) VALUES(?, ?, ?, ?, ?, ?)`,
			id, ev.At, ev.Type, ev.Source, ev.Reason, at); err != nil {
			return fmt.Errorf("ghi event: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ghi heartbeat: %w", err)
	}
	return nil
}

// LastHeartbeat trả heartbeat nhận gần nhất; ok=false khi chưa có.
func (s *Store) LastHeartbeat() (at time.Time, raw json.RawMessage, ok bool, err error) {
	var recv, body string
	err = s.db.QueryRow(`SELECT received_at, raw_json FROM heartbeats ORDER BY id DESC LIMIT 1`).Scan(&recv, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil, false, nil
	}
	if err != nil {
		return time.Time{}, nil, false, fmt.Errorf("đọc heartbeat cuối: %w", err)
	}
	at, err = time.Parse(time.RFC3339, recv)
	if err != nil {
		return time.Time{}, nil, false, fmt.Errorf("received_at %q hỏng: %w", recv, err)
	}
	return at, json.RawMessage(body), true, nil
}

// Events trả tối đa limit event, mới nhất trước. Mỗi phần tử là JSON cùng khoá như dòng
// events.jsonl cũ (at, id, received_at, source, type, và reason nếu không rỗng).
func (s *Store) Events(limit int) ([]json.RawMessage, error) {
	rows, err := s.db.Query(`SELECT device_id, at, type, source, reason, received_at
		FROM events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("đọc events: %w", err)
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var id, at, typ, source, reason, recv string
		if err := rows.Scan(&id, &at, &typ, &source, &reason, &recv); err != nil {
			return nil, fmt.Errorf("đọc events: %w", err)
		}
		m := map[string]string{"id": id, "at": at, "type": typ, "source": source, "received_at": recv}
		if reason != "" {
			m["reason"] = reason
		}
		b, err := json.Marshal(m)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("đọc events: %w", err)
	}
	return out, nil
}
