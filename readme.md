![image](./assets/Ner0bit_Logo.png)

Ner0bit là dự án hệ thống tưới nước tự động dùng ESP32. Thiết bị kết nối Internet để đồng bộ lịch tưới từ máy chủ và điều khiển tưới theo lịch. Mục tiêu là giảm công tưới thủ công và cho phép cập nhật lịch tưới từ xa.

## Tính năng

- Kết nối Wi-Fi, mỗi 5 phút POST heartbeat lên server và nhận lịch tưới mới.
- Tưới theo lịch: bật/tắt bơm đúng mốc giờ theo đồng hồ hệ thống ESP32 (đặt theo header `Date` của server, NTP tinh chỉnh), độc lập mạng.
- Mỗi lần bật/tắt bơm hoặc bỏ mốc là một event (`start` / `stop` / `skipped`), gửi lên server ~3 giây sau khi xảy ra. Heartbeat kèm heap trống, trạng thái module, nhiệt độ DS3231.
- Module DS3231 + AT24C32 **tuỳ chọn**: có module thì giữ giờ qua mất điện khi không có mạng, queue events và lịch cache nằm trên EEPROM; không có thì queue events nằm trong RAM, lịch cache trong NVS — mất điện lúc không có mạng thì chờ có mạng lại mới tưới.
- Failsafe: mất internet vẫn tưới theo lịch cache lần fetch cuối; events gửi bù khi có mạng lại.
- Setup qua WiFi hotspot `Ner0bit-Setup` (captive portal): điền WiFi + Server URL (HTTPS; `http://` chỉ để demo) + Token. Vào lại chế độ cấu hình: nhấn RESET, trong 5 giây đầu (LED chớp nhanh) bắt đầu nhấn BOOT và giữ 3 giây (LED sáng liên tục khi đang giữ); hoặc gõ `setup` trong `pio device monitor`. Mất WiFi không tự mở hotspot.
- Hệ thống 1 thiết bị, 1 server; heartbeat xác thực bằng token.
- An toàn bơm: hard-cap 30 phút/phiên, nghỉ tối thiểu 3 phút giữa 2 lần bật.

Chi tiết thiết kế và interface ESP ↔ server: [docs/system-design.md](docs/system-design.md).

## Phần cứng
- Bo mạch ESP32-C3 Super Mini.
- Mạch đồng hồ DS3231 tích hợp AT24C32 (module ZS-042) — **tuỳ chọn**. Cấp nguồn 3.3 V, không cấp 5 V (mạch sạc trên module sẽ sạc pin CR2032).
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
├── server/            # Server Go: nhận heartbeat, trả lịch tưới, giao diện web (Docker)
├── web-client/        # Chỗ trống cũ — giao diện web thật nằm ở server/ui/
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

Nhận heartbeat của thiết bị, trả lịch tưới, tự tính lịch hằng ngày theo mưa và nắng đo thật. Dữ liệu (heartbeat, event, lịch, số liệu mưa/nắng, sổ phút miễn) nằm trong một file **SQLite**: `NER0BIT_DB`, mặc định `$NER0BIT_DATA_DIR/ner0bit.db` (trong container là `/data/ner0bit.db`).

```bash
cd server
cp .env.example .env              # điền NER0BIT_TOKEN (+ NER0BIT_DEVICE_ID, NER0BIT_UI_PASSWORD)
docker compose up -d --build      # lắng nghe :8080 (HTTP)
go test ./...                     # test (cần Go 1.24+)
```

- Lần đầu chạy với DB trống, server nhập một lần `schedule.json`, `heartbeats.jsonl`, `events.jsonl` cũ trong thư mục dữ liệu (nếu có) vào SQLite. Các file cũ được giữ nguyên nhưng không còn được đọc/ghi. `data/schedule.example.json` chỉ là mẫu định dạng `schedule.json` cũ cho lần nhập này; lịch giờ sửa trên giao diện web.
- Chưa có lịch dự phòng ⇒ server trả 503, thiết bị giữ lịch cũ; đặt lịch trên giao diện web là có hiệu lực ngay.
- **Lịch tự động mặc định tắt**: thiết bị tưới theo lịch dự phòng (dòng 1) như trước. Bật ở mục "Thiết lập tự động" trên giao diện web; từ đó server lấy mưa trạm Vrain Đỗ Xuân Hợp và METAR Tân Sơn Nhất mỗi giờ (phút :09), chốt từng lần tưới 6 phút trước giờ tưới. Server cần ra được Internet (`data.vrain.vn`, `aviationweather.gov`).
- Thuật toán, tham số và cách sinh 2 dòng lịch: [docs/system-design.md](docs/system-design.md), mục "Server: lịch tưới tự động".

### Giao diện web

Mở URL server bằng trình duyệt (điện thoại hoặc máy tính), đăng nhập bằng `NER0BIT_UI_PASSWORD`:

- Trạng thái thiết bị: online (heartbeat ≤ 15 phút), bơm bật/tắt, nhiệt độ, WiFi, bộ nhớ, module DS3231/AT24C32, hàng đợi event, giờ thiết bị.
- **Hôm nay**: dải 24 giờ (lịch server trả và các lần bơm chạy thật), mỗi lần tưới một hàng kèm lý do (ví dụ "mưa 6.2 mm từ 19h ⇒ miễn 11 phút; tưới 4 phút"), lần chưa chốt in nghiêng; nút "Huỷ" cho lần tự động; mưa và nhiệt độ 48 giờ qua; "Tưới thử" thêm phiên 5 phút sau N phút (mặc định 7).
- **Lịch tưới**: xem từng ngày (cả ngày đã qua), thêm/xoá phiên bơm thử, cảnh báo phiên quá sát nhau.
- **Thiết lập tự động**: bật/tắt, các lần tưới (giờ + buổi), block, ngưỡng mưa, `min_run`, `carry_max`, `e_ref`. Lịch dự phòng khác lịch tự động thì có nút đặt lại cho khớp.
- **Lịch dự phòng**: sửa dòng 1, có cảnh báo mốc > 30 phút (bị cắt) và mốc sẽ bị bỏ vì chưa nghỉ đủ 3 phút.
- Hoạt động gần đây: bật/tắt/bỏ mốc, nguồn, lý do. Trang tự làm mới mỗi 15 giây.

`NER0BIT_UI_PASSWORD` trống ⇒ UI tắt (thiết bị vẫn chạy bình thường). Phiên đăng nhập là cookie ký HMAC, hạn 30 ngày, `Secure` — nên phải mở qua `https://` (Cloudflare Tunnel / reverse proxy) hoặc `http://localhost`; qua `http://<IP LAN>` trình duyệt không lưu cookie. Sai mật khẩu 5 lần trong 10 phút (theo IP; `NER0BIT_TRUST_CF_HEADER=1` ⇒ lấy IP thật từ `CF-Connecting-IP` — chỉ bật khi server chỉ nhận request qua Cloudflare Tunnel) ⇒ khoá 10 phút. API JSON cho UI: `GET /api/state`, `GET|PUT /api/schedule`, `GET /api/day`, `POST /api/sessions`, `DELETE /api/sessions/{id}`, `POST /api/sessions/{id}/cancel`, `GET /api/weather`, `GET|PUT /api/settings`, `POST /api/settings/daily-from-slots`, `GET /api/events?limit=N` (bảng trong [docs/system-design.md](docs/system-design.md)).

Server chỉ nói HTTP — HTTPS do Cloudflare Tunnel hoặc reverse proxy lo; Server URL trên thiết bị là URL public `https://...`.

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

Thiết kế đã chốt ([docs/system-design.md](docs/system-design.md)). Firmware (`firmware/`): scheduler bơm, event queue trên AT24C32 (hoặc RAM khi không có module), heartbeat HTTPS + NTP, setup portal. **Thiết bị đang chạy thật** (ESP32-C3, fw 0.2.0), bơm cho chậu cây ngoài trời, nhận lịch từ server tại https://irri.thuanle.me. Server Go (`server/`): SQLite, giao diện web, lịch tưới tự động theo mưa Vrain và nắng METAR (mặc định tắt, bật trên giao diện web).

