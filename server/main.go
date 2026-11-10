// Server Ner0bit: nhận heartbeat của thiết bị tưới, ghi log, trả lịch tưới.
//
// Cấu hình qua biến môi trường:
//
//	NER0BIT_TOKEN      (bắt buộc) token thiết bị gửi trong "Authorization: Bearer ..."
//	NER0BIT_DEVICE_ID  MAC 12 hex của thiết bị; rỗng ⇒ nhận mọi id
//	NER0BIT_ADDR       địa chỉ lắng nghe, mặc định ":8080"
//	NER0BIT_DATA_DIR   thư mục dữ liệu, mặc định "./data"
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
		Token:    token,
		DeviceID: os.Getenv("NER0BIT_DEVICE_ID"),
		DataDir:  getenv("NER0BIT_DATA_DIR", "./data"),
	}
	if err := os.MkdirAll(srv.DataDir, 0o755); err != nil {
		log.Fatalf("tạo thư mục dữ liệu: %v", err)
	}
	if _, err := loadSchedule(filepath.Join(srv.DataDir, "schedule.json")); err != nil {
		// Không dừng: sửa file xong là có hiệu lực ngay (đọc lại mỗi request); tới lúc đó trả 503.
		log.Printf("CẢNH BÁO: lịch chưa dùng được: %v", err)
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

	log.Printf("Ner0bit server lắng nghe %s, dữ liệu ở %s", httpSrv.Addr, srv.DataDir)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
