#include "shared.h"

#include <esp_timer.h>

#include "at24c32.h"
#include "ds3231.h"

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
