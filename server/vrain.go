package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// RainSnapshot là một bản đọc mưa cộng dồn của trạm Vrain.
// SumDepthMM cộng dồn từ 19:00 và về lại ở bản 20:07 (xem VDay).
type RainSnapshot struct {
	DataTS, FetchedAt time.Time
	SumDepthMM        float64
	Level             string
}

// VDay trả ngày Vrain (giờ VN, YYYY-MM-DD): từ 20:00 trở đi thuộc ngày kế tiếp.
func VDay(t time.Time) string {
	t = t.In(vietnam)
	if t.Hour() >= 20 {
		t = t.AddDate(0, 0, 1)
	}
	return t.Format("2006-01-02")
}

// parseVrain lấy sumDepth/level của trạm uuid. Thiếu trạm hoặc sumDepth null là lỗi,
// không bao giờ trả số 0 giả.
func parseVrain(body []byte, uuid string) (sum float64, level string, err error) {
	var items []struct {
		SumDepth *float64 `json:"sumDepth"`
		Level    string   `json:"level"`
		Station  struct {
			UUID string `json:"uuid"`
		} `json:"station"`
	}
	if err := json.Unmarshal(body, &items); err != nil {
		return 0, "", fmt.Errorf("Vrain trả JSON không hợp lệ: %w", err)
	}
	for _, it := range items {
		if it.Station.UUID != uuid {
			continue
		}
		if it.SumDepth == nil {
			return 0, "", fmt.Errorf("Vrain: trạm %s có sumDepth null", uuid)
		}
		return *it.SumDepth, it.Level, nil
	}
	return 0, "", fmt.Errorf("Vrain: không thấy trạm %s", uuid)
}

// RainSource là nguồn mưa; Task scheduler dùng interface này để thay bằng bản giả khi test.
type RainSource interface {
	Fetch(ctx context.Context) (RainSnapshot, error)
}

// VrainClient đọc JSON công khai của Vrain.
type VrainClient struct {
	URL, UUID string
	HTTP      *http.Client
}

// Fetch tải và phân tích; DataTS lấy từ Last-Modified (thiếu ⇒ lỗi).
func (c *VrainClient) Fetch(ctx context.Context) (RainSnapshot, error) {
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return RainSnapshot{}, fmt.Errorf("tạo request Vrain: %w", err)
	}
	req.Header.Set("User-Agent", "ner0bit-server")
	resp, err := hc.Do(req)
	if err != nil {
		return RainSnapshot{}, fmt.Errorf("gọi Vrain: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return RainSnapshot{}, fmt.Errorf("Vrain trả HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return RainSnapshot{}, fmt.Errorf("đọc phản hồi Vrain: %w", err)
	}
	lm := resp.Header.Get("Last-Modified")
	if lm == "" {
		return RainSnapshot{}, fmt.Errorf("Vrain thiếu header Last-Modified")
	}
	ts, err := http.ParseTime(lm)
	if err != nil {
		return RainSnapshot{}, fmt.Errorf("Vrain Last-Modified không hợp lệ %q: %w", lm, err)
	}
	sum, level, err := parseVrain(body, c.UUID)
	if err != nil {
		return RainSnapshot{}, err
	}
	return RainSnapshot{
		DataTS:     ts.In(vietnam),
		FetchedAt:  time.Now().In(vietnam),
		SumDepthMM: sum,
		Level:      level,
	}, nil
}

// InsertRainSnapshot ghi snapshot; trùng data_ts ⇒ (false, nil).
func (s *Store) InsertRainSnapshot(r RainSnapshot) (bool, error) {
	res, err := s.db.Exec(`INSERT OR IGNORE INTO rain_snapshots(data_ts, fetched_at, vday, sum_depth, level)
		VALUES(?, ?, ?, ?, ?)`,
		r.DataTS.In(vietnam).Format(time.RFC3339), r.FetchedAt.In(vietnam).Format(time.RFC3339),
		VDay(r.DataTS), r.SumDepthMM, r.Level)
	if err != nil {
		return false, fmt.Errorf("ghi snapshot mưa: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("ghi snapshot mưa: %w", err)
	}
	return n > 0, nil
}

func scanRain(sc interface{ Scan(...any) error }) (RainSnapshot, error) {
	var ts, fetched string
	var level *string
	var r RainSnapshot
	if err := sc.Scan(&ts, &fetched, &r.SumDepthMM, &level); err != nil {
		return r, err
	}
	var err error
	if r.DataTS, err = time.Parse(time.RFC3339, ts); err != nil {
		return r, fmt.Errorf("data_ts hỏng %q: %w", ts, err)
	}
	if r.FetchedAt, err = time.Parse(time.RFC3339, fetched); err != nil {
		return r, fmt.Errorf("fetched_at hỏng %q: %w", fetched, err)
	}
	r.DataTS, r.FetchedAt = r.DataTS.In(vietnam), r.FetchedAt.In(vietnam)
	if level != nil {
		r.Level = *level
	}
	return r, nil
}

// LatestRain trả snapshot mới nhất của vday có data_ts <= upTo.
func (s *Store) LatestRain(vday string, upTo time.Time) (RainSnapshot, bool, error) {
	row := s.db.QueryRow(`SELECT data_ts, fetched_at, sum_depth, level FROM rain_snapshots
		WHERE vday = ? AND data_ts <= ? ORDER BY data_ts DESC LIMIT 1`,
		vday, upTo.In(vietnam).Format(time.RFC3339))
	r, err := scanRain(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RainSnapshot{}, false, nil
		}
		return RainSnapshot{}, false, fmt.Errorf("đọc snapshot mưa: %w", err)
	}
	return r, true, nil
}

// RainSince trả các snapshot có data_ts >= from, theo thứ tự thời gian.
func (s *Store) RainSince(from time.Time) ([]RainSnapshot, error) {
	rows, err := s.db.Query(`SELECT data_ts, fetched_at, sum_depth, level FROM rain_snapshots
		WHERE data_ts >= ? ORDER BY data_ts`, from.In(vietnam).Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("đọc snapshot mưa: %w", err)
	}
	defer rows.Close()
	var out []RainSnapshot
	for rows.Next() {
		r, err := scanRain(rows)
		if err != nil {
			return nil, fmt.Errorf("đọc snapshot mưa: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// rainAnomaly trả mô tả bất thường giữa hai snapshot liên tiếp, "" nếu bình thường.
func rainAnomaly(prev, cur RainSnapshot) string {
	pv, cv := VDay(prev.DataTS), VDay(cur.DataTS)
	if pv == cv {
		if cur.SumDepthMM < prev.SumDepthMM {
			return fmt.Sprintf("mưa cộng dồn giảm trong cùng ngày Vrain: %.1f → %.1f mm lúc %s",
				prev.SumDepthMM, cur.SumDepthMM, cur.DataTS.In(vietnam).Format("15:04"))
		}
		return ""
	}
	if cur.DataTS.In(vietnam).Hour() != 20 {
		return fmt.Sprintf("mưa cộng dồn sang ngày Vrain mới lúc %s (không phải bản 20h)",
			cur.DataTS.In(vietnam).Format("15:04"))
	}
	return ""
}

// hourlyRain đổi chuỗi cộng dồn thành mưa từng giờ (len-1 phần tử): cùng vday lấy hiệu
// (âm ⇒ 0), khác vday (đã reset) lấy giá trị hiện tại.
func hourlyRain(snaps []RainSnapshot) []float64 {
	if len(snaps) < 2 {
		return nil
	}
	out := make([]float64, 0, len(snaps)-1)
	for i := 1; i < len(snaps); i++ {
		prev, cur := snaps[i-1], snaps[i]
		if VDay(prev.DataTS) != VDay(cur.DataTS) {
			out = append(out, cur.SumDepthMM)
			continue
		}
		d := cur.SumDepthMM - prev.SumDepthMM
		if d < 0 {
			d = 0
		}
		out = append(out, d)
	}
	return out
}

// AddWarning ghi một cảnh báo để hiện trên giao diện.
func (s *Store) AddWarning(at time.Time, source, msg string) error {
	_, err := s.db.Exec(`INSERT INTO warnings(at, source, message) VALUES(?, ?, ?)`,
		at.In(vietnam).Format(time.RFC3339), source, msg)
	if err != nil {
		return fmt.Errorf("ghi cảnh báo: %w", err)
	}
	return nil
}
