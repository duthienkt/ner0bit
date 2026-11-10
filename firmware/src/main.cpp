// Ner0bit firmware — ESP32-C3 Super Mini.
// loop(): scheduler bơm 1 lần/giây theo đồng hồ hệ thống. Network task (net_task) chạy song song:
// WiFi, NTP, heartbeat. Module DS3231 + AT24C32 tuỳ chọn (xem shared.h).
// Chế độ cấu hình (portal) thay cho network task khi chưa có cấu hình hoặc giữ BOOT lúc boot.
#include <Arduino.h>
#include <Wire.h>

#include "config_store.h"
#include "net_task.h"
#include "pins.h"
#include "portal.h"
#include "pump.h"
#include "schedule.h"
#include "schedule_store.h"
#include "shared.h"

static char s_schedBody[kScheduleBodyMax];  // static: không chiếm stack loopTask
static Schedule s_schedule;
static NetConfig s_cfg;

// Mỗi module một dòng log, rồi một dòng chế độ lưu trữ.
static void logModules(ModuleState ds, ModuleState at, bool clockValid, uint32_t rtcUtc) {
    switch (ds) {
        case ModuleState::Ok:
            if (clockValid) {
                Serial.printf("[main] DS3231: ok — đồng hồ hệ thống lấy từ DS3231 (utc=%lu)\n",
                              (unsigned long)rtcUtc);
            } else {
                Serial.printf("[main] DS3231: ok nhưng giờ vô lý (utc=%lu) — chờ NTP / server\n",
                              (unsigned long)rtcUtc);
            }
            break;
        case ModuleState::LostPower:
            Serial.println("[main] DS3231: lost_power — cờ OSF bật (pin CR2032?), giờ không tin "
                           "được — chờ NTP / server");
            break;
        case ModuleState::Failed:
            Serial.println("[main] DS3231: failed — có ACK nhưng đọc lỗi, bỏ dùng — chờ NTP / "
                           "server");
            break;
        case ModuleState::Absent:
            Serial.println("[main] DS3231: absent — không có module, chờ NTP / server (chưa có giờ "
                           "thì không tưới)");
            break;
    }
    switch (at) {
        case ModuleState::Ok:
            Serial.println("[main] AT24C32: ok");
            break;
        case ModuleState::Failed:
            Serial.println("[main] AT24C32: failed — có ACK nhưng đọc/ghi header queue lỗi, bỏ "
                           "dùng");
            break;
        case ModuleState::LostPower:  // không dùng cho AT24C32
        case ModuleState::Absent:
            Serial.println("[main] AT24C32: absent");
            break;
    }
    Serial.println(at == ModuleState::Ok
                       ? "[main] lưu trữ: AT24C32 (queue events, lastStop, lịch cache)"
                       : "[main] lưu trữ: RAM (queue events — mất khi reset; lastStop — giữ qua "
                         "reset mềm, mất khi mất điện) + NVS (lịch cache)");
}

void setup() {
    // 1. Bơm tắt trước mọi thứ, rồi Serial, lock, LED (active-LOW, tắt).
    pump::begin();
    Serial.begin(115200);
    sharedInit();
    pinMode(LED_PIN, OUTPUT);
    digitalWrite(LED_PIN, HIGH);

    // 2. Cửa sổ 5 s: giữ BOOT ≥ 3 s ⇒ chế độ cấu hình.
    const bool button = portal::bootButtonHeld();
    Serial.printf("[main] Ner0bit fw 0.1.0, nút BOOT: %s\n", button ? "giữ ⇒ setup" : "không");

    // 3. I2C, module DS3231 / AT24C32 (đều tuỳ chọn), đồng hồ hệ thống, storage.
    Wire.begin(I2C_SDA, I2C_SCL, I2C_FREQ);
    uint32_t rtcUtc = 0;
    ModuleState ds;
    ModuleState at;
    bool clockValid;
    uint16_t queued;
    LastStopBoot ls;
    {
        SharedLock lock;
        clockBoot(rtcUtc);
        storageBoot();
        ls = lastStopBoot();
        g_sched.setFetchOk(false);
        ds = g_ds3231;
        at = g_at24c32;
        clockValid = g_clockValid;
        queued = g_queue.count();
    }
    if (ls.switched) logStorageFailover();
    logModules(ds, at, clockValid, rtcUtc);
    if (ls.retainedValid) {
        Serial.printf("[main] queue %u events, lastStop %lu (storage %lu, bản sao giữ qua reset "
                      "%lu)\n",
                      (unsigned)queued, (unsigned long)ls.seed, (unsigned long)ls.stored,
                      (unsigned long)ls.retained);
    } else {
        Serial.printf("[main] queue %u events, lastStop %lu (bản sao giữ qua reset: không có)\n",
                      (unsigned)queued, (unsigned long)ls.seed);
    }

    // 4. Lịch cache (AT24C32 hoặc NVS theo chế độ storage).
    uint32_t fetchDay = 0;
    const bool hasCache = schedule_store::load(s_schedBody, sizeof s_schedBody, fetchDay);
    const bool cacheOk = hasCache && parseSchedule(s_schedBody, s_schedule);
    if (cacheOk) {
        SharedLock lock;
        g_sched.setSchedule(s_schedule, fetchDay);
    }
    Serial.printf("[main] lịch cache: %s\n",
                  cacheOk ? "đã nạp" : (hasCache ? "lỗi, bỏ qua" : "chưa có"));

    // 5. Chế độ cấu hình hoặc chạy thường.
    const bool hasCfg = config::load(s_cfg);
    if (!hasCfg || button) {
        Serial.println(hasCfg ? "[main] vào chế độ cấu hình (nút BOOT)"
                              : "[main] chưa có cấu hình ⇒ chế độ cấu hình");
        portal::run(hasCfg ? &s_cfg : nullptr);  // không return
    }
    startNetTask(s_cfg);
}

void loop() {
    schedulerTick();  // tự giới hạn 1 lần / 1000 ms
    delay(20);
}
