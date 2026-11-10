# Ner0bit — Thiết kế hệ thống

Ngày chốt: 2026-11-10 · Trạng thái: đã duyệt, chờ linh kiện

Cập nhật 2026-11-10: chốt cách vào chế độ cấu hình (reboot + nút BOOT, không tự mở khi mất WiFi), token xác thực, TLS dùng CA bundle, phân biệt dòng 2 rỗng/thiếu, quy tắc bỏ mốc/tưới bù, trường `capped`/`skipped`, nơi lưu trữ, hệ thống 1 thiết bị.

Cập nhật 2026-11-10 (firmware): TLS dùng `setCACert()` với PEM nối 7 root thay cho `setCACertBundle()`; thêm bảng gán chân tạm.

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

Sơ đồ nối chân chi tiết vẽ bổ sung khi có linh kiện. Ràng buộc đã chốt:

- Gate MOSFET: điện trở kéo xuống ~100 kΩ (gate → GND) để bơm **không tự bật** khi ESP đang boot/reset (chân GPIO thả nổi), cộng điện trở nối tiếp ~100 Ω.
- Chân điều khiển bơm **không dùng GPIO2/8/9** (chân strapping; GPIO8 còn là LED, GPIO9 là nút BOOT).

Gán chân **tạm** (firmware đang dùng, gom trong `firmware/src/pins.h`) — chỉ chốt khi có sơ đồ nối chân:

| Chức năng | GPIO | Ghi chú |
|---|---|---|
| Gate MOSFET bơm (`PUMP_PIN`) | 4 | HIGH = bơm chạy; firmware kéo LOW ngay đầu `setup()` |
| I2C SDA (`I2C_SDA`) | 5 | DS3231 `0x68` + AT24C32 `0x57`, 100 kHz |
| I2C SCL (`I2C_SCL`) | 6 | |
| LED onboard (`LED_PIN`) | 8 | Active-LOW; nháy nhanh trong cửa sổ BOOT, sáng liên tục khi ở chế độ cấu hình |
| Nút BOOT (`BOOT_BUTTON_PIN`) | 9 | Active-LOW, `INPUT_PULLUP` |

## Setup / cấu hình WiFi

Vào chế độ cấu hình khi:

- Chưa có cấu hình trong NVS (lần đầu), hoặc
- **Reboot kèm nút BOOT**: nhấn-thả RESET (hoặc cắm lại điện), **sau đó** giữ nút BOOT (GPIO9) ≥ 3 giây trong **5 giây đầu** sau khi boot. LED onboard nháy nhanh trong cửa sổ 5 giây này để báo.
  - Không giữ BOOT *trong lúc* nhấn RESET/cắm điện — GPIO9 là chân strapping, chip sẽ vào chế độ nạp ROM chứ không chạy firmware.
  - Chỉ kiểm tra nút BOOT trong cửa sổ lúc boot; khi đang chạy, nhấn BOOT không có tác dụng.
- **Mất WiFi không tự mở AP** — thiết bị chỉ tiếp tục thử nối lại và tưới theo lịch cache (failsafe). Đổi mật khẩu WiFi nhà ⇒ vào setup bằng reboot + BOOT.

Quy trình:

1. ESP phát WiFi `Ner0bit-Setup` (AP mode, **không mật khẩu**), captive portal tại `192.168.4.1`.
2. Form điền: WiFi SSID, WiFi password, **Server URL** (bắt buộc `https://`), **Token**.
   - Lần setup đầu (chưa có cấu hình) **bắt buộc điền Token**.
   - Đã có cấu hình: để trống password/Token ⇒ giữ giá trị cũ. Nhưng **đổi Server URL thì phải nhập lại Token** (AP không mật khẩu — không cho ai đó trỏ thiết bị sang server lạ để nhận token cũ).
3. Lưu vào NVS, khởi động lại, nối WiFi nhà.

Scheduler bơm vẫn chạy bình thường trong lúc ở chế độ cấu hình. Nếu đã có cấu hình cũ mà 10 phút không ai lưu form ⇒ tự reboot về chế độ thường với cấu hình cũ (tránh AP mở mãi khi lỡ vào setup).

## Interface ESP ↔ Server (cốt lõi — server viết sau phải theo đúng)

Mỗi 5 phút ESP POST tới **Server URL** (URL duy nhất, cấu hình lúc setup):

```
POST <Server URL>
Content-Type: application/json
Authorization: Bearer <Token>

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
    },
    {
      "start": "2026-11-10T11:00:00+07:00",
      "stop":  "2026-11-10T11:30:00+07:00",
      "source": "today",
      "capped": true
    },
    {
      "start": "2026-11-10T11:35:00+07:00",
      "source": "today",
      "skipped": "rest"
    }
  ]
}
```

- `Authorization`: token bí mật điền lúc setup. Hệ thống chỉ có **1 thiết bị**: server so khớp cả token lẫn `id`; sai một trong hai ⇒ trả 401/403 (ESP coi như lỗi, xem dưới).
- `id`: định danh thiết bị — MAC address của ESP32 (12 hex, tự sinh). Log tự chứa đủ thông tin, không phụ thuộc URL.
- `events`: các phiên tưới phát sinh từ heartbeat trước (mảng rỗng nếu không có). Khi offline, events xếp hàng trong AT24C32 và gửi bù khi có mạng. Queue giới hạn ~200 events; tràn thì bỏ event cũ nhất.
  - Nhận **HTTP 200** ⇒ ESP xoá các events vừa gửi khỏi queue (kể cả khi lịch trong body parse lỗi). Status khác / timeout ⇒ giữ nguyên, gửi lại lần sau.
- `source`: `normal` (dòng 1) | `today` (dòng 2) | `failsafe` (phiên chạy khi lần fetch gần nhất bị lỗi — mất mạng / server lỗi — dù lịch cache đang dùng là dòng 1 hay dòng 2).
- `capped`: chỉ có (và = `true`) khi phiên bị cắt bởi hard-cap 30 phút.
- `skipped`: mốc tới giờ nhưng **không bật bơm**; event này không có `stop`. Giá trị hiện có: `"rest"` (vi phạm thời gian nghỉ 10 phút) | `"busy"` (mốc tới giờ khi bơm đang chạy phiên khác).

**Response** (HTTP 200, `text/plain`) là lịch tưới, đúng 2 dòng:

```
25200:900,61200
28800,46800:600
```

- Mỗi mốc: `<giây_từ_0h00>[:<duration_giây>]`. Thiếu `:duration` → mặc định 300 giây (5 phút).
  - `25200` = 07:00, tưới 5 phút. `25200:900` = 07:00, tưới 15 phút.
- **Dòng 1**: lịch thường, lặp mỗi ngày.
- **Dòng 2**: lịch riêng hôm nay. Có mốc hợp lệ ⇒ **thay thế hoàn toàn** dòng 1 trong ngày. Rỗng ⇒ hôm nay không tưới (module thời tiết bỏ hẳn ngày mưa). Thiếu dòng ⇒ dùng dòng 1.
- Phân biệt **rỗng** và **thiếu** dòng 2 (quan trọng — hai trường hợp cho kết quả ngược nhau):
  - Body **có** ký tự `\n` đầu tiên ⇒ có dòng 2 = phần giữa `\n` thứ nhất và `\n` thứ hai (hoặc hết body). `"25200\n"` ⇒ dòng 2 **rỗng** ⇒ hôm nay không tưới.
  - Body **không có** `\n` nào ⇒ thiếu dòng 2. `"25200"` ⇒ dùng dòng 1.
  - Dòng 2 có nội dung nhưng **không có mốc hợp lệ nào** (vd. `abc`) ⇒ coi như **thiếu** (dùng dòng 1) — an toàn cho cây hơn là bỏ tưới.
  - PHP luôn in đủ 2 dòng, kể cả dòng 2 rỗng: `echo "$line1\n$line2";`.
- Mọi mốc tính theo **giờ Việt Nam (UTC+7)**; server chịu trách nhiệm sinh dòng 2 đúng ngày VN.
- Qua 0h00, ESP bỏ dòng 2 cũ (không còn "hôm nay"), dùng dòng 1 cho tới lần fetch thành công kế tiếp.

Lỗi POST / timeout / status ≠ 200 ⇒ ESP giữ lịch cache, thử lại sau 5 phút.

### Quy tắc parse ở ESP

- ESP chỉ phải parse 2 dòng lịch — bằng C thuần (`strtok`/`strtoul`), không cần thư viện JSON. Heartbeat JSON chỉ được tạo ra (`snprintf`), không bao giờ parse.
- Format canonical không chứa khoảng trắng (`25200:900,61200`); parser chấp nhận CRLF và khoảng trắng thừa quanh số.
- Mỗi dòng tối đa **20 mốc**, nhận theo thứ tự xuất hiện, phần dư bỏ.
- Entry sai bị **bỏ lẻ**, không văng cả file: token không phải số, giây ≥ 86400, hoặc duration = 0.
- Cả hai dòng đều không có mốc hợp lệ nào ⇒ coi như fetch lỗi, giữ cache. (PHP không sinh trạng thái này — "hôm nay không tưới" là dòng 1 có mốc + dòng 2 rỗng.)
- Request gửi bằng **HTTP/1.0** (`Connection: close`) nên response không chunked; ESP đọc body thẳng vào buffer cố định, body lịch tối đa **1023 byte** (dài hơn ⇒ coi như lịch lỗi, giữ cache).
- HTTPS qua `WiFiClientSecure` (TLS). Timeout 10 giây **cho từng pha** (kết nối TCP, bắt tay TLS, đọc response) — một request xấu nhất có thể kéo dài vài chục giây, nhưng không bao giờ chặn scheduler bơm (1 giây/lần) vì network chạy trong FreeRTOS task riêng.

### TLS

- Server public qua **Cloudflare Tunnel** hoặc chứng chỉ **Let's Encrypt** (chưa chốt cái nào).
- ESP **kiểm tra chứng chỉ** bằng nhiều root CA nhúng trong firmware: `WiFiClientSecure::setCACert()` với **một chuỗi PEM nối 7 root** (`firmware/src/ca_bundle.cpp`), **không** pin một root CA đơn lẻ và **không** `setInsecure()`. 7 root:
  - ISRG Root X1, ISRG Root X2 (Let's Encrypt)
  - GTS Root R1, GTS Root R3, GTS Root R4 (Google Trust Services)
  - SSL.com Root Certification Authority RSA, SSL.com Root Certification Authority ECC
- Lý do: Cloudflare tự xoay nhà cấp chứng chỉ (Let's Encrypt, Google Trust Services, SSL.com…) — pin một root thì tới lúc đổi CA thiết bị mất kết nối âm thầm. Danh sách 7 root dùng được cho cả hai phương án, chỉ tốn ~10 KB flash (nhẹ hơn nhúng cả CA bundle Mozilla qua `setCACertBundle()`). Cập nhật danh sách: nối lại các file PEM như ghi chú đầu `ca_bundle.cpp` rồi build lại.
- Không hỗ trợ `http://` (token đi trong header).

## Scheduler & an toàn bơm

- Mỗi giây so giờ RTC với lịch hiện hành; đến mốc thì bật bơm, tắt sau duration.
- **Hard-cap 30 phút/phiên** — duration lớn hơn bị cắt còn 30 phút (bơm rửa xe không nên chạy liên tục lâu hơn).
- **Nghỉ tối thiểu 10 phút** giữa hai lần bật bơm (chống bật/tắt dồn dập — kẻ thù chính của tuổi thọ bơm).
  - Mốc tới giờ mà chưa nghỉ đủ 10 phút ⇒ **bỏ mốc** (không hoãn), ghi event `"skipped": "rest"`.
  - Thời điểm tắt bơm gần nhất lưu trong AT24C32 để quy tắc này còn hiệu lực qua reboot.
- **Phiên đang chạy luôn chạy nốt** theo duration lúc bắt đầu, kể cả khi lịch mới về giữa chừng hoặc qua 0h00.
- Mốc tới giờ khi bơm đang chạy phiên khác ⇒ **bỏ mốc** (phiên đang chạy không kéo dài), ghi event `"skipped": "busy"`.
- **Tưới bù**: khi boot hoặc khi nhận lịch mới, nếu thời điểm hiện tại nằm trong khung `[mốc, mốc + duration)` của một mốc và bơm đang tắt ⇒ bật cho **phần thời gian còn lại** (vẫn qua quy tắc nghỉ và hard-cap). Mốc đã qua hẳn khung ⇒ không bù.
  - Vd. mất điện lúc 07:03 giữa phiên 07:00–07:05, có điện lại 07:04 ⇒ tưới tiếp 1 phút. (Tắt do mất điện không ghi "thời điểm tắt bơm", nên quy tắc nghỉ không chặn phần bù này.)
  - Phần phiên trước khi mất điện không có event (event chỉ ghi lúc phiên kết thúc) — chấp nhận.
- Mọi phiên tưới (kể cả bị cap, tưới bù) và mốc bị bỏ đều nằm trong `events` của heartbeat.

## Đồng bộ thời gian

- Có mạng: header **`Date`** của heartbeat HTTPS (mỗi 300 s) là nguồn giờ chính; NTP (mỗi 6 giờ) chỉ tinh chỉnh. Giờ hiển thị/lịch theo UTC+7.
- Mất mạng: tin DS3231 (pin onboard giữ giờ).
- **NTP không xác thực** — ai giả được gói NTP trong LAN là xê dịch được đồng hồ (bơm tưới sai giờ). Kể cả chỉ cho NTP chỉnh từng bước nhỏ (vd. ≤ 300 s) thì kẻ tấn công vẫn dời được đồng hồ không giới hạn: mỗi lần sync (6 giờ một lần, và mỗi lần reboot) đẩy thêm 299 s. Nên mọi giờ ghi vào DS3231 đều qua `TimeTrust` (`firmware/lib/core/time_trust.*`), với *neo* là header `Date` đã được TLS xác thực:
  1. **Header `Date` là nguồn tin cậy.** Mỗi response **200** qua HTTPS có `Date` hợp lệ: tính `dateNow = Date + (monoSec() lúc này − monoSec() lúc nhận response)`. Đồng hồ chưa hợp lệ (DS3231 mất nguồn — cờ OSF, chưa từng chỉnh, hoặc đọc lỗi) hoặc |DS3231 − dateNow| > **5 s** ⇒ ghi DS3231 = dateNow, đồng hồ hợp lệ. Lệch ≤ 5 s ⇒ không ghi. Cả hai trường hợp đều lưu *neo* (`Date`, `monoSec()` lúc nhận response). Response không có `Date` hợp lệ ⇒ bỏ qua, neo cũ giữ nguyên.
  2. **Có neo (chưa quá 24 giờ)** ⇒ chỉ nhận NTP khi |NTP − (giờ neo + thời gian monotonic đã trôi từ lúc neo)| ≤ **5 s**; lệch hơn ⇒ bỏ, ghi log (có thể bị giả mạo). Lúc này DS3231 không được xét: NTP chỉ còn tác dụng tinh chỉnh dưới 5 s, không bao giờ kéo đồng hồ ra xa giờ đã xác thực quá 5 s.
  3. **Chưa có neo** (vừa boot, hoặc server không tới được, hoặc neo đã quá 24 giờ) ⇒ chỉ nhận NTP khi đồng hồ chưa hợp lệ (bootstrap — chưa có giờ nào tốt hơn) hoặc |NTP − DS3231| ≤ **5 s**. NTP lệch lớn **không bao giờ** ghi đè một DS3231 đang hợp lệ: ESP bỏ giờ NTP đó, heartbeat ngay, và chờ header `Date` phân xử (DS3231 sai thật thì quy tắc 1 chỉnh lại).
  4. Giờ NTP hoặc `Date` < 2026-01-01 hoặc ≥ **2100-01-01** (4102444800) ⇒ vô lý, bỏ (`Date` vô lý không đổi neo).
- Server **phải gửi header `Date` đúng giờ** — DS3231 được chỉnh theo nó mỗi khi lệch quá 5 s. Web server và Cloudflare đều tự gửi header này theo chuẩn; server chỉ cần đồng bộ giờ hệ thống (NTP phía server).
- Header `Date` phải theo dạng IMF-fixdate (RFC 7231: `Sun, 06 Nov 1994 08:49:37 GMT` — PHP/nginx/Apache/Cloudflare đều tự gửi). Parser chặt (`parseHttpDate`, `firmware/lib/core/http_date.*`): đúng 29 ký tự, không nhận định dạng RFC 850/asctime.
- Giờ hệ thống của ESP (`time()`) vẫn do SNTP đặt, nhưng không dùng cho scheduler (chỉ DS3231) và không ảnh hưởng TLS: mbedTLS của Arduino-ESP32 build không bật `MBEDTLS_HAVE_TIME_DATE` nên không kiểm hạn chứng chỉ.

## Failsafe

- Lịch của lần fetch thành công gần nhất được cache trong NVS (chỉ ghi khi nội dung lịch thay đổi).

## Lưu trữ

| Nơi | Dữ liệu | Lý do |
|---|---|---|
| NVS (flash ESP32) | WiFi SSID/password, Server URL, Token, lịch cache | Ít khi ghi |
| AT24C32 (EEPROM 4 KB, ~1 triệu lần ghi) | Queue events (ring buffer, 200 × ~10 B ≈ 2 KB), thời điểm tắt bơm gần nhất | Ghi thường xuyên, không làm mòn flash ESP |
- Mất WiFi hoặc server chết: ESP vẫn tưới đúng lịch cache theo RTC; `source: "failsafe"` trong events gửi bù khi mạng trở lại.

## Phần để sau (không thuộc bản này)

- **Module PHP**: endpoint heartbeat + sinh lịch (theo interface trên) + lưu log events. Chỉ phục vụ 1 thiết bị: một lịch duy nhất, kiểm tra token + `id`.
- **Module thời tiết**: chỉnh dòng 2 theo dự báo.
- **Web-client**: xem trạng thái thiết bị, sửa lịch.
- **Enclosure**: đã có file `.blend` ban đầu.
