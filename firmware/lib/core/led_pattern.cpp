#include "led_pattern.h"

LedState pickLed(bool pumpOn, bool clockValid, bool wifiConnected) {
    if (pumpOn) return LedState::Pumping;
    if (!clockValid) return LedState::NoClock;
    if (!wifiConnected) return LedState::NoWifi;
    return LedState::Idle;
}

bool ledOn(LedState s, uint32_t ms) {
    switch (s) {
        case LedState::Pumping: return ms % 300 < 150;
        case LedState::NoWifi: return ms % 2000 < 1000;
        case LedState::NoClock: {
            const uint32_t t = ms % 2000;
            return t < 100 || (t >= 200 && t < 300);
        }
        case LedState::Idle: return ms % 5000 < 50;
    }
    return false;
}
