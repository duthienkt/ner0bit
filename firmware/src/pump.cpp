#include "pump.h"

#include <Arduino.h>

#include "pins.h"

namespace pump {

static bool s_on = false;

void begin() {
    // Đặt mức LOW trước khi bật output để chân không bao giờ lên HIGH thoáng qua.
    digitalWrite(PUMP_PIN, LOW);
    pinMode(PUMP_PIN, OUTPUT);
    digitalWrite(PUMP_PIN, LOW);
    s_on = false;
}

void set(bool on) {
    digitalWrite(PUMP_PIN, on ? HIGH : LOW);
    s_on = on;
}

bool isOn() { return s_on; }

}  // namespace pump
