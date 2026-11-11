// Server Ner0bit: nhận heartbeat của thiết bị tưới, ghi log, trả lịch tưới.
//
// Cấu hình qua biến môi trường:
//
//	NER0BIT_TOKEN      (bắt buộc) token thiết bị gửi trong "Authorization: Bearer ..."
//	NER0BIT_DEVICE_ID  MAC 12 hex của thiết bị; rỗng ⇒ nhận mọi id
//	NER0BIT_ADDR       địa chỉ lắng nghe, mặc định ":8080"
//	NER0BIT_DATA_DIR   thư mục dữ liệu, mặc định "./data"
//	NER0BIT_DB         file SQLite, mặc định "$NER0BIT_DATA_DIR/ner0bit.db"
//	NER0BIT_TRUST_CF_HEADER=1  tin header CF-Connecting-IP — CHỈ khi chạy sau Cloudflare Tunnel, không mở port
//	NER0BIT_UI_PASSWORD mật khẩu giao diện web (GET /); trống ⇒ tắt UI. Tách khỏi token thiết bị
//	                   vì UI sửa được lịch, tức điều khiển được bơm — dùng chuỗi dài, ngẫu nhiên.
//
// Server chỉ nói HTTP; HTTPS do Cloudflare Tunnel hoặc reverse proxy (Caddy, nginx) đảm nhận.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	token := os.Getenv("NER0BIT_TOKEN")
	if token == "" {
		log.Fatal("thiếu NER0BIT_TOKEN")
	}
	srv := &Server{
		Token:         token,
		DeviceID:      os.Getenv("NER0BIT_DEVICE_ID"),
		DataDir:       getenv("NER0BIT_DATA_DIR", "./data"),
		UIPassword:    os.Getenv("NER0BIT_UI_PASSWORD"),
		TrustCFHeader: os.Getenv("NER0BIT_TRUST_CF_HEADER") == "1",
	}
	if err := os.MkdirAll(srv.DataDir, 0o755); err != nil {
		log.Fatalf("tạo thư mục dữ liệu: %v", err)
	}
	store, err := OpenStore(getenv("NER0BIT_DB", filepath.Join(srv.DataDir, "ner0bit.db")))
	if err != nil {
		log.Fatalf("mở DB: %v", err)
	}
	defer store.Close()
	srv.Store = store
	if ok, err := ImportLegacy(store, srv.DataDir, time.Now().In(vietnam).Format(time.DateOnly)); err != nil {
		log.Fatalf("nhập dữ liệu cũ: %v", err)
	} else if ok {
		log.Print("Đã nhập dữ liệu cũ (schedule.json, heartbeats.jsonl, events.jsonl) vào SQLite; các file cũ được giữ nguyên")
	}
	// Không dừng khi thiếu lịch: lưu lịch qua UI là có hiệu lực ngay; tới lúc đó heartbeat trả 503.
	if d, err := store.Daily(); err != nil {
		log.Printf("CẢNH BÁO: lịch chưa dùng được, đọc lịch dự phòng lỗi: %v", err)
	} else if d == "" {
		log.Print("CẢNH BÁO: lịch chưa dùng được (chưa đặt lịch dự phòng)")
	}
	if err := srv.LoadLastHeartbeat(); err != nil {
		log.Printf("CẢNH BÁO: không nạp được heartbeat cuối: %v", err)
	}
	switch {
	case srv.UIPassword == "":
		log.Print("UI tắt (NER0BIT_UI_PASSWORD trống)")
	case len(srv.UIPassword) < 12:
		log.Print("CẢNH BÁO: NER0BIT_UI_PASSWORD ngắn hơn 12 ký tự — UI sửa được lịch bơm, nên dùng chuỗi dài")
	}
	if srv.DeviceID == "" {
		log.Print("CẢNH BÁO: NER0BIT_DEVICE_ID trống — nhận heartbeat của mọi id")
	}

	httpSrv := &http.Server{
		Addr:              getenv("NER0BIT_ADDR", ":8080"),
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	// Lịch tự động chạy nền, dừng cùng ctx với HTTP; chờ nó xong trước khi đóng DB.
	httpClient := &http.Client{Timeout: 15 * time.Second}
	sched := &Scheduler{
		Store: store,
		Rain:  &VrainClient{URL: vrainURL, UUID: vrainStationUUID, HTTP: httpClient},
		Metar: &MetarClient{URL: metarURL, HTTP: httpClient},
	}
	schedDone := make(chan struct{})
	go func() {
		defer close(schedDone)
		sched.Run(ctx)
	}()

	log.Printf("Ner0bit server lắng nghe %s, dữ liệu ở %s", httpSrv.Addr, srv.DataDir)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	<-schedDone
}
