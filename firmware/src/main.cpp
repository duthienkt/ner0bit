// TẠM (Task 5): chỉ dùng tối thiểu các driver để chúng được biên dịch + link.
// Task 7 viết lại toàn bộ file này.
#include <Arduino.h>
#include <Wire.h>

#include "at24c32.h"
#include "config_store.h"
#include "ds3231.h"
#include "pins.h"
#include "pump.h"

static At24c32 g_eeprom;

void setup() {
    pump::begin();
    Serial.begin(115200);
    Wire.begin(I2C_SDA, I2C_SCL, I2C_FREQ);

    const bool rtcOk = ds3231::begin();
    const bool clockValid = rtcOk && !ds3231::lostPower();
    uint32_t utc = 0;
    if (clockValid && !ds3231::readUtc(utc)) utc = 0;

    uint8_t probe = 0;
    const bool eepromOk = g_eeprom.read(0, &probe, 1);

    NetConfig cfg;
    const bool hasCfg = config::load(cfg);
    static char body[1024];
    uint32_t fetchDay = 0;
    const bool hasSched = config::loadSchedule(body, sizeof body, fetchDay);

    Serial.printf("rtc=%d valid=%d utc=%lu eeprom=%d cfg=%d sched=%d\n", rtcOk, clockValid,
                  (unsigned long)utc, eepromOk, hasCfg, hasSched);

    if (false) {  // giữ symbol cho link-check, không chạy
        ds3231::writeUtc(utc);
        g_eeprom.write(0, &probe, 1);
        config::save(cfg);
        config::saveScheduleIfChanged(body, fetchDay);
        (void)config::hasConfig();
        pump::set(pump::isOn());
    }
}

void loop() { delay(1000); }
