// Ner0bit firmware — ESP32-C3 Super Mini.
// loop(): scheduler bơm 1 lần/giây. Network task (net_task) chạy song song: WiFi, NTP, heartbeat.
// Chế độ cấu hình (portal) thay cho network task khi chưa có cấu hình hoặc giữ BOOT lúc boot.
#include <Arduino.h>
#include <Wire.h>

#include "config_store.h"
#include "ds3231.h"
#include "net_task.h"
#include "pins.h"
#include "portal.h"
#include "pump.h"
#include "schedule.h"
#include "shared.h"

static char s_schedBody[kScheduleBodyMax];  // static: không chiếm stack loopTask
static Schedule s_schedule;
static NetConfig s_cfg;

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

    // 3–4. I2C, queue, đồng hồ, lịch cache.
    Wire.begin(I2C_SDA, I2C_SCL, I2C_FREQ);
    uint32_t fetchDay = 0;
    const bool hasCache = config::loadSchedule(s_schedBody, sizeof s_schedBody, fetchDay);
    const bool cacheOk = hasCache && parseSchedule(s_schedBody, s_schedule);
    bool rtcOk;
    uint16_t queued;
    uint32_t lastStop;
    {
        SharedLock lock;
        g_queue.begin();
        g_sched.setLastStop(g_queue.lastStop());
        rtcOk = ds3231::begin();
        g_clockValid = rtcOk && !ds3231::lostPower();
        if (cacheOk) g_sched.setSchedule(s_schedule, fetchDay);
        g_sched.setFetchOk(false);
        queued = g_queue.count();
        lastStop = g_queue.lastStop();
    }
    Serial.printf("[main] DS3231 %s, đồng hồ %s; queue %u events, lastStop %lu\n",
                  rtcOk ? "OK" : "KHÔNG PHẢN HỒI", g_clockValid ? "hợp lệ" : "CHƯA HỢP LỆ (chờ NTP)",
                  (unsigned)queued, (unsigned long)lastStop);
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
