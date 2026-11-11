package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"
)

// latVTS là vĩ độ dùng tính bức xạ đỉnh khí quyển (độ).
const latVTS = 10.8136

// MetarObs là một bản tin METAR của VVTS. TempC/DewpC nil khi bản tin không có.
type MetarObs struct {
	ObsTime         time.Time
	TempC, DewpC    *float64
	CloudsJSON, Raw string
}

// parseMetar đọc JSON của aviationweather.gov; ObsTime lấy từ obsTime (epoch giây).
func parseMetar(body []byte) ([]MetarObs, error) {
	var items []struct {
		ObsTime *int64          `json:"obsTime"`
		Temp    *float64        `json:"temp"`
		Dewp    *float64        `json:"dewp"`
		Clouds  json.RawMessage `json:"clouds"`
		RawOb   string          `json:"rawOb"`
	}
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("METAR trả JSON không hợp lệ: %w", err)
	}
	out := make([]MetarObs, 0, len(items))
	for i, it := range items {
		if it.ObsTime == nil {
			return nil, fmt.Errorf("METAR: bản tin %d thiếu obsTime", i)
		}
		clouds := "[]"
		if len(it.Clouds) > 0 && string(it.Clouds) != "null" {
			var buf bytes.Buffer
			if err := json.Compact(&buf, it.Clouds); err != nil {
				return nil, fmt.Errorf("METAR: bản tin %d có clouds hỏng: %w", i, err)
			}
			clouds = buf.String()
		}
		out = append(out, MetarObs{
			ObsTime:    time.Unix(*it.ObsTime, 0).In(vietnam),
			TempC:      it.Temp,
			DewpC:      it.Dewp,
			CloudsJSON: clouds,
			Raw:        it.RawOb,
		})
	}
	return out, nil
}

// MetarSource là nguồn METAR; scheduler dùng interface này để thay bằng bản giả khi test.
type MetarSource interface {
	Fetch(ctx context.Context) ([]MetarObs, error)
}

// MetarClient đọc METAR công khai của aviationweather.gov.
type MetarClient struct {
	URL  string
	HTTP *http.Client
}

// Fetch tải và phân tích các bản tin METAR.
func (c *MetarClient) Fetch(ctx context.Context) ([]MetarObs, error) {
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("tạo request METAR: %w", err)
	}
	req.Header.Set("User-Agent", "ner0bit-server")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gọi METAR: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("METAR trả HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("đọc phản hồi METAR: %w", err)
	}
	return parseMetar(body)
}

// raMM trả bức xạ đỉnh khí quyển Ra (FAO-56 eq. 21–25) đổi sang mm/ngày (× 0.408).
func raMM(date time.Time, latDeg float64) float64 {
	const gsc = 0.0820 // MJ/m²/phút
	j := float64(date.YearDay())
	phi := latDeg * math.Pi / 180
	dr := 1 + 0.033*math.Cos(2*math.Pi*j/365)
	delta := 0.409 * math.Sin(2*math.Pi*j/365-1.39)
	ws := math.Acos(-math.Tan(phi) * math.Tan(delta))
	ra := 24 * 60 / math.Pi * gsc * dr *
		(ws*math.Sin(phi)*math.Sin(delta) + math.Cos(phi)*math.Cos(delta)*math.Sin(ws))
	return ra * 0.408
}

// hargreaves trả bốc hơi tham chiếu ET (mm/ngày) theo Hargreaves.
func hargreaves(tmax, tmin, ra float64) float64 {
	return 0.0023 * ra * ((tmax+tmin)/2 + 17.8) * math.Sqrt(math.Max(0, tmax-tmin))
}

// SunFactor tính hệ số nắng s = min(1, ET/eRef) từ các bản tin METAR có nhiệt độ trong
// [00:00, 17:00) giờ VN của date. Dưới 12 bản tin ⇒ (1, 0, false).
func SunFactor(obs []MetarObs, date string, eRefMM float64) (s, et float64, ok bool) {
	d, err := time.ParseInLocation("2006-01-02", date, vietnam)
	if err != nil {
		return 1, 0, false
	}
	from, to := d, d.Add(17*time.Hour)
	n := 0
	var tmax, tmin float64
	for _, o := range obs {
		if o.TempC == nil || o.ObsTime.Before(from) || !o.ObsTime.Before(to) {
			continue
		}
		t := *o.TempC
		if n == 0 || t > tmax {
			tmax = t
		}
		if n == 0 || t < tmin {
			tmin = t
		}
		n++
	}
	if n < 12 || eRefMM <= 0 {
		return 1, 0, false
	}
	et = hargreaves(tmax, tmin, raMM(d, latVTS))
	return math.Min(1, et/eRefMM), et, true
}

// InsertMetar ghi các bản tin; trùng obs_time bị bỏ qua.
func (s *Store) InsertMetar(obs []MetarObs) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("ghi METAR: %w", err)
	}
	defer tx.Rollback()
	for _, o := range obs {
		clouds := o.CloudsJSON
		if clouds == "" {
			clouds = "[]"
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO metar_obs(obs_time, temp, dewp, clouds_json, raw)
			VALUES(?, ?, ?, ?, ?)`,
			o.ObsTime.In(vietnam).Format(time.RFC3339), o.TempC, o.DewpC, clouds, o.Raw); err != nil {
			return fmt.Errorf("ghi METAR: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ghi METAR: %w", err)
	}
	return nil
}

// MetarBetween trả các bản tin có from <= obs_time < to, theo thứ tự thời gian.
func (s *Store) MetarBetween(from, to time.Time) ([]MetarObs, error) {
	rows, err := s.db.Query(`SELECT obs_time, temp, dewp, clouds_json, raw FROM metar_obs
		WHERE obs_time >= ? AND obs_time < ? ORDER BY obs_time`,
		from.In(vietnam).Format(time.RFC3339), to.In(vietnam).Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("đọc METAR: %w", err)
	}
	defer rows.Close()
	var out []MetarObs
	for rows.Next() {
		var ts string
		var o MetarObs
		if err := rows.Scan(&ts, &o.TempC, &o.DewpC, &o.CloudsJSON, &o.Raw); err != nil {
			return nil, fmt.Errorf("đọc METAR: %w", err)
		}
		if o.ObsTime, err = time.Parse(time.RFC3339, ts); err != nil {
			return nil, fmt.Errorf("obs_time hỏng %q: %w", ts, err)
		}
		o.ObsTime = o.ObsTime.In(vietnam)
		out = append(out, o)
	}
	return out, rows.Err()
}
