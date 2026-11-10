#include "net_task.h"

#include <Arduino.h>
#include <HTTPClient.h>
#include <WiFi.h>
#include <WiFiClientSecure.h>
#include <esp_mac.h>
#include <esp_sntp.h>
#include <string.h>
#include <sys/time.h>
#include <time.h>

#include "ca_bundle.h"
#include "ds3231.h"
#include "heartbeat.h"
#include "http_date.h"
#include "pump.h"
#include "schedule.h"
#include "shared.h"
#include "time_trust.h"

namespace {

constexpr const char* kFw = "0.1.0";
constexpr uint32_t kTaskStack = 12 * 1024;      // byte (ESP-IDF tính stack theo byte)
constexpr uint32_t kLoopMs = 1000;
constexpr uint32_t kWifiRetryS = 30;            // chưa vào được WiFi ⇒ begin lại sau 30 s
constexpr uint32_t kHeartbeatS = 300;
constexpr uint32_t kDrainS = 5;                 // 200 OK mà queue còn events ⇒ gửi tiếp sau 5 s
constexpr uint32_t kNtpIntervalS = 6 * 3600;    // chỉnh DS3231 theo NTP mỗi 6 giờ
constexpr uint32_t kHttpTimeoutMs = 10000;

NetConfig s_cfg;
char s_id[13];                                  // MAC STA, 12 hex chữ thường
WiFiClientSecure* s_tls = nullptr;              // cấp một lần trên heap, dùng lại mỗi request

// Buffer lớn để static, không chiếm stack task.
Event s_events[kMaxEventsPerHeartbeat];
char s_json[kHeartbeatBufSize];
char s_body[kScheduleBodyMax];
Schedule s_sched;
TimeTrust s_trust;                              // chỉ net task dùng, không cần g_lock

volatile bool s_ntpSynced = false;              // set từ callback SNTP (task lwIP)

void onNtpSync(struct timeval*) { s_ntpSynced = true; }

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

// Gọi khi callback SNTP báo đã sync. NTP không xác thực ⇒ chỉ ghi DS3231 theo quy tắc TimeTrust
// (docs/system-design.md, "Đồng bộ thời gian"). true nếu nên heartbeat ngay: đồng hồ vừa chuyển
// từ không hợp lệ ⇒ hợp lệ (áp lịch), hoặc có ứng viên lệch lớn cần header Date xác nhận.
bool ntpApply() {
    const long long sys = (long long)time(nullptr);
    const uint32_t ntp = sys < 0 ? 0u : (sys > 0xFFFFFFFFLL ? 0xFFFFFFFFu : (uint32_t)sys);
    const uint32_t mono = monoSec();
    NtpVerdict v;
    uint32_t rtc = 0;
    bool wasValid;
    bool rtcOk;
    bool ok = false;
    {
        SharedLock lock;
        wasValid = g_clockValid;
        rtcOk = wasValid && ds3231::readUtc(rtc);  // đọc lỗi ⇒ coi như đồng hồ chưa hợp lệ (R4)
        v = s_trust.onNtp(ntp, rtc, rtcOk, mono);
        if (v == NtpVerdict::Accept) {
            ok = ds3231::writeUtc(ntp);
            if (ok) g_clockValid = true;
        }
    }
    const long long skew = (long long)ntp - (long long)rtc;
    switch (v) {
        case NtpVerdict::Reject:
            Serial.printf("[net] NTP: giờ vô lý (%llu), bỏ qua\n", (unsigned long long)ntp);
            return false;
        case NtpVerdict::Pending:
            Serial.printf("[net] NTP: lệch %lld s so với DS3231 (> %lu s) — chưa ghi, chờ header "
                          "Date qua HTTPS xác nhận\n",
                          skew, (unsigned long)kMaxClockSkewS);
            return true;
        case NtpVerdict::Accept:
            break;
    }
    if (!ok) {
        Serial.println("[net] NTP: ghi DS3231 lỗi");
    } else if (rtcOk) {
        Serial.printf("[net] NTP: đã chỉnh DS3231 utc=%lu (lệch %lld s)\n", (unsigned long)ntp, skew);
    } else {
        Serial.printf("[net] NTP: đã chỉnh DS3231 utc=%lu (đồng hồ trước đó chưa hợp lệ)\n",
                      (unsigned long)ntp);
    }
    return ok && !wasValid;
}

// Header Date của response 200 qua HTTPS (TLS đã xác thực server) ⇒ xác nhận hoặc bỏ ứng viên
// NTP đang chờ. mono: monoSec() lúc nhận response. Không giữ g_lock khi gọi.
void checkServerDate(const char* date, uint32_t mono) {
    if (!s_trust.hasPending()) return;
    uint32_t dateUtc = 0;
    if (!parseHttpDate(date, dateUtc)) {
        Serial.printf("[net] Date: header thiếu hoặc sai định dạng (\"%s\"), ứng viên NTP vẫn chờ\n",
                      date);
        return;
    }
    uint32_t utc = 0;
    if (s_trust.onServerDate(dateUtc, mono, utc) != DateVerdict::Confirm) {
        Serial.printf("[net] NTP: ứng viên không khớp header Date (%lu) quá %lu s ⇒ bỏ, giữ "
                      "DS3231\n",
                      (unsigned long)dateUtc, (unsigned long)kMaxClockSkewS);
        return;
    }
    bool ok;
    {
        SharedLock lock;
        utc += monoSec() - mono;  // thời gian trôi từ lúc nhận response tới giờ
        ok = ds3231::writeUtc(utc);
        if (ok) g_clockValid = true;
    }
    if (ok) {
        Serial.printf("[net] NTP: header Date xác nhận, đã chỉnh DS3231 utc=%lu\n",
                      (unsigned long)utc);
    } else {
        Serial.println("[net] NTP: header Date xác nhận nhưng ghi DS3231 lỗi");
    }
}

void markFetchFailed() {
    SharedLock lock;
    g_sched.setFetchOk(false);
}

// Đọc body 200 vào s_body. false nếu lỗi đọc hoặc quá kScheduleBodyMax - 1 byte.
bool readBody(HTTPClient& http) {
    const int size = http.getSize();  // -1 khi chunked / không có Content-Length
    if (size >= (int)kScheduleBodyMax) {
        Serial.printf("[net] body quá dài (%d byte)\n", size);
        return false;
    }
    String body = http.getString();
    if (body.length() >= kScheduleBodyMax) {
        Serial.printf("[net] body quá dài (%u byte)\n", (unsigned)body.length());
        return false;
    }
    memcpy(s_body, body.c_str(), body.length() + 1);
    return true;
}

// Gửi một heartbeat. Trả true khi nên gửi tiếp sớm (200 OK và queue còn events).
bool heartbeat() {
    if (strncmp(s_cfg.url, "https://", 8) != 0) {
        Serial.println("[net] lỗi: Server URL không bắt đầu bằng https:// — bỏ qua POST");
        markFetchFailed();
        return false;
    }

    uint16_t n;
    bool pumpOn;
    bool clockOk;
    uint32_t rtc = 0;
    {
        SharedLock lock;
        n = g_queue.peek(s_events, kMaxEventsPerHeartbeat);
        pumpOn = pump::isOn();
        clockOk = g_clockValid && localNow(rtc);
    }
    if (!clockOk) rtc = 0;  // đồng hồ không dùng được ⇒ "rtc" = 1970, server thấy ngay

    HeartbeatInfo info{s_id, kFw, monoSec(), (int)WiFi.RSSI(), pumpOn, rtc};
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
    http.setReuse(false);
    http.setConnectTimeout(kHttpTimeoutMs);
    http.setTimeout(kHttpTimeoutMs);
    if (!http.begin(*s_tls, s_cfg.url)) {
        Serial.println("[net] lỗi: HTTPClient.begin thất bại (URL sai?)");
        markFetchFailed();
        return false;
    }
    http.addHeader("Content-Type", "application/json");
    http.addHeader("Authorization", auth);
    const char* collect[] = {"Date"};
    http.collectHeaders(collect, 1);
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
    checkServerDate(date, respMono);  // trước khi áp lịch: fetchDay theo giờ đã chỉnh
    const bool parsed = bodyOk && parseSchedule(s_body, s_sched);

    bool applied = false;
    uint32_t fetchDay = 0;
    uint16_t remaining;
    {
        SharedLock lock;
        g_queue.pop(n);  // 200 ⇒ xoá events vừa gửi, kể cả khi lịch lỗi
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

    if (applied) {
        config::saveScheduleIfChanged(s_body, fetchDay);  // NVS, không cần g_lock
        Serial.printf("[net] 200: lịch mới (day %lu), queue còn %u\n", (unsigned long)fetchDay,
                      (unsigned)remaining);
    } else if (parsed) {
        Serial.printf("[net] 200: đồng hồ chưa dùng được, chưa áp lịch; queue còn %u\n",
                      (unsigned)remaining);
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

    sntp_set_time_sync_notification_cb(onNtpSync);

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

    for (;;) {
        vTaskDelay(pdMS_TO_TICKS(kLoopMs));
        const uint32_t now = monoSec();
        const bool connected = WiFi.status() == WL_CONNECTED;

        if (!connected) {
            if (wasConnected) {
                Serial.println("[net] WiFi: mất kết nối");
                wasConnected = false;
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
        if (s_ntpSynced) {
            s_ntpSynced = false;
            ntpLastOk = now;
            ntpEverOk = true;
            // Đồng hồ vừa hợp lệ ⇒ heartbeat ngay để áp lịch (lần trước có thể đã bỏ vì giờ sai).
            // Có ứng viên lệch lớn ⇒ heartbeat ngay để header Date xác nhận sớm.
            if (ntpApply()) nextHb = now;
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

void startNetTask(const NetConfig& cfg) {
    s_cfg = cfg;
    s_cfg.ssid[sizeof s_cfg.ssid - 1] = '\0';
    s_cfg.pass[sizeof s_cfg.pass - 1] = '\0';
    s_cfg.url[sizeof s_cfg.url - 1] = '\0';
    s_cfg.token[sizeof s_cfg.token - 1] = '\0';
    if (xTaskCreate(netTask, "net", kTaskStack, nullptr, 1, nullptr) != pdPASS) {
        Serial.println("[net] lỗi: không tạo được task mạng");
    }
}
