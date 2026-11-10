![image](./assets/Ner0bit_Logo.png)

Ner0bit là dự án hệ thống tưới nước tự động dùng ESP32. Thiết bị kết nối Internet để đồng bộ lịch tưới từ máy chủ và điều khiển tưới theo lịch. Mục tiêu là giảm công tưới thủ công và cho phép cập nhật lịch tưới từ xa.

## Tính năng

- Kết nối Wi-Fi, mỗi 5 phút POST heartbeat lên server và nhận lịch tưới mới.
- Tưới theo lịch: bật/tắt bơm đúng mốc giờ (RTC DS3231, độc lập mạng).
- Failsafe: mất internet vẫn tưới theo lịch cache lần fetch cuối; events gửi bù khi có mạng lại.
- Setup qua WiFi hotspot `Ner0bit-Setup` (captive portal): điền WiFi + Server URL (HTTPS; `http://` chỉ để demo) + Token. Vào lại chế độ cấu hình: nhấn RESET, sau đó giữ nút BOOT ≥ 3 giây trong 5 giây đầu sau khi boot. Mất WiFi không tự mở hotspot.
- Hệ thống 1 thiết bị, 1 server; heartbeat xác thực bằng token.
- An toàn bơm: hard-cap 30 phút/phiên, nghỉ tối thiểu 10 phút giữa 2 lần bật.

Chi tiết thiết kế và interface ESP ↔ server: [docs/system-design.md](docs/system-design.md).

## Phần cứng
- Bo mạch ESP32-C3 Super Mini.
- Mạch đồng hồ DS3231 tích hợp AT24C32.
- MOSFET IRLB8721 (+ tản nhiệt, diode flyback 10A10).
- Buck 24V→5V.
- Bơm rửa xe 12/24V (thiết kế chạy 24V).
- Nguồn 24V ≥6A.
- Dây điện đi mạch, board đồng đục lỗ, dây header các loại.
- Ống dẫn nước, vòi tưới....

## Cấu trúc repo

```
ner0bit/
├── firmware/          # Firmware ESP32-C3 chính — project PlatformIO
├── playground/        # Code thử nghiệm
│   └── blink/         # Blink LED onboard GPIO 8 (bài kiểm tra toolchain đầu tiên)
├── server/            # Server Go: nhận heartbeat, trả lịch tưới (Docker)
├── web-client/        # Web UI xem trạng thái + sửa lịch (viết sau)
├── enclosure-design/  # Hộp đựng (Blender)
├── docs/              # Tài liệu, thiết kế
│   └── system-design.md
└── assets/            # Logo, hình ảnh
```

## Build firmware

Yêu cầu: [PlatformIO](https://platformio.org/) (extension VSCode hoặc CLI `pio`).

```bash
cd firmware
pio test -e native   # unit test logic lõi (lib/core) trên máy host
pio run              # build cho ESP32-C3
pio run -t upload    # nạp vào board
pio device monitor   # xem Serial log (Ctrl+C để thoát)
```

Gán chân tạm nằm trong `firmware/src/pins.h` (bảng trong [docs/system-design.md](docs/system-design.md), mục Phần cứng & điện).

## Server (Go)

Nhận heartbeat của thiết bị, ghi log, trả lịch tưới. Lịch sửa tay trong `server/data/schedule.json` — server đọc lại mỗi request, không cần restart.

```bash
cd server
cp .env.example .env                                  # điền NER0BIT_TOKEN (+ NER0BIT_DEVICE_ID)
cp data/schedule.example.json data/schedule.json      # sửa lịch
docker compose up -d --build                          # lắng nghe :8080 (HTTP)
go test ./...                                         # test (cần Go 1.24+)
```

`schedule.json`:

```json
{"daily": "25200:900,61200", "days": {"2026-11-12": "", "2026-11-13": "28800"}}
```

- `daily`: dòng 1, lặp mỗi ngày (`<giây từ 0h>[:<giây tưới>]`, mặc định 300 giây).
- `days`: lịch riêng theo ngày (giờ VN) — `""` = hôm đó không tưới, có mốc = thay `daily` trong ngày.
- File sai ⇒ server trả 503, thiết bị giữ lịch cũ; lỗi ghi trong log container.

Log: `data/heartbeats.jsonl`, `data/events.jsonl` (mỗi dòng một JSON). Server chỉ nói HTTP — HTTPS do Cloudflare Tunnel hoặc reverse proxy lo; Server URL trên thiết bị là URL public `https://...`.

## Chạy code thử nghiệm

Yêu cầu: [PlatformIO](https://platformio.org/) (extension VSCode hoặc CLI `pio`).

```bash
cd playground/blink
pio run              # build
pio run -t upload    # nạp vào board
pio device monitor   # xem Serial log (Ctrl+C để thoát)
```

Board: ESP32-C3 Super Mini, LED onboard GPIO 8 (active-LOW).

Cổng nạp cấu hình cho macOS (`/dev/cu.usbmodem*` trong `platformio.ini`). Nạp từ Linux thì đổi thành `/dev/ttyACM*`.

## Trạng thái triển khai

Thiết kế đã chốt ([docs/system-design.md](docs/system-design.md)). Firmware đã viết xong (`firmware/`): scheduler bơm, event queue trên AT24C32, heartbeat HTTPS + NTP, setup portal. Mới kiểm chứng bằng unit test (`pio test -e native`) và build — **chưa chạy thử trên phần cứng** (đang chờ linh kiện để dựng mạch). Server Go (`server/`) đã có bản cơ bản; module thời tiết, web-client viết sau.

