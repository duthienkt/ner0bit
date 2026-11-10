# Ner0bit — Thiết kế hệ thống

Ngày chốt: 2026-11-10 · Trạng thái: đã duyệt, chờ linh kiện

## Tổng quan

Hệ thống 2 phần:

- **Thiết bị**: ESP32-C3 điều khiển bơm tưới cây theo lịch, hoạt động độc lập với mạng (RTC + lịch cache).
- **Server** (PHP, viết sau): nhận heartbeat, trả lịch tưới. Module thời tiết chỉnh lịch theo thời tiết (viết sau).

Giao tiếp một chiều duy nhất do ESP chủ động: **mỗi 5 phút POST một heartbeat, response mang lịch mới**. Không cần static IP, không MQTT, không websocket — chạy được trên shared hosting PHP.

## Phần cứng & điện

| Khối | Chọn | Ghi chú |
|---|---|---|
| MCU | ESP32-C3 Super Mini | LED onboard GPIO 8, active-LOW |
| RTC | DS3231 + AT24C32 (kèm pin) | Giữ giờ khi mất điện/mạng |
| Bơm | Bơm rửa xe 12/24V | Thiết kế chạy ở 24V |
| Nguồn | Adapter/PSU 24V ≥6A | |
| Công tắc bơm | MOSFET IRLB8721 + tản nhiệt | Logic-level, Rds(on) ~8.7 mΩ |
| Flyback | Diode 10A10 song song bơm | Dập xung ngược khi tắt |
| Cấp MCU | Buck 24V→5V | Super Mini có regulator 5V→3.3V onboard |

Sơ đồ nối chân chi tiết vẽ bổ sung khi có linh kiện.

## Setup lần đầu

1. Giữ nút reset khi boot → ESP vào chế độ cấu hình.
2. ESP phát WiFi `Ner0bit-Setup` (AP mode), captive portal tại `192.168.4.1`.
3. Form điền: WiFi SSID, WiFi password, **Server URL**.
4. Lưu vào NVS, khởi động lại, nối WiFi nhà.

## Interface ESP ↔ Server (cốt lõi — server viết sau phải theo đúng)

Mỗi 5 phút ESP POST tới **Server URL** (URL duy nhất, cấu hình lúc setup):

```
POST <Server URL>
Content-Type: application/json

{
  "id": "a4cf12b8e901",
  "fw": "0.1.0",
  "uptime_s": 123456,
  "rssi": -58,
  "pump_on": false,
  "rtc": "2026-11-10T15:05:00+07:00",
  "events": [
    {
      "start": "2026-11-10T07:00:00+07:00",
      "stop":  "2026-11-10T07:05:00+07:00",
      "source": "today"
    }
  ]
}
```

- `id`: định danh thiết bị — MAC address của ESP32 (12 hex, tự sinh). Log tự chứa đủ thông tin, không phụ thuộc URL.
- `events`: các phiên tưới phát sinh từ heartbeat trước (mảng rỗng nếu không có). Khi offline, events xếp hàng trong flash và gửi bù khi có mạng.
- `source`: `normal` (dòng 1) | `today` (dòng 2) | `failsafe` (tưới theo cache khi mất mạng).

**Response** (HTTP 200, `text/plain`) là lịch tưới, đúng 2 dòng:

```
25200:900,61200
28800,46800:600
```

- Mỗi mốc: `<giây_từ_0h00>[:<duration_giây>]`. Thiếu `:duration` → mặc định 300 giây (5 phút).
  - `25200` = 07:00, tưới 5 phút. `25200:900` = 07:00, tưới 15 phút.
- **Dòng 1**: lịch thường, lặp mỗi ngày.
- **Dòng 2**: lịch riêng hôm nay. Có nội dung ⇒ **thay thế hoàn toàn** dòng 1 trong ngày. Rỗng ⇒ hôm nay không tưới (module thời tiết bỏ hẳn ngày mưa). Thiếu dòng ⇒ dùng dòng 1.
- Mọi mốc tính theo **giờ Việt Nam (UTC+7)**; server chịu trách nhiệm sinh dòng 2 đúng ngày VN.
- Qua 0h00, ESP bỏ dòng 2 cũ (không còn "hôm nay"), dùng dòng 1 cho tới lần fetch thành công kế tiếp.

Lỗi POST / timeout / status ≠ 200 ⇒ ESP giữ lịch cache, thử lại sau 5 phút.

## Scheduler & an toàn bơm

- Mỗi giây so giờ RTC với lịch hiện hành; đến mốc thì bật bơm, tắt sau duration.
- **Hard-cap 30 phút/phiên** — duration lớn hơn bị cắt còn 30 phút (bơm rửa xe không nên chạy liên tục lâu hơn).
- **Nghỉ tối thiểu 10 phút** giữa hai lần bật bơm (chống bật/tắt dồn dập — kẻ thù chính của tuổi thọ bơm).
- Mọi phiên tưới (kể cả bị cap/cắt) đều nằm trong `events` của heartbeat.

## Đồng bộ thời gian

- Có mạng: NTP (múi UTC+7), định kỳ hiệu chỉnh DS3231.
- Mất mạng: tin DS3231 (pin onboard giữ giờ).

## Failsafe

- Lịch của lần fetch thành công gần nhất được cache trong flash (AT24C32/NVS).
- Mất WiFi hoặc server chết: ESP vẫn tưới đúng lịch cache theo RTC; `source: "failsafe"` trong events gửi bù khi mạng trở lại.

## Phần để sau (không thuộc bản này)

- **Module PHP**: endpoint heartbeat + sinh lịch (theo interface trên) + lưu log events.
- **Module thời tiết**: chỉnh dòng 2 theo dự báo.
- **Web-client**: xem trạng thái thiết bị, sửa lịch.
- **Enclosure**: đã có file `.blend` ban đầu.
