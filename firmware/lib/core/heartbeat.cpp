#include "heartbeat.h"

#include <stdarg.h>
#include <stdio.h>
#include <string.h>

#include "civil_time.h"

// Ghi v ra đúng `width` chữ số thập phân (có số 0 đầu).
static void putDigits(char* p, unsigned v, int width) {
    for (int i = width - 1; i >= 0; i--) {
        p[i] = (char)('0' + v % 10);
        v /= 10;
    }
}

void formatIsoLocal(uint32_t local, char* buf) {
    int32_t y;
    unsigned mo, d;
    civilFromDays((int32_t)(local / 86400), y, mo, d);  // uint32_t local ⇒ y ∈ [1970, 2106]
    const uint32_t sod = local % 86400;
    // "YYYY-MM-DDTHH:MM:SS+07:00"
    putDigits(buf, (unsigned)y, 4);
    buf[4] = '-';
    putDigits(buf + 5, mo, 2);
    buf[7] = '-';
    putDigits(buf + 8, d, 2);
    buf[10] = 'T';
    putDigits(buf + 11, sod / 3600, 2);
    buf[13] = ':';
    putDigits(buf + 14, sod / 60 % 60, 2);
    buf[16] = ':';
    putDigits(buf + 17, sod % 60, 2);
    memcpy(buf + 19, "+07:00", 7);  // gồm '\0'
}

static const char* sourceName(Source s) {
    switch (s) {
        case Source::Today:
            return "today";
        case Source::Failsafe:
            return "failsafe";
        case Source::Normal:
        default:
            return "normal";
    }
}

// Tên mặc định khi record hỏng (giá trị enum lạ) nằm trong tập tên hợp lệ, không dài hơn tên dài
// nhất ⇒ kHeartbeatBufSize vẫn đủ.
static const char* typeName(EventType t) {
    switch (t) {
        case EventType::Start:
            return "start";
        case EventType::Skipped:
            return "skipped";
        case EventType::Stop:
        default:
            return "stop";
    }
}

static const char* reasonName(Reason r) {
    switch (r) {
        case Reason::Capped:
            return "capped";
        case Reason::Abort:
            return "abort";
        case Reason::Rest:
            return "rest";
        case Reason::Busy:
            return "busy";
        case Reason::Done:
        default:
            return "done";
    }
}

static const char* moduleName(ModuleState m) {
    switch (m) {
        case ModuleState::Ok:
            return "ok";
        case ModuleState::LostPower:
            return "lost_power";
        case ModuleState::Failed:
            return "failed";
        case ModuleState::Absent:
        default:
            return "absent";
    }
}

namespace {
// Ghi tích luỹ bằng snprintf; một lần tràn là hỏng cả chuỗi.
struct Writer {
    char* buf;
    size_t cap;
    size_t len;
    bool ok;

    void add(const char* fmt, ...) {
        if (!ok) return;
        va_list ap;
        va_start(ap, fmt);
        int w = vsnprintf(buf + len, cap - len, fmt, ap);
        va_end(ap);
        if (w < 0 || (size_t)w >= cap - len) {
            ok = false;
            return;
        }
        len += (size_t)w;
    }
};
}  // namespace

size_t buildHeartbeat(char* buf, size_t cap, const HeartbeatInfo& h, const Event* ev, uint16_t n) {
    if (buf == nullptr || cap == 0) return 0;
    Writer w = {buf, cap, 0, true};
    char ts[26];

    w.add("{\"id\":\"%s\",\"fw\":\"%s\",\"uptime_s\":%lu,\"rssi\":%d,\"pump_on\":%s,"
          "\"heap_free\":%lu,\"heap_min\":%lu,\"ds3231\":\"%s\",\"at24c32\":\"%s\",",
          h.id, h.fw, (unsigned long)h.uptimeS, h.rssi, h.pumpOn ? "true" : "false", (unsigned long)h.heapFree,
          (unsigned long)h.heapMin, moduleName(h.ds3231), moduleName(h.at24c32));
    if (h.tempQuarterC != kTempUnknown) {
        // 0,25 °C/đơn vị ⇒ phần lẻ luôn .00/.25/.50/.75; in bằng số nguyên, không dùng float.
        const int32_t q = h.tempQuarterC;
        const uint32_t a = (uint32_t)(q < 0 ? -q : q);
        w.add("\"temp_c\":%s%lu.%02u,", q < 0 ? "-" : "", (unsigned long)(a / 4), (unsigned)(a % 4 * 25));
    }
    formatIsoLocal(h.rtcLocal, ts);
    w.add("\"queue\":%u,\"rtc\":\"%s\",\"events\":[", (unsigned)h.queueLen, ts);

    for (uint16_t i = 0; i < n && ev != nullptr; i++) {
        const Event& e = ev[i];
        formatIsoLocal(e.at, ts);
        w.add("%s{\"type\":\"%s\",\"at\":\"%s\",\"source\":\"%s\"", i ? "," : "", typeName(e.type), ts,
              sourceName(e.source));
        // Start không có reason; record hỏng (type lạ) in như stop nên cũng có reason.
        if (e.type == EventType::Start) {
            w.add("}");
        } else {
            w.add(",\"reason\":\"%s\"}", reasonName(e.reason));
        }
    }
    w.add("]}");

    if (!w.ok) {
        buf[0] = '\0';
        return 0;
    }
    return w.len;
}
