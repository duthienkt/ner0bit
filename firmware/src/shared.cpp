#include "shared.h"

#include <stdio.h>

#include <stdarg.h>

#include <Arduino.h>
#include <esp_attr.h>
#include <esp_system.h>
#include <esp_timer.h>
#include <sys/time.h>
#include <time.h>

#include "at24c32.h"
#include "ds3231.h"
#include "net_task.h"
#include "pump.h"
#include "ram_storage.h"
#include "retained_stop.h"
#include "time_trust.h"

namespace {

constexpr int kBootReadAttempts = 3;  // đọc chip lúc boot: thử lại khi lỗi bus thoáng qua
constexpr int kWriteAttempts = 3;     // ghi AT24C32 / DS3231: thử lại trước khi coi là hỏng

At24c32 s_eeprom;
RamStorage s_ram;

// IStorage chuyển được giữa AT24C32 và RAM. g_queue giữ tham chiếu tới đối tượng này nên đổi
// storage không cần dựng lại queue. Ghi AT24C32 thử lại kWriteAttempts lần (ghi lại cùng byte
// cùng địa chỉ là vô hại); vẫn lỗi ⇒ đánh dấu để storageFailover() chuyển sang RAM.
class SwitchStorage : public IStorage {
public:
    bool onEeprom() const { return eeprom_; }
    void select(bool eeprom) {
        eeprom_ = eeprom;
        writeFailed_ = false;
    }
    bool takeWriteFailed() {
        const bool f = writeFailed_;
        writeFailed_ = false;
        return f;
    }
    bool read(uint16_t addr, uint8_t* buf, uint16_t len) override {
        return eeprom_ ? s_eeprom.read(addr, buf, len) : s_ram.read(addr, buf, len);
    }
    bool write(uint16_t addr, const uint8_t* buf, uint16_t len) override {
        if (!eeprom_) return s_ram.write(addr, buf, len);
        for (int i = 0; i < kWriteAttempts; i++) {
            if (s_eeprom.write(addr, buf, len)) return true;
        }
        writeFailed_ = true;
        return false;
    }

private:
    bool eeprom_ = false;
    bool writeFailed_ = false;
};

SwitchStorage s_store;
uint32_t s_generation = 0;

// Bản sao lastStop giữ qua reset mềm / WDT / panic / brownout (không được khởi tạo lúc boot).
// Cập nhật cùng lúc với lastStop trong queue, ở cả hai chế độ storage. Xem retained_stop.h.
RTC_NOINIT_ATTR RetainedStop s_retainedStop;
RTC_NOINIT_ATTR uint32_t s_setupRequest;  // = kSetupMagic ⇒ boot vào chế độ cấu hình
constexpr uint32_t kSetupMagic = 0x53455455;  // "SETU"

// Ghi lastStop xuống queue (AT24C32 / RAM) và bản sao giữ qua reset. Gọi trong lock.
void writeLastStop(uint32_t v) {
    g_queue.setLastStop(v);
    retainedStopWrite(s_retainedStop, v);
}

bool ds3231Usable() { return g_ds3231 == ModuleState::Ok || g_ds3231 == ModuleState::LostPower; }

}  // namespace

SemaphoreHandle_t g_lock = nullptr;
EventQueue g_queue(s_store);
Scheduler g_sched;
volatile bool g_clockValid = false;
ModuleState g_ds3231 = ModuleState::Absent;
ModuleState g_at24c32 = ModuleState::Absent;

namespace {
SemaphoreHandle_t s_logLock = nullptr;
char s_logBuf[1280];  // đủ cho dòng log lịch dài nhất (kScheduleLogMax) + tiền tố

void nlogv(const char* fmt, va_list ap) {
    if (s_logLock) xSemaphoreTake(s_logLock, portMAX_DELAY);
    int n;
    if (g_clockValid) {
        const time_t t = time(nullptr) + (time_t)kUtcOffset;
        struct tm tm;
        gmtime_r(&t, &tm);
        n = snprintf(s_logBuf, sizeof s_logBuf, "%02d:%02d:%02d ", tm.tm_hour, tm.tm_min, tm.tm_sec);
    } else {
        n = snprintf(s_logBuf, sizeof s_logBuf, "+%lus ", (unsigned long)monoSec());
    }
    if (n < 0) n = 0;
    const int m = vsnprintf(s_logBuf + n, sizeof s_logBuf - (size_t)n, fmt, ap);
    size_t len = (size_t)n + (m > 0 ? (size_t)m : 0);
    if (len >= sizeof s_logBuf) {  // cụt ⇒ vẫn kết thúc bằng xuống dòng
        len = sizeof s_logBuf - 1;
        s_logBuf[len - 1] = '\n';
    }
    Serial.write((const uint8_t*)s_logBuf, len);
    if (s_logLock) xSemaphoreGive(s_logLock);
}
}  // namespace

void nlog(const char* fmt, ...) {
    va_list ap;
    va_start(ap, fmt);
    nlogv(fmt, ap);
    va_end(ap);
}

void nlogln(const char* line) { nlog("%s\n", line); }

void sharedInit() {
    if (!g_lock) g_lock = xSemaphoreCreateMutex();
    if (!s_logLock) s_logLock = xSemaphoreCreateMutex();
}

void clockBoot(uint32_t& utc) {
    utc = 0;
    if (!ds3231::begin()) {
        g_ds3231 = ModuleState::Absent;
        return;
    }
    bool stopped = false;
    bool statusOk = false;
    for (int i = 0; i < kBootReadAttempts && !statusOk; i++) statusOk = ds3231::oscStopped(stopped);
    if (!statusOk) {
        g_ds3231 = ModuleState::Failed;
        return;
    }
    if (stopped) {
        g_ds3231 = ModuleState::LostPower;  // giờ trong chip không tin được, chờ NTP / Date
        return;
    }
    bool readOk = false;
    for (int i = 0; i < kBootReadAttempts && !readOk; i++) readOk = ds3231::readUtc(utc);
    if (!readOk) {
        utc = 0;
        g_ds3231 = ModuleState::Failed;
        return;
    }
    g_ds3231 = ModuleState::Ok;
    if (utc < kMinValidUtc || utc >= kMaxValidUtc) return;  // chip chạy nhưng giờ vô lý
    timeval tv;
    tv.tv_sec = (time_t)utc;
    tv.tv_usec = 0;
    settimeofday(&tv, nullptr);
    g_clockValid = true;
}

void storageBoot() {
    bool ack = false;
    for (int i = 0; i < kBootReadAttempts && !ack; i++) ack = At24c32::present();
    if (ack) {
        s_store.select(true);
        g_queue.begin();  // đọc header (thử lại), hỏng ⇒ format
        if (g_queue.available() && !s_store.takeWriteFailed()) {
            g_at24c32 = ModuleState::Ok;
            return;
        }
        g_at24c32 = ModuleState::Failed;
    } else {
        g_at24c32 = ModuleState::Absent;
    }
    s_store.select(false);
    g_queue.begin();  // RAM toàn 0 ⇒ format: queue rỗng, lastStop = 0
}

LastStopBoot lastStopBoot() {
    LastStopBoot b;
    const bool powerOn = esp_reset_reason() == ESP_RST_POWERON;
    b.stored = g_queue.lastStop();
    b.retainedValid = retainedStopValid(s_retainedStop, powerOn);
    b.retained = b.retainedValid ? s_retainedStop.lastStop : 0;
    b.seed = retainedStopSeed(b.stored, s_retainedStop, powerOn);
    // Chép giá trị mới hơn vào cả hai nơi: queue (AT24C32: một lần ghi, chỉ khi bản sao mới hơn)
    // và bản sao (cấp điện lại ⇒ ghi đè rác bằng giá trị storage).
    if (b.seed != b.stored) g_queue.setLastStop(b.seed);
    retainedStopWrite(s_retainedStop, b.seed);
    b.switched = storageFailover();
    g_sched.setLastStop(b.seed);
    return b;
}

bool storageOnEeprom() { return s_store.onEeprom(); }

IStorage& storageDevice() { return s_store; }

bool storageFailover() {
    if (!s_store.takeWriteFailed() || !s_store.onEeprom()) return false;
    const uint32_t lastStop = g_queue.lastStop();  // giá trị RAM, đã cập nhật dù ghi lỗi
    s_store.select(false);
    g_at24c32 = ModuleState::Failed;
    s_generation++;
    g_queue.begin();  // RAM chưa dùng (toàn 0) ⇒ queue mới rỗng
    g_queue.setLastStop(lastStop);
    return true;
}

uint32_t storageGeneration() { return s_generation; }

void logStorageFailover() {
    nlogln("[storage] lỗi ghi AT24C32 ⇒ chuyển sang RAM tới lần khởi động sau: queue "
                   "events mới (events chưa gửi trên AT24C32 bỏ), lastStop giữ, lịch cache ghi "
                   "NVS");
}

uint32_t systemUtc() {
    const long long t = (long long)time(nullptr);
    return t < 0 ? 0u : (t > 0xFFFFFFFFLL ? 0xFFFFFFFFu : (uint32_t)t);
}

bool localNow(uint32_t& out) {
    const uint32_t utc = systemUtc();
    if (utc < kMinValidUtc || utc >= kMaxValidUtc) return false;
    out = utc + kUtcOffset;
    return true;
}

RtcWrite setClock(uint32_t utc, uint32_t usec) {
    timeval tv;
    tv.tv_sec = (time_t)utc;
    tv.tv_usec = (suseconds_t)usec;
    settimeofday(&tv, nullptr);
    g_clockValid = true;
    if (!ds3231Usable()) return RtcWrite::NoModule;
    // DS3231 chỉ có giây và bắt đầu đếm giây mới từ lúc ghi ⇒ làm tròn để lệch ≤ 0,5 s.
    const uint32_t rtc = usec >= 500000 ? utc + 1 : utc;
    for (int i = 0; i < kWriteAttempts; i++) {
        if (ds3231::writeUtc(rtc)) return RtcWrite::Ok;
    }
    g_ds3231 = ModuleState::Failed;
    return RtcWrite::Failed;
}

int16_t readDs3231Temp() {
    static uint8_t s_fails = 0;  // lỗi liên tiếp; chỉ net task gọi, trong lock
    if (!ds3231Usable()) return kTempUnknown;
    int16_t q = 0;
    if (ds3231::readTempQuarters(q)) {
        s_fails = 0;
        return q;
    }
    if (++s_fails >= kDs3231TempFails) g_ds3231 = ModuleState::Failed;
    return kTempUnknown;
}

const char* resetReasonCode() {
    switch (esp_reset_reason()) {
        case ESP_RST_POWERON: return "poweron";
        case ESP_RST_SW: return "sw";
        case ESP_RST_PANIC: return "panic";
        case ESP_RST_INT_WDT: return "int_wdt";
        case ESP_RST_TASK_WDT: return "task_wdt";
        case ESP_RST_WDT: return "wdt";
        case ESP_RST_BROWNOUT: return "brownout";
        case ESP_RST_DEEPSLEEP: return "deepsleep";
        case ESP_RST_EXT: return "ext";
        default: return "unknown";  // USB, JTAG, eFuse, glitch nguồn, CPU lockup, ...
    }
}

const char* resetReasonText() {
    switch (esp_reset_reason()) {
        case ESP_RST_POWERON: return "cấp điện";
        case ESP_RST_SW: return "phần mềm (chủ động khởi động lại)";
        case ESP_RST_PANIC: return "lỗi phần mềm (panic)";
        case ESP_RST_INT_WDT: return "watchdog ngắt (treo)";
        case ESP_RST_TASK_WDT: return "watchdog (treo)";
        case ESP_RST_WDT: return "watchdog khác (treo)";
        case ESP_RST_BROWNOUT: return "sụt áp nguồn (brownout)";
        case ESP_RST_DEEPSLEEP: return "thức dậy từ deep sleep";
        case ESP_RST_EXT: return "chân reset ngoài";
        case ESP_RST_USB: return "USB (nạp firmware / mở cổng Serial)";
        default: return "không rõ";
    }
}

uint32_t monoSec() { return (uint32_t)(esp_timer_get_time() / 1000000ULL); }

namespace {

// Ghi kết quả tick/abort xuống queue. Gọi trong lock. true nếu AT24C32 vừa hỏng và đã chuyển
// sang RAM (caller log ngoài lock).
bool persist(const TickResult& r) {
    if (r.hasEvent) g_queue.push(r.event);
    if (r.lastStopChanged) writeLastStop(r.lastStop);
    if (!storageFailover()) return false;
    // Ghi lỗi giữa chừng: event vừa rồi có thể không nằm trọn trong queue AT24C32 (đã bỏ) ⇒
    // ghi lại vào queue RAM mới. lastStop đã được storageFailover chép sang.
    if (r.hasEvent) g_queue.push(r.event);
    return true;
}

void logEvent(const TickResult& r) {
    if (!r.hasEvent) return;
    nlog("[sched] event type=%u at=%lu source=%u reason=%u\n", (unsigned)r.event.type,
                  (unsigned long)r.event.at, (unsigned)r.event.source, (unsigned)r.event.reason);
}

}  // namespace

void schedulerTick() {
    static int64_t s_lastUs = 0;
    static bool s_ticked = false;
    static bool s_warned = false;  // đã log "đồng hồ không dùng được" (log một lần mỗi đợt)
    const int64_t nowUs = esp_timer_get_time();
    if (s_ticked && nowUs - s_lastUs < 1000000) return;
    s_ticked = true;
    s_lastUs = nowUs;

    TickResult r;
    bool wasOn;
    bool clockOk;
    bool switched;
    {
        SharedLock lock;
        wasOn = pump::isOn();
        uint32_t now = 0;
        clockOk = g_clockValid && localNow(now);
        // Đồng hồ không dùng được ⇒ kết thúc hẳn phiên đang chạy (không chỉ tắt bơm): nếu không,
        // lần sau đồng hồ dùng được sẽ bật lại bơm giữa chừng.
        r = clockOk ? g_sched.tick(now, monoSec()) : g_sched.abort(monoSec());
        pump::set(r.pumpOn);
        switched = persist(r);
    }
    if (r.hasEvent) notifyNetTask();  // heartbeat sớm (~3 s), gộp nhiều event

    if (switched) logStorageFailover();
    if (!clockOk) {
        if (!s_warned) {
            s_warned = true;
            nlogln("[sched] đồng hồ chưa hợp lệ — bơm tắt, bỏ tick (chờ NTP / server)");
        }
        if (r.hasEvent) nlogln("[sched] 🛑 dừng phiên đang chạy do đồng hồ không dùng được");
    } else {
        s_warned = false;
    }
    if (r.pumpOn != wasOn)
        nlogln(r.pumpOn ? "[sched] 💦 Bắt đầu tưới (bơm BẬT)" : "[sched] 🛑 Ngừng tưới (bơm TẮT)");
    if (r.hasEvent && r.event.type == EventType::Skipped)
        nlogln(r.event.reason == Reason::Rest ? "[sched] ⏭️ Bỏ mốc tưới (nghỉ giữa 2 phiên)"
                                              : "[sched] ⏭️ Bỏ mốc tưới (bận — phiên khác đang chạy)");
    logEvent(r);
}

void stopPumpForRestart() {
    TickResult r;
    bool switched;
    {
        SharedLock lock;
        r = g_sched.abort(monoSec());
        pump::set(false);
        switched = persist(r);
    }
    if (switched) logStorageFailover();
    if (r.hasEvent) nlogln("[sched] 🛑 dừng phiên đang chạy trước khi khởi động lại");
    logEvent(r);
}

void requestSetupOnNextBoot() { s_setupRequest = kSetupMagic; }

bool consumeSetupRequest() {
    const bool req = s_setupRequest == kSetupMagic && esp_reset_reason() != ESP_RST_POWERON;
    s_setupRequest = 0;
    return req;
}
