#include "shared.h"

#include <Arduino.h>
#include <esp_timer.h>

#include "at24c32.h"
#include "ds3231.h"
#include "pump.h"

static At24c32 s_eeprom;

SemaphoreHandle_t g_lock = nullptr;
EventQueue g_queue(s_eeprom);
Scheduler g_sched;
volatile bool g_clockValid = false;

void sharedInit() {
    if (!g_lock) g_lock = xSemaphoreCreateMutex();
}

bool localNow(uint32_t& out) {
    uint32_t utc = 0;
    if (!ds3231::readUtc(utc)) return false;
    out = utc + kUtcOffset;
    return true;
}

uint32_t monoSec() { return (uint32_t)(esp_timer_get_time() / 1000000ULL); }

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
    {
        SharedLock lock;
        wasOn = pump::isOn();
        uint32_t now = 0;
        if (!g_clockValid || !localNow(now)) {
            pump::set(false);
            if (!s_warned) {
                s_warned = true;
                Serial.println("[sched] đồng hồ chưa hợp lệ / đọc DS3231 lỗi — bơm tắt, bỏ tick");
            }
            return;
        }
        s_warned = false;
        r = g_sched.tick(now, monoSec());
        pump::set(r.pumpOn);
        if (r.hasEvent) g_queue.push(r.event);
        if (r.lastStopChanged) g_queue.setLastStop(r.lastStop);
    }

    if (r.pumpOn != wasOn) Serial.printf("[sched] bơm %s\n", r.pumpOn ? "BẬT" : "TẮT");
    if (r.hasEvent) {
        Serial.printf("[sched] event start=%lu stop=%lu source=%u flags=%u\n",
                      (unsigned long)r.event.start, (unsigned long)r.event.stop,
                      (unsigned)r.event.source, (unsigned)r.event.flags);
    }
}
