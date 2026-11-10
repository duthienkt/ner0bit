#include "net_task.h"

#include <Arduino.h>
#include <HTTPClient.h>
#include <WiFi.h>
#include <WiFiClientSecure.h>
#include <esp_mac.h>
#include <esp_sntp.h>
#include <esp_system.h>
#include <esp_timer.h>
#include <string.h>
#include <sys/time.h>
#include <time.h>

#include "ca_bundle.h"
#include "heartbeat.h"
#include "http_date.h"
#include "pump.h"
#include "schedule.h"
#include "schedule_format.h"
#include "schedule_store.h"
#include "shared.h"
#include "time_trust.h"

namespace {

constexpr const char* kFw = kFwVersion;
constexpr uint32_t kTaskStack = 12 * 1024;      // byte (ESP-IDF tính stack theo byte)
constexpr uint32_t kLoopMs = 1000;
constexpr uint32_t kWifiRetryS = 30;            // chưa vào được WiFi ⇒ begin lại sau 30 s
constexpr uint32_t kHeartbeatS = 300;
constexpr uint32_t kDrainS = 5;                 // 200 OK mà queue còn events ⇒ gửi tiếp sau 5 s
constexpr uint32_t kEventDelayS = 3;            // có event mới ⇒ gửi sau 3 s (gộp các event kế)
constexpr uint32_t kNtpIntervalS = 6 * 3600;    // tinh chỉnh đồng hồ theo NTP mỗi 6 giờ
constexpr uint32_t kHttpTimeoutMs = 10000;

NetConfig s_cfg;
char s_id[13];                                  // MAC STA, 12 hex chữ thường
WiFiClientSecure* s_tls = nullptr;              // cấp một lần trên heap, dùng lại mỗi request
WiFiClient* s_plain = nullptr;                  // cho Server URL http:// (chỉ dùng thử/demo)

// Buffer lớn để static, không chiếm stack task.
Event s_events[kMaxEventsPerHeartbeat];
char s_json[kHeartbeatBufSize];
char s_body[kScheduleBodyMax];
Schedule s_sched;
char s_schedLog[kScheduleLogMax];   // dòng log lịch dễ đọc (static: không chiếm stack task)
char s_lastBody[kScheduleBodyMax];  // body lịch đã áp lần trước ⇒ log "MỚI" / "không đổi"
uint32_t s_lastDay = 0;
TimeTrust s_trust;                              // chỉ net task dùng, không cần g_lock
TaskHandle_t volatile s_task = nullptr;

// Kết quả SNTP chờ net task xét (sntp_sync_time ghi từ task lwIP). Bảo vệ bằng s_ntpMux.
portMUX_TYPE s_ntpMux = portMUX_INITIALIZER_UNLOCKED;
bool s_ntpPending = false;
int64_t s_ntpUs = 0;    // giờ NTP (µs UTC)
int64_t s_ntpAtUs = 0;  // esp_timer_get_time() lúc nhận

bool elapsed(uint32_t now, uint32_t since, uint32_t interval) { return now - since >= interval; }

void makeId() {
    uint8_t mac[6] = {0};
    esp_read_mac(mac, ESP_MAC_WIFI_STA);
    snprintf(s_id, sizeof s_id, "%02x%02x%02x%02x%02x%02x", mac[0], mac[1], mac[2], mac[3], mac[4],
             mac[5]);
}

void wifiBegin() {
    Serial.printf("[net] WiFi: kết nối SSID \"%s\"\n", s_cfg.ssid);
    WiFi.disconnect();
    WiFi.begin(s_cfg.ssid, s_cfg.pass);
}

// (Re)start SNTP. Lần sync kế tiếp do lwIP tự làm theo chu kỳ kNtpIntervalS.
void ntpStart() {
    Serial.println("[net] NTP: bắt đầu sync (pool.ntp.org, time.google.com)");
    sntp_set_sync_interval(kNtpIntervalS * 1000UL);
    configTime(0, 0, "pool.ntp.org", "time.google.com");
}

// Lấy kết quả SNTP chưa xét (nếu có).
bool ntpTake(int64_t& ntpUs, int64_t& atUs) {
    portENTER_CRITICAL(&s_ntpMux);
    const bool pending = s_ntpPending;
    s_ntpPending = false;
    ntpUs = s_ntpUs;
    atUs = s_ntpAtUs;
    portEXIT_CRITICAL(&s_ntpMux);
    return pending;
}

uint32_t clampUtc(int64_t sec) {
    return sec < 0 ? 0u : (sec > 0xFFFFFFFFLL ? 0xFFFFFFFFu : (uint32_t)sec);
}

const char* rtcNote(RtcWrite rw) {
    switch (rw) {
        case RtcWrite::Ok: return ", đã ghi DS3231";
        case RtcWrite::Failed: return ", ghi DS3231 LỖI ⇒ bỏ dùng DS3231 tới lần khởi động sau";
        case RtcWrite::NoModule: break;
    }
    return "";
}

// Xét kết quả SNTP. NTP không xác thực ⇒ chỉ chỉnh đồng hồ hệ thống (và DS3231) theo quy tắc
// TimeTrust (docs/system-design.md, "Đồng bộ thời gian"): trong ±5 s quanh neo header Date, hoặc
// khi chưa có neo thì quanh đồng hồ hệ thống (chưa hợp lệ ⇒ nhận để bootstrap). true nếu nên
// heartbeat ngay: đồng hồ vừa chuyển từ không hợp lệ ⇒ hợp lệ (áp lịch), hoặc NTP lệch đồng hồ
// mà chưa có neo (header Date sẽ phân xử: đồng hồ sai thì được chỉnh theo Date).
bool ntpApply(int64_t ntpUs, int64_t atUs) {
    const uint32_t mono = monoSec();
    NtpVerdict v;
    uint32_t ntp = 0;
    uint32_t sys = 0;
    bool wasValid;
    RtcWrite rw = RtcWrite::NoModule;
    {
        SharedLock lock;
        // Chiếu giờ NTP tới lúc này theo esp_timer: đồng hồ hệ thống có thể đã được chỉnh (Date)
        // từ lúc nhận gói.
        const int64_t nowUs = ntpUs + (esp_timer_get_time() - atUs);
        ntp = clampUtc(nowUs < 0 ? -1 : nowUs / 1000000);
        wasValid = g_clockValid;
        sys = systemUtc();
        v = s_trust.onNtp(ntp, sys, wasValid, mono);
        if (v == NtpVerdict::Accept) rw = setClock(ntp, (uint32_t)(nowUs % 1000000));
    }
    uint32_t ref = 0;
    const bool anchored = s_trust.anchorNow(mono, ref);
    if (v == NtpVerdict::Reject) {
        if (ntp < kMinValidUtc || ntp >= kMaxValidUtc) {
            Serial.printf("[net] NTP: giờ vô lý (%lu), bỏ qua\n", (unsigned long)ntp);
        } else if (anchored) {
            Serial.printf("[net] NTP: lệch %lld s so với neo header Date (> %lu s) — bỏ qua, có "
                          "thể bị giả mạo\n",
                          (long long)ntp - (long long)ref, (unsigned long)kTrustWindowS);
        } else {
            Serial.printf("[net] NTP: lệch %lld s so với đồng hồ (> %lu s), chưa có neo header "
                          "Date — bỏ qua, chờ heartbeat\n",
                          (long long)ntp - (long long)sys, (unsigned long)kTrustWindowS);
            return true;
        }
        return false;
    }
    if (wasValid) {
        Serial.printf("[net] NTP: đã tinh chỉnh đồng hồ utc=%lu (lệch %lld s)%s\n",
                      (unsigned long)ntp, (long long)ntp - (long long)sys, rtcNote(rw));
    } else {
        Serial.printf("[net] NTP: đã đặt đồng hồ utc=%lu (trước đó chưa hợp lệ)%s\n",
                      (unsigned long)ntp, rtcNote(rw));
    }
    return !wasValid;
}

// Header Date của response 200 qua HTTPS (TLS đã xác thực server) là neo thời gian: đồng hồ hệ
// thống chưa hợp lệ hoặc lệch > 5 s ⇒ chỉnh theo Date (và ghi DS3231). respMono: monoSec() lúc
// nhận response. Không giữ g_lock khi gọi.
void applyServerDate(const char* date, uint32_t respMono) {
    uint32_t dateUtc = 0;
    if (!parseHttpDate(date, dateUtc)) {
        Serial.printf("[net] Date: header thiếu hoặc sai định dạng (\"%s\"), không neo được giờ\n",
                      date);
        return;
    }
    DateVerdict v;
    uint32_t sys = 0;
    bool wasValid;
    uint32_t utc = 0;
    RtcWrite rw = RtcWrite::NoModule;
    {
        SharedLock lock;
        wasValid = g_clockValid;
        sys = systemUtc();
        v = s_trust.onServerDate(dateUtc, respMono, sys, wasValid, monoSec(), utc);
        if (v == DateVerdict::Write) rw = setClock(utc, 0);
    }
    switch (v) {
        case DateVerdict::Reject:
            Serial.printf("[net] Date: giờ vô lý (%lu), bỏ qua\n", (unsigned long)dateUtc);
            return;
        case DateVerdict::NoChange:
            return;
        case DateVerdict::Write:
            break;
    }
    if (wasValid) {
        Serial.printf("[net] Date: đồng hồ lệch %lld s ⇒ đã chỉnh utc=%lu%s\n",
                      (long long)sys - (long long)utc, (unsigned long)utc, rtcNote(rw));
    } else {
        Serial.printf("[net] Date: đã đặt đồng hồ utc=%lu (trước đó chưa hợp lệ)%s\n",
                      (unsigned long)utc, rtcNote(rw));
    }
}

void markFetchFailed() {
    SharedLock lock;
    g_sched.setFetchOk(false);
}

// Đọc body 200 thẳng từ stream vào s_body (không cấp phát String), tối đa kScheduleBodyMax − 1
// byte, cả pha đọc trong kHttpTimeoutMs. false nếu lỗi/timeout, thiếu byte so với Content-Length,
// hoặc chạm giới hạn (body dài hơn ⇒ lịch lỗi). Request dùng HTTP/1.0 nên body không chunked.
bool readBody(HTTPClient& http) {
    const int size = http.getSize();  // -1 khi không có Content-Length (body hết khi đóng kết nối)
    if (size >= (int)kScheduleBodyMax) {
        Serial.printf("[net] body quá dài (%d byte)\n", size);
        return false;
    }
    const String te = http.header("Transfer-Encoding");
    if (te.length() > 0 && !te.equalsIgnoreCase("identity")) {
        Serial.println("[net] body có Transfer-Encoding (không hỗ trợ với HTTP/1.0)");
        return false;
    }
    const size_t limit = size >= 0 ? (size_t)size : kScheduleBodyMax - 1;
    size_t len = 0;
    NetworkClient* stream = size == 0 ? nullptr : http.getStreamPtr();
    if (size != 0 && !stream) {
        Serial.println("[net] lỗi đọc body: mất kết nối");
        return false;
    }
    const uint32_t t0 = millis();
    while (len < limit) {
        if (millis() - t0 >= kHttpTimeoutMs) {
            Serial.printf("[net] lỗi đọc body: timeout (%u byte)\n", (unsigned)len);
            return false;
        }
        const int avail = stream->available();
        if (avail > 0) {
            size_t want = limit - len;
            if ((size_t)avail < want) want = (size_t)avail;
            const int got = stream->read((uint8_t*)s_body + len, want);
            if (got > 0) {
                len += (size_t)got;
                continue;
            }
        } else if (!stream->connected()) {
            break;  // server đóng kết nối
        }
        vTaskDelay(pdMS_TO_TICKS(5));
    }
    s_body[len] = '\0';
    if (size >= 0 && len < (size_t)size) {
        Serial.printf("[net] lỗi đọc body: nhận %u/%d byte\n", (unsigned)len, size);
        return false;
    }
    if (size < 0 && len >= kScheduleBodyMax - 1) {
        Serial.printf("[net] body quá dài (≥ %u byte)\n", (unsigned)len);
        return false;
    }
    return true;
}

// Gửi một heartbeat. Trả true khi nên gửi tiếp sớm (200 OK và queue còn events).
bool heartbeat() {
    const bool isHttps = strncmp(s_cfg.url, "https://", 8) == 0;
    const bool isHttp = strncmp(s_cfg.url, "http://", 7) == 0;
    if (!isHttps && !isHttp) {
        Serial.println("[net] lỗi: Server URL không bắt đầu bằng https:// hoặc http:// — bỏ qua POST");
        markFetchFailed();
        return false;
    }

    uint16_t n;
    uint16_t queueLen;
    uint32_t droppedAtPeek;
    uint32_t generation;
    bool pumpOn;
    bool clockOk;
    uint32_t rtc = 0;
    int16_t temp;
    ModuleState ds;
    ModuleState at;
    {
        SharedLock lock;
        n = g_queue.peek(s_events, kMaxEventsPerHeartbeat);
        queueLen = g_queue.count();
        droppedAtPeek = g_queue.dropped();
        generation = storageGeneration();
        pumpOn = pump::isOn();
        clockOk = g_clockValid && localNow(rtc);
        temp = readDs3231Temp();  // lỗi ⇒ bỏ "temp_c"; lỗi liên tiếp ⇒ DS3231 failed
        ds = g_ds3231;
        at = g_at24c32;
    }
    if (!clockOk) rtc = 0;  // đồng hồ không dùng được ⇒ "rtc" = 1970, server thấy ngay

    HeartbeatInfo info{s_id,
                       kFw,
                       monoSec(),
                       (int)WiFi.RSSI(),
                       pumpOn,
                       (uint32_t)esp_get_free_heap_size(),
                       (uint32_t)esp_get_minimum_free_heap_size(),
                       ds,
                       at,
                       temp,
                       queueLen,
                       rtc};
    const size_t len = buildHeartbeat(s_json, sizeof s_json, info, s_events, n);
    if (len == 0) {
        Serial.println("[net] lỗi: buildHeartbeat không đủ buffer");
        markFetchFailed();
        return false;
    }

    char auth[8 + sizeof s_cfg.token];
    snprintf(auth, sizeof auth, "Bearer %s", s_cfg.token);

    Serial.printf("[net] POST %s (%u events, %u byte)\n", s_cfg.url, (unsigned)n, (unsigned)len);
    HTTPClient http;
    http.useHTTP10(true);  // không chunked ⇒ đọc body thô từ stream được (readBody); kèm reuse=false
    http.setReuse(false);
    http.setConnectTimeout(kHttpTimeoutMs);
    http.setTimeout(kHttpTimeoutMs);
    // http:// không mã hoá: token, lịch và header Date đều có thể bị đọc/sửa trên đường truyền.
    if (!http.begin(isHttps ? static_cast<WiFiClient&>(*s_tls) : *s_plain, s_cfg.url)) {
        Serial.println("[net] lỗi: HTTPClient.begin thất bại (URL sai?)");
        markFetchFailed();
        return false;
    }
    http.addHeader("Content-Type", "application/json");
    http.addHeader("Authorization", auth);
    const char* collect[] = {"Date", "Transfer-Encoding"};
    http.collectHeaders(collect, 2);
    const int code = http.POST((uint8_t*)s_json, len);
    const uint32_t respMono = monoSec();

    if (code != 200) {
        if (code < 0) {
            Serial.printf("[net] POST lỗi: %s\n", HTTPClient::errorToString(code).c_str());
        } else {
            Serial.printf("[net] POST status %d\n", code);
        }
        http.end();
        markFetchFailed();
        return false;
    }

    char date[32];
    strlcpy(date, http.header("Date").c_str(), sizeof date);  // dài hơn ⇒ cụt ⇒ parse lỗi
    const bool bodyOk = readBody(http);
    http.end();
    applyServerDate(date, respMono);  // trước khi áp lịch: fetchDay theo giờ đã chỉnh
    const bool parsed = bodyOk && parseSchedule(s_body, s_sched);

    bool applied = false;
    uint32_t fetchDay = 0;
    uint16_t remaining;
    bool switched;
    {
        SharedLock lock;
        // 200 ⇒ xoá events vừa gửi, kể cả khi lịch lỗi. Queue đầy mà scheduler push trong lúc
        // gửi ⇒ vài event đã gửi bị ghi đè rồi: chỉ pop phần còn lại, không pop event chưa gửi.
        // Storage đã đổi (AT24C32 hỏng ⇒ RAM) trong lúc gửi ⇒ events đã gửi nằm ở queue cũ, queue
        // mới chỉ có event chưa gửi: không pop.
        if (storageGeneration() == generation) g_queue.popSent(n, droppedAtPeek);
        switched = storageFailover();
        remaining = g_queue.count();
        uint32_t now = 0;
        if (parsed && g_clockValid && localNow(now)) {
            fetchDay = now / 86400;
            g_sched.setSchedule(s_sched, fetchDay);
            g_sched.setFetchOk(true);
            applied = true;
        } else {
            g_sched.setFetchOk(false);
        }
    }

    if (switched) logStorageFailover();
    if (parsed) formatSchedule(s_sched, s_schedLog, sizeof s_schedLog);
    if (applied) {
        schedule_store::saveIfChanged(s_body, fetchDay, s_sched.l2 != Line2State::Missing);
        // Dòng 2 gắn với ngày nhận ⇒ cùng body nhưng sang ngày mới (có dòng 2) cũng tính là mới.
        const bool changed = strcmp(s_body, s_lastBody) != 0 ||
                             (s_sched.l2 != Line2State::Missing && fetchDay != s_lastDay);
        strlcpy(s_lastBody, s_body, sizeof s_lastBody);
        s_lastDay = fetchDay;
        Serial.printf("[net] 200: lịch %s (day %lu), queue còn %u\n",
                      changed ? "MỚI" : "không đổi", (unsigned long)fetchDay, (unsigned)remaining);
        Serial.printf("[net] lịch: %s\n", s_schedLog);
    } else if (parsed) {
        Serial.printf("[net] 200: đồng hồ chưa dùng được, chưa áp lịch; queue còn %u\n",
                      (unsigned)remaining);
        Serial.printf("[net] lịch (chưa áp): %s\n", s_schedLog);
    } else {
        Serial.printf("[net] 200: body lịch không hợp lệ, giữ cache; queue còn %u\n",
                      (unsigned)remaining);
    }
    return remaining > 0;
}

void netTask(void*) {
    makeId();
    Serial.printf("[net] task start, id=%s\n", s_id);

    s_tls = new WiFiClientSecure;
    s_tls->setCACert(kCaBundle);
    s_tls->setHandshakeTimeout(kHttpTimeoutMs / 1000);  // giây
    s_plain = new WiFiClient;
    if (strncmp(s_cfg.url, "http://", 7) == 0) {
        Serial.println("[net] CẢNH BÁO: Server URL dùng http:// — không mã hoá, token và lịch đi dạng rõ."
                       " Chỉ dùng để thử/demo.");
    }

    WiFi.persistent(false);  // không ghi đè cấu hình WiFi vào NVS mỗi lần begin
    WiFi.mode(WIFI_STA);     // chỉ STA — mất WiFi thì thử lại, không bao giờ mở AP
    WiFi.setAutoReconnect(true);
    wifiBegin();

    bool wasConnected = false;
    bool ntpStarted = false;
    uint32_t lastBegin = monoSec();
    uint32_t ntpKick = 0;        // lần gọi configTime gần nhất
    uint32_t ntpLastOk = 0;      // lần sync NTP thành công gần nhất
    bool ntpEverOk = false;
    uint32_t nextHb = 0;
    bool eventHb = false;        // có event mới, chờ tới eventHbAt rồi heartbeat
    uint32_t eventHbAt = 0;

    for (;;) {
        // Chờ tối đa 1 s; schedulerTick có event ⇒ notifyNetTask đánh thức sớm.
        const bool notified = ulTaskNotifyTake(pdTRUE, pdMS_TO_TICKS(kLoopMs)) > 0;
        const uint32_t now = monoSec();
        const bool connected = WiFi.status() == WL_CONNECTED;
        // Hạn tính từ event đầu tiên, event đến sau trong 3 s không dời hạn ⇒ gộp, trễ tối đa ~3 s.
        if (notified && !eventHb) {
            eventHb = true;
            eventHbAt = now + kEventDelayS;
        }

        if (!connected) {
            eventHb = false;  // events nằm trong queue, gửi khi có WiFi lại (heartbeat ngay lúc đó)
            // Không heartbeat được ⇒ coi như fetch lỗi: phiên mới mang source failsafe. Báo ngay
            // khi vừa mất WiFi, rồi mỗi lần tới hạn heartbeat.
            if (wasConnected) {
                Serial.println("[net] WiFi: mất kết nối");
                wasConnected = false;
                markFetchFailed();
                nextHb = now + kHeartbeatS;
            } else if ((int32_t)(now - nextHb) >= 0) {
                markFetchFailed();
                nextHb = now + kHeartbeatS;
            }
            if (elapsed(now, lastBegin, kWifiRetryS)) {
                wifiBegin();
                lastBegin = now;
            }
            continue;
        }

        if (!wasConnected) {
            wasConnected = true;
            Serial.printf("[net] WiFi: đã kết nối, IP %s, RSSI %d\n",
                          WiFi.localIP().toString().c_str(), (int)WiFi.RSSI());
            nextHb = now;  // heartbeat ngay khi có WiFi
        }

        // NTP: khởi động lần đầu có WiFi; lwIP tự sync lại mỗi 6 giờ. Nếu quá 6 giờ không có
        // lần sync thành công nào thì khởi động lại SNTP.
        if (!ntpStarted) {
            ntpStart();
            ntpStarted = true;
            ntpKick = now;
        } else if (elapsed(now, ntpKick, kNtpIntervalS) &&
                   (!ntpEverOk || elapsed(now, ntpLastOk, kNtpIntervalS + 600))) {
            ntpStart();
            ntpKick = now;
        }
        int64_t ntpUs = 0;
        int64_t ntpAtUs = 0;
        if (ntpTake(ntpUs, ntpAtUs)) {
            ntpLastOk = now;
            ntpEverOk = true;
            // Đồng hồ vừa hợp lệ ⇒ heartbeat ngay để áp lịch (lần trước có thể đã bỏ vì giờ sai).
            // NTP lệch đồng hồ mà chưa có neo ⇒ heartbeat ngay để header Date phân xử sớm.
            if (ntpApply(ntpUs, ntpAtUs)) nextHb = now;
        }

        if (eventHb && (int32_t)(now - eventHbAt) >= 0) {
            eventHb = false;
            uint16_t queued;
            {
                SharedLock lock;
                queued = g_queue.count();
            }
            // Queue rỗng ⇒ event đã đi cùng heartbeat vừa gửi, không gửi thêm.
            if (queued > 0 && (int32_t)(nextHb - now) > 0) nextHb = now;
        }

        if ((int32_t)(now - nextHb) >= 0) {
            const bool more = heartbeat();
            nextHb = monoSec() + (more ? kDrainS : kHeartbeatS);
            Serial.printf("[net] heartbeat kế tiếp sau %lu s (stack trống %u byte)\n",
                          (unsigned long)(more ? kDrainS : kHeartbeatS),
                          (unsigned)uxTaskGetStackHighWaterMark(nullptr));
        }
    }
}

}  // namespace

// Thay bản weak của ESP-IDF (esp_sntp.h): bản gốc settimeofday ngay khi nhận gói NTP, bỏ qua
// TimeTrust. Ở đây chỉ ghi lại kết quả; net task xét rồi mới chỉnh đồng hồ (ntpApply).
// Chạy trong task lwIP.
extern "C" void sntp_sync_time(struct timeval* tv) {
    const int64_t at = esp_timer_get_time();
    portENTER_CRITICAL(&s_ntpMux);
    s_ntpUs = (int64_t)tv->tv_sec * 1000000LL + tv->tv_usec;
    s_ntpAtUs = at;
    s_ntpPending = true;
    portEXIT_CRITICAL(&s_ntpMux);
    sntp_set_sync_status(SNTP_SYNC_STATUS_COMPLETED);
}

void notifyNetTask() {
    TaskHandle_t t = s_task;
    if (t) xTaskNotifyGive(t);
}

void startNetTask(const NetConfig& cfg) {
    s_cfg = cfg;
    s_cfg.ssid[sizeof s_cfg.ssid - 1] = '\0';
    s_cfg.pass[sizeof s_cfg.pass - 1] = '\0';
    s_cfg.url[sizeof s_cfg.url - 1] = '\0';
    s_cfg.token[sizeof s_cfg.token - 1] = '\0';
    TaskHandle_t t = nullptr;
    if (xTaskCreate(netTask, "net", kTaskStack, nullptr, 1, &t) != pdPASS) {
        Serial.println("[net] lỗi: không tạo được task mạng");
        return;
    }
    s_task = t;
}
