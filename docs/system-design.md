# Ner0bit — Thiết kế hệ thống

Ngày chốt: 2026-11-10 · Trạng thái: đã duyệt, chờ linh kiện

Cập nhật 2026-11-10: chốt cách vào chế độ cấu hình (reboot + nút BOOT, không tự mở khi mất WiFi), token xác thực, TLS dùng CA bundle, phân biệt dòng 2 rỗng/thiếu, quy tắc bỏ mốc/tưới bù, trường `capped`/`skipped`, nơi lưu trữ, hệ thống 1 thiết bị.

Cập nhật 2026-11-10 (firmware): TLS dùng `setCACert()` với PEM nối 7 root thay cho `setCACertBundle()`; thêm bảng gán chân tạm.

Cập nhật 2026-11-10 (firmware v2): mỗi lần bơm đổi trạng thái là một event (`start` / `stop` kèm `reason` / `skipped`), thay cho bản ghi phiên `{start, stop}` cùng cờ `capped` / `skipped`; heartbeat gửi sớm ~3 s sau event; heartbeat thêm `heap_free`, `heap_min`, trạng thái module, `temp_c`, `queue`. Đồng hồ hệ thống ESP32 là đồng hồ chạy; module DS3231 + AT24C32 thành **tuỳ chọn** (không có thì queue events nằm trong RAM, lịch cache trong NVS); lịch cache chuyển sang AT24C32 khi có module.

## Tổng quan

Hệ thống 2 phần:

- **Thiết bị**: ESP32-C3 điều khiển bơm tưới cây theo lịch, hoạt động độc lập với mạng (đồng hồ hệ thống + lịch cache; module DS3231 tuỳ chọn giữ giờ qua mất điện).
- **Server** (Go, `server/`): nhận heartbeat, trả lịch tưới. Module thời tiết chỉnh lịch theo thời tiết (viết sau).

Giao tiếp một chiều duy nhất do ESP chủ động: **mỗi 5 phút POST một heartbeat (và ~3 s sau mỗi event), response mang lịch mới**. Không cần static IP, không MQTT, không websocket — server là một binary Go nhỏ (Docker), public qua Cloudflare Tunnel hoặc reverse proxy.

## Phần cứng & điện

| Khối | Chọn | Ghi chú |
|---|---|---|
| MCU | ESP32-C3 Super Mini | LED onboard GPIO 8, active-LOW |
| RTC + EEPROM (**tuỳ chọn**) | Module ZS-042: DS3231 + AT24C32, pin CR2032 | Giữ giờ qua mất điện khi không có mạng; EEPROM cho queue events + lịch cache. Không lắp vẫn chạy (bảng dưới) |
| Bơm | Bơm rửa xe 12/24V | Thiết kế chạy ở 24V |
| Nguồn | Adapter/PSU 24V ≥6A | |
| Công tắc bơm | MOSFET IRLB8721 + tản nhiệt | Logic-level, Rds(on) ~8.7 mΩ |
| Flyback | Diode 10A10 song song bơm | Dập xung ngược khi tắt |
| Cấp MCU | Buck 24V→5V | Super Mini có regulator 5V→3.3V onboard |

Sơ đồ nối chân chi tiết vẽ bổ sung khi có linh kiện. Ràng buộc đã chốt:

- Gate MOSFET: điện trở kéo xuống ~100 kΩ (gate → GND) để bơm **không tự bật** khi ESP đang boot/reset (chân GPIO thả nổi), cộng điện trở nối tiếp ~100 Ω.
- Chân điều khiển bơm **không dùng GPIO2/8/9** (chân strapping; GPIO8 còn là LED, GPIO9 là nút BOOT).
- Module ZS-042 cấp nguồn **3.3 V** (chân 3V3 của Super Mini), **không cấp 5 V**:
  - Module có sẵn mạch sạc pin (diode + điện trở 200 Ω từ VCC vào pin) làm cho pin sạc được LIR2032. Cấp 5 V thì mạch này **sạc pin CR2032** (pin không sạc được) ⇒ pin phồng/hỏng. Ở 3.3 V, áp sau diode thấp hơn áp pin nên gần như không sạc; muốn chắc chắn thì gỡ điện trở (hoặc diode) sạc trên module.
  - Điện trở kéo lên I2C trên module nối vào VCC — cấp 5 V sẽ kéo SDA/SCL lên 5 V, quá mức chịu của GPIO ESP32-C3.

### Module DS3231 + AT24C32 tuỳ chọn

Lúc boot firmware dò từng chip qua I2C (DS3231 `0x68`, AT24C32 `0x57`), độc lập nhau, rồi ghi một dòng log cho mỗi chip và một dòng chế độ lưu trữ. Trạng thái mỗi chip gửi trong heartbeat (`ds3231`, `at24c32`).

| Chip | Trạng thái lúc boot | Hệ quả |
|---|---|---|
| DS3231 | `ok` — có ACK, cờ OSF tắt, đọc giờ được | Đặt đồng hồ hệ thống theo DS3231 ⇒ tưới được ngay, kể cả khi không có mạng. (Giờ đọc được vô lý — trước 2026 / từ 2100 — thì vẫn `ok` nhưng chờ NTP / `Date` như dưới.) |
| DS3231 | `lost_power` — cờ OSF bật (pin hết / bị tháo) | Giờ trong chip không tin được ⇒ chờ NTP / `Date`; khi có giờ thì ghi lại DS3231 (xoá OSF). Heartbeat vẫn báo `lost_power` tới lần boot sau. |
| DS3231 | `failed` — có ACK nhưng đọc lỗi (đã thử 3 lần) | Bỏ dùng tới lần boot sau; như không có module. |
| DS3231 | `absent` — không có ACK | Chờ NTP / `Date`; chưa có giờ ⇒ không tưới. |
| AT24C32 | `ok` — có ACK, đọc (hoặc format) được header queue | Queue events, lastStop, lịch cache trên AT24C32 — giữ qua mất điện. |
| AT24C32 | `failed` (có ACK nhưng đọc/ghi header lỗi) / `absent` | Queue events + lastStop trong **RAM 4 KB** (mất khi reset; lastStop có bản sao giữ qua reset mềm — xem "Scheduler & an toàn bơm"), lịch cache trong NVS. |

Lỗi lúc chạy **không bao giờ dừng việc tưới**:

- DS3231 ghi giờ lỗi (đã thử 3 lần) hoặc đọc nhiệt độ lỗi 3 lần liên tiếp ⇒ `failed`, không dùng DS3231 nữa tới lần boot sau; tưới tiếp theo đồng hồ hệ thống.
- AT24C32 ghi lỗi (đã thử 3 lần) ⇒ `failed`, chuyển hẳn sang RAM tới lần boot sau: queue RAM mới rỗng (events chưa gửi trên AT24C32 bị bỏ; event vừa phát sinh được ghi lại vào queue RAM), lastStop giữ, lịch cache ghi NVS từ đó. Có log một dòng.

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
2. Form điền: WiFi SSID, WiFi password, **Server URL** (`https://`, hoặc `http://` khi thử/demo), **Token**.
   - Lần setup đầu (chưa có cấu hình) **bắt buộc điền Token**.
   - Đã có cấu hình: để trống password/Token ⇒ giữ giá trị cũ. Nhưng **đổi Server URL thì phải nhập lại Token** (AP không mật khẩu — không cho ai đó trỏ thiết bị sang server lạ để nhận token cũ).
3. Lưu vào NVS, khởi động lại, nối WiFi nhà.

Scheduler bơm vẫn chạy bình thường trong lúc ở chế độ cấu hình. Nếu đã có cấu hình cũ mà 10 phút không ai lưu form ⇒ tự reboot về chế độ thường với cấu hình cũ (tránh AP mở mãi khi lỡ vào setup).

## Interface ESP ↔ Server (cốt lõi — server viết sau phải theo đúng)

ESP POST tới **Server URL** (URL duy nhất, cấu hình lúc setup):

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
  "heap_free": 182344,
  "heap_min": 151020,
  "ds3231": "ok",
  "at24c32": "ok",
  "temp_c": 27.25,
  "queue": 5,
  "rtc": "2026-11-10T15:05:00+07:00",
  "events": [
    {"type": "start",   "at": "2026-11-10T07:00:00+07:00", "source": "today"},
    {"type": "stop",    "at": "2026-11-10T07:05:00+07:00", "source": "today", "reason": "done"},
    {"type": "start",   "at": "2026-11-10T11:00:00+07:00", "source": "today"},
    {"type": "stop",    "at": "2026-11-10T11:30:00+07:00", "source": "today", "reason": "capped"},
    {"type": "skipped", "at": "2026-11-10T11:32:00+07:00", "source": "today", "reason": "rest"}
  ]
}
```

ESP gửi JSON không khoảng trắng, khoá theo đúng thứ tự trên (`temp_c` có thể vắng). Server không được dựa vào thứ tự khoá.

**Khi nào gửi heartbeat:**

- Định kỳ mỗi **300 s**.
- **~3 s sau event** (bật/tắt bơm, bỏ mốc): event đầu tiên đặt hạn 3 s, các event đến sau trong 3 s đó đi chung một heartbeat. Event đã đi cùng một heartbeat khác trong lúc chờ ⇒ không gửi thêm.
- Ngay khi vừa có WiFi; ngay khi đồng hồ vừa hợp lệ nhờ NTP (để áp lịch), hoặc NTP lệch đồng hồ mà chưa có neo `Date` (để `Date` phân xử — xem "Đồng bộ thời gian").
- Sau một response 200 mà queue còn events (quá 20 event một lần) ⇒ gửi tiếp sau 5 s cho tới khi hết.
- Mất WiFi: events nằm trong queue, gửi ngay khi WiFi trở lại.

**Các trường:**

- `Authorization`: token bí mật điền lúc setup. Hệ thống chỉ có **1 thiết bị**: server so khớp cả token lẫn `id`; sai một trong hai ⇒ trả 401/403 (ESP coi như lỗi, xem dưới).
- `id`: định danh thiết bị — MAC address của ESP32 (12 hex chữ thường, tự sinh). Log tự chứa đủ thông tin, không phụ thuộc URL.
- `fw`: phiên bản firmware. `uptime_s`: giây kể từ boot (monotonic). `rssi`: dBm của WiFi. `pump_on`: bơm đang chạy lúc gửi.
- `heap_free`: heap trống lúc gửi (`esp_get_free_heap_size()`, byte). `heap_min`: heap trống thấp nhất từ lúc boot (`esp_get_minimum_free_heap_size()`, byte) — theo dõi rò bộ nhớ.
- `ds3231`, `at24c32`: trạng thái module — `"ok"` | `"absent"` (không có chip) | `"lost_power"` (chỉ DS3231: cờ OSF bật lúc boot, giữ tới lần boot sau) | `"failed"` (có chip nhưng lỗi I/O, đã bỏ dùng). Xem "Module DS3231 + AT24C32 tuỳ chọn".
- `temp_c`: nhiệt độ cảm biến trong DS3231 (°C, bước 0,25, in 2 chữ số thập phân, có thể âm). **Vắng** khi không có / không dùng DS3231 hoặc lần đọc này lỗi.
- `queue`: số event trong queue lúc gửi, **kể cả** các event gửi kèm heartbeat này. `queue` > số phần tử `events` ⇒ còn event chờ (ESP gửi tiếp sau 5 s).
- `rtc`: giờ đồng hồ hệ thống lúc gửi (UTC+7). Đồng hồ chưa hợp lệ ⇒ `"1970-01-01T00:00:00+07:00"`.
- `events`: event phát sinh từ heartbeat trước, cũ nhất trước, tối đa **20** mỗi heartbeat (mảng rỗng nếu không có). Khi offline, events xếp hàng trong queue (AT24C32, hoặc RAM nếu không có / hỏng AT24C32 — xem "Lưu trữ") và gửi bù khi có mạng. Queue giới hạn **200** events; tràn thì bỏ event cũ nhất.
  - Nhận **HTTP 200** ⇒ ESP xoá các events vừa gửi khỏi queue (kể cả khi lịch trong body parse lỗi). Status khác / timeout ⇒ giữ nguyên, gửi lại lần sau.

**Event** — mỗi lần bơm đổi trạng thái là một event, cộng event bỏ mốc. Thứ tự khoá: `type`, `at`, `source`, `reason`.

| `type` | `at` | `reason` | Ý nghĩa |
|---|---|---|---|
| `start` | lúc bật bơm | — (không có khoá) | Bơm bật, kể cả tưới bù sau mất điện / khi nhận lịch mới. |
| `stop` | lúc tắt bơm | `done` \| `capped` \| `abort` | Bơm tắt. `done`: chạy hết duration. `capped`: phiên bị cắt ở hard-cap 30 phút (duration của mốc > 1800 s). `abort`: kết thúc giữa chừng vì đồng hồ không dùng được hoặc sắp chủ động khởi động lại — `at` = lúc bật + thời gian monotonic đã chạy. |
| `skipped` | mốc bị bỏ (giờ của mốc, không phải lúc xét) | `rest` \| `busy` | Mốc tới giờ nhưng **không bật bơm**. `rest`: chưa nghỉ đủ 3 phút từ lần tắt trước. `busy`: bơm đang chạy phiên khác. |

- `at`: giờ VN dạng `YYYY-MM-DDTHH:MM:SS+07:00`.
- `source`: `normal` (dòng 1) | `today` (dòng 2) | `failsafe` (phiên chạy khi lần fetch gần nhất bị lỗi — mất mạng / server lỗi — dù lịch cache đang dùng là dòng 1 hay dòng 2). `start` và `stop` của cùng một phiên luôn cùng `source`.
- Một phiên bình thường = `start` rồi `stop`. Phiên bị mất điện giữa chừng chỉ có `start` (không có `stop`); phần tưới bù sau đó là một cặp `start`/`stop` mới. Ở chế độ RAM (không có AT24C32), event chưa gửi mất khi mất điện / reset.

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
  - Server: ngày có lịch riêng thì in `dòng1 + "\n" + dòng2` (kể cả dòng 2 rỗng); không có lịch riêng thì chỉ in dòng 1, không `\n`.
- Mọi mốc tính theo **giờ Việt Nam (UTC+7)**; server chịu trách nhiệm sinh dòng 2 đúng ngày VN.
- Qua 0h00, ESP bỏ dòng 2 cũ (không còn "hôm nay"), dùng dòng 1 cho tới lần fetch thành công kế tiếp.

Lỗi POST / timeout / status ≠ 200 ⇒ ESP giữ lịch cache, thử lại ở lần heartbeat kế (5 phút, hoặc sớm hơn nếu có event mới).

### Quy tắc parse ở ESP

- ESP chỉ phải parse 2 dòng lịch — bằng C thuần (`strtok`/`strtoul`), không cần thư viện JSON. Heartbeat JSON chỉ được tạo ra (`snprintf`), không bao giờ parse.
- Format canonical không chứa khoảng trắng (`25200:900,61200`); parser chấp nhận CRLF và khoảng trắng thừa quanh số.
- Mỗi dòng tối đa **20 mốc**, nhận theo thứ tự xuất hiện, phần dư bỏ.
- Entry sai bị **bỏ lẻ**, không văng cả file: token không phải số, giây ≥ 86400, hoặc duration = 0.
- Cả hai dòng đều không có mốc hợp lệ nào ⇒ coi như fetch lỗi, giữ cache. (Server không sinh trạng thái này — "hôm nay không tưới" là dòng 1 có mốc + dòng 2 rỗng.)
- Request gửi bằng **HTTP/1.0** (`Connection: close`) nên response không chunked; ESP đọc body thẳng vào buffer cố định, body lịch tối đa **1023 byte** (dài hơn ⇒ coi như lịch lỗi, giữ cache).
- HTTPS qua `WiFiClientSecure` (TLS). Timeout 10 giây **cho từng pha** (kết nối TCP, bắt tay TLS, đọc response) — một request xấu nhất có thể kéo dài vài chục giây, nhưng không bao giờ chặn scheduler bơm (1 giây/lần) vì network chạy trong FreeRTOS task riêng.

### TLS

- Server public qua **Cloudflare Tunnel** hoặc chứng chỉ **Let's Encrypt** (chưa chốt cái nào).
- ESP **kiểm tra chứng chỉ** bằng nhiều root CA nhúng trong firmware: `WiFiClientSecure::setCACert()` với **một chuỗi PEM nối 7 root** (`firmware/src/ca_bundle.cpp`), **không** pin một root CA đơn lẻ và **không** `setInsecure()`. 7 root:
  - ISRG Root X1, ISRG Root X2 (Let's Encrypt)
  - GTS Root R1, GTS Root R3, GTS Root R4 (Google Trust Services)
  - SSL.com Root Certification Authority RSA, SSL.com Root Certification Authority ECC
- Lý do: Cloudflare tự xoay nhà cấp chứng chỉ (Let's Encrypt, Google Trust Services, SSL.com…) — pin một root thì tới lúc đổi CA thiết bị mất kết nối âm thầm. Danh sách 7 root dùng được cho cả hai phương án, chỉ tốn ~10 KB flash (nhẹ hơn nhúng cả CA bundle Mozilla qua `setCACertBundle()`). Cập nhật danh sách: nối lại các file PEM như ghi chú đầu `ca_bundle.cpp` rồi build lại.
- `http://` được chấp nhận **chỉ để thử/demo** (vd. server chạy trong LAN): dùng `WiFiClient` thường, không mã hoá. Token, lịch tưới và header `Date` đi dạng rõ — ai cùng mạng cũng đọc được token, sửa được lịch và giờ. Portal và Serial log đều cảnh báo. Triển khai thật luôn dùng `https://`.

## Scheduler & an toàn bơm

- Mỗi giây so giờ **đồng hồ hệ thống** (không đọc I2C) với lịch hiện hành; đến mốc thì bật bơm, tắt sau duration.
- **Mỗi lần bơm đổi trạng thái ghi một event**: bật ⇒ `start`, tắt ⇒ `stop` (`reason` `done` / `capped` / `abort`); mốc bị bỏ ⇒ `skipped`. Tối đa 1 event mỗi tick (giây); `start` của phiên sau không bao giờ cùng tick với `stop` của phiên trước. Có event ⇒ báo network task gửi heartbeat sau ~3 s.
- **Hard-cap 30 phút/phiên** — duration lớn hơn bị cắt còn 30 phút (bơm rửa xe không nên chạy liên tục lâu hơn); phiên bị cắt kết thúc bằng `stop` reason `capped`.
- **Nghỉ tối thiểu 3 phút** giữa hai lần bật bơm (chống bật/tắt dồn dập — kẻ thù chính của tuổi thọ bơm).
  - Mốc tới giờ mà chưa nghỉ đủ 3 phút ⇒ **bỏ mốc** (không hoãn), ghi event `skipped` reason `rest`.
  - Thời điểm tắt bơm gần nhất (lastStop) lưu trong AT24C32 để quy tắc này còn hiệu lực qua reboot. Ở cả hai chế độ, lastStop còn có bản sao trong RAM `RTC_NOINIT` (magic + check) giữ qua reset mềm, WDT, panic, brownout; lúc boot nạp giá trị mới hơn giữa AT24C32 và bản sao, bản sao bị bỏ khi khởi động do cấp điện lại (`ESP_RST_POWERON`) ⇒ chế độ RAM (không có AT24C32) chỉ mất lastStop khi mất điện, và lần tắt do mất điện được miễn quy tắc nghỉ.
- **Phiên đang chạy luôn chạy nốt** theo duration lúc bắt đầu, kể cả khi lịch mới về giữa chừng hoặc qua 0h00.
- Mốc tới giờ khi bơm đang chạy phiên khác ⇒ **bỏ mốc** (phiên đang chạy không kéo dài), ghi event `skipped` reason `busy`.
- **Đồng hồ không dùng được** (chưa hợp lệ, hoặc giờ hệ thống vô lý) ⇒ bơm tắt, phiên đang chạy kết thúc hẳn bằng `stop` reason `abort` (không bật lại giữa chừng khi đồng hồ có lại), bỏ tick. Trước mỗi lần chủ động khởi động lại (vd. thoát chế độ cấu hình) cũng kết thúc phiên đang chạy như vậy.
- **Tưới bù**: khi boot hoặc khi nhận lịch mới, nếu thời điểm hiện tại nằm trong khung `[mốc, mốc + duration)` của một mốc và bơm đang tắt ⇒ bật cho **phần thời gian còn lại** (vẫn qua quy tắc nghỉ và hard-cap), ghi `start` như phiên thường. Mốc đã qua hẳn khung ⇒ không bù.
  - Vd. mất điện lúc 07:03 giữa phiên 07:00–07:05, có điện lại 07:04 ⇒ tưới tiếp 1 phút. (Tắt do mất điện không ghi "thời điểm tắt bơm", nên quy tắc nghỉ không chặn phần bù này.)
  - Phiên bị mất điện chỉ có event `start` (không có `stop`); phần bù là cặp `start` 07:04 / `stop` 07:05 mới.
- Mọi lần bật/tắt (kể cả bị cap, tưới bù, abort) và mốc bị bỏ đều nằm trong `events` của heartbeat.

## Đồng bộ thời gian

- **Đồng hồ hệ thống ESP32 (`time()` / `settimeofday`) là đồng hồ chạy.** Scheduler đọc nó mỗi giây (giờ VN = UTC + 25200), không đọc I2C. Đồng hồ được coi là **hợp lệ** từ lần đầu được đặt từ một nguồn tin cậy:
  - DS3231 lúc boot (có module, cờ OSF tắt, giờ hợp lý), hoặc
  - NTP được `TimeTrust` chấp nhận, hoặc
  - header `Date` của response 200.

  Chưa hợp lệ ⇒ bơm tắt, không tưới (xem "Scheduler & an toàn bơm"). Giờ hiển thị/lịch theo UTC+7.
- **DS3231 chỉ để khôi phục giờ lúc boot.** Mỗi lần đồng hồ hệ thống được đặt/chỉnh (NTP được nhận, `Date`) và DS3231 dùng được (`ok` / `lost_power`), firmware ghi luôn giờ đó vào DS3231 (làm tròn tới giây, xoá cờ OSF). Ghi lỗi (đã thử 3 lần) ⇒ DS3231 `failed`, không dùng nữa; tưới vẫn theo đồng hồ hệ thống. Không có module ⇒ chỉ đặt đồng hồ hệ thống.
- Có mạng: header **`Date`** của heartbeat HTTPS là nguồn giờ chính; NTP (mỗi 6 giờ) chỉ tinh chỉnh.
- Mất mạng: đồng hồ hệ thống tiếp tục chạy. Mất điện rồi có điện lại mà vẫn không có mạng ⇒ lấy giờ từ DS3231 lúc boot (nếu có module và pin còn) — xem "Failsafe".
- **SNTP không tự chỉnh đồng hồ.** Firmware thay hàm weak `sntp_sync_time()` của ESP-IDF: bản gốc gọi `settimeofday` ngay khi nhận gói NTP; bản thay chỉ ghi lại kết quả (kèm `esp_timer` lúc nhận). Network task chiếu giờ đó tới hiện tại, xét qua `TimeTrust`, được nhận thì mới `settimeofday` (và ghi DS3231).
- **NTP không xác thực** — ai giả được gói NTP trong LAN là xê dịch được đồng hồ (bơm tưới sai giờ). Kể cả chỉ cho NTP chỉnh từng bước nhỏ (vd. ≤ 300 s) thì kẻ tấn công vẫn dời được đồng hồ không giới hạn: mỗi lần sync (6 giờ một lần, và mỗi lần reboot) đẩy thêm 299 s. Nên mọi lần đặt đồng hồ (hệ thống + DS3231) đều qua `TimeTrust` (`firmware/lib/core/time_trust.*`), với *neo* là header `Date` đã được TLS xác thực:
  1. **Header `Date` là nguồn tin cậy.** Mỗi response **200** qua HTTPS có `Date` hợp lệ: tính `dateNow = Date + (monoSec() lúc này − monoSec() lúc nhận response)`. Đồng hồ chưa hợp lệ hoặc |đồng hồ − dateNow| > **5 s** ⇒ đặt đồng hồ = dateNow (và ghi DS3231), đồng hồ hợp lệ. Lệch ≤ 5 s ⇒ không chỉnh. Cả hai trường hợp đều lưu *neo* (`Date`, `monoSec()` lúc nhận response). Response không có `Date` hợp lệ ⇒ bỏ qua, neo cũ giữ nguyên.
  2. **Có neo (chưa quá 24 giờ)** ⇒ chỉ nhận NTP khi |NTP − (giờ neo + thời gian monotonic đã trôi từ lúc neo)| ≤ **5 s**; lệch hơn ⇒ bỏ, ghi log (có thể bị giả mạo). Lúc này đồng hồ hiện tại không được xét: NTP chỉ còn tác dụng tinh chỉnh dưới 5 s, không bao giờ kéo đồng hồ ra xa giờ đã xác thực quá 5 s.
  3. **Chưa có neo** (vừa boot, hoặc server không tới được, hoặc neo đã quá 24 giờ) ⇒ chỉ nhận NTP khi đồng hồ chưa hợp lệ (bootstrap — chưa có giờ nào tốt hơn) hoặc |NTP − đồng hồ| ≤ **5 s**. NTP lệch lớn **không bao giờ** ghi đè một đồng hồ đang hợp lệ: ESP bỏ giờ NTP đó, heartbeat ngay, và chờ header `Date` phân xử (đồng hồ sai thật thì quy tắc 1 chỉnh lại).
  4. Giờ NTP hoặc `Date` < 2026-01-01 hoặc ≥ **2100-01-01** (4102444800) ⇒ vô lý, bỏ (`Date` vô lý không đổi neo).
- Server **phải gửi header `Date` đúng giờ** — đồng hồ (và DS3231) được chỉnh theo nó mỗi khi lệch quá 5 s. Web server và Cloudflare đều tự gửi header này theo chuẩn; server chỉ cần đồng bộ giờ hệ thống (NTP phía server).
- Header `Date` phải theo dạng IMF-fixdate (RFC 7231: `Sun, 06 Nov 1994 08:49:37 GMT` — Go `net/http`, nginx, Apache, Cloudflare đều tự gửi). Parser chặt (`parseHttpDate`, `firmware/lib/core/http_date.*`): đúng 29 ký tự, không nhận định dạng RFC 850/asctime.
- Đồng hồ chưa hợp lệ không chặn TLS (nên bootstrap qua `Date` được): mbedTLS của Arduino-ESP32 build không bật `MBEDTLS_HAVE_TIME_DATE` nên không kiểm hạn chứng chỉ.

## Failsafe

- Lịch của lần fetch thành công gần nhất được cache (AT24C32 hoặc NVS — xem "Lưu trữ"), chỉ ghi khi nội dung thay đổi.
- Mất WiFi hoặc server chết: ESP vẫn tưới đúng lịch cache theo đồng hồ hệ thống; phiên chạy lúc này mang `source: "failsafe"`, events gửi bù khi mạng trở lại.
- Mất điện trong lúc không có mạng — khi có điện lại:

| Module | Giờ sau boot | Tưới |
|---|---|---|
| DS3231 `ok` (pin còn) | Lấy từ DS3231 | Tưới tiếp theo lịch cache (kể cả tưới bù). |
| Không có module, hoặc DS3231 `lost_power` / `failed` | Chưa có giờ | **Không tưới cho tới khi có mạng lại** (NTP hoặc `Date` đặt giờ). Bơm luôn tắt khi chưa có giờ. |

- Không có AT24C32: queue events (chưa gửi) và lastStop nằm trong RAM ⇒ mất khi mất điện / reset (lastStop giữ qua reset mềm nhờ bản sao `RTC_NOINIT`, chỉ mất khi mất điện).

## Lưu trữ

Cấu hình (WiFi, Server URL, Token) **luôn ở NVS**, không bao giờ chép sang module. Phần còn lại tuỳ chế độ chọn lúc boot (xem "Module DS3231 + AT24C32 tuỳ chọn"):

| Dữ liệu | Có AT24C32 (`at24c32: "ok"`) | Không có / hỏng AT24C32 |
|---|---|---|
| WiFi SSID/password, Server URL, Token | NVS | NVS |
| Queue events (ring buffer 200 × 10 B ≈ 2 KB) + lastStop | AT24C32 — giữ qua mất điện | RAM 4 KB (`RamStorage`, cùng layout và cùng code `EventQueue`) — mất khi reset (lastStop: bản sao `RTC_NOINIT` giữ qua reset mềm) |
| Lịch cache | AT24C32 `0x0800` — ghi khi body hoặc ngày fetch đổi; **không ghi NVS** (lần lưu AT24C32 thành công đầu tiên mỗi lần boot xoá khoá `sched` / `fday` cũ trong NVS nếu có) | NVS — body ghi khi đổi; khoá `fday` (ngày fetch) **chỉ ghi khi body có dòng 2** (rỗng hoặc có mốc), nên không ghi flash mỗi ngày |

- AT24C32: EEPROM 4 KB, ~1 triệu lần ghi — hợp cho dữ liệu ghi thường xuyên, không làm mòn flash ESP.
- AT24C32 hỏng lúc chạy ⇒ chuyển sang cột phải tới lần boot sau (queue RAM mới rỗng, lastStop giữ).
- Ngày fetch (`fetchDay`) chỉ dùng để biết dòng 2 thuộc ngày nào; body không có dòng 2 thì ngày fetch không quan trọng.

**Sơ đồ bộ nhớ AT24C32** (little-endian; `RamStorage` dùng cùng địa chỉ cho phần queue):

| Địa chỉ | Nội dung |
|---|---|
| `0x0000` | Header queue `{u16 magic = 0x4E43, u16 head, u16 count}` |
| `0x0008` | `u32 lastStop` (giờ VN, epoch; 0 = chưa từng) |
| `0x0010`–`0x07DF` | 200 record × 10 B `{u32 at, u8 type, u8 source, u8 reason, 3 B dự trữ = 0}` — `at` là giờ VN (epoch); `type` 0 start / 1 stop / 2 skipped; `source` 0 normal / 1 today / 2 failsafe; `reason` 0 không có / 1 done / 2 capped / 3 abort / 4 rest / 5 busy |
| `0x07E0`–`0x07FF` | Trống |
| `0x0800`–`0x0C0A` | Lịch cache: header 12 B `{u16 magic = 0x5343, u16 len, u32 fetchDay, u32 crc}` + body `len` byte (1 ≤ len ≤ 1023, không chứa `\0`) |
| `0x0C0B`–`0x0FFF` | Trống |

- Queue: ghi record trước, header sau. Magic khác `0x4E43` (vd. `0x4E42` của bản cũ, record `{start, stop}`) ⇒ format một lần (queue rỗng, lastStop = 0).
- Lịch cache: `crc` = CRC-32 IEEE (như zlib) trên 6 byte `{len, fetchDay}` rồi body. Ghi body trước, header sau; chỉ đổi `fetchDay` ⇒ chỉ ghi lại header. Magic / len / CRC sai (vd. mất điện giữa lúc ghi) ⇒ coi như không có cache.

## Phần để sau (không thuộc bản này)

- ~~Module PHP~~ → đã thay bằng **server Go** (`server/`): heartbeat + trả lịch từ `data/schedule.json` + log `heartbeats.jsonl`/`events.jsonl`. Chỉ 1 thiết bị, kiểm tra token + `id`.
- **Module thời tiết**: chỉnh dòng 2 theo dự báo.
- **Web-client**: xem trạng thái thiết bị, sửa lịch.
- **Enclosure**: đã có file `.blend` ban đầu.
