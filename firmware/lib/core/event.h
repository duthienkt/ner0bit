#pragma once
#include <stdint.h>

enum class Source : uint8_t { Normal = 0, Today = 1, Failsafe = 2 };
enum EventFlags : uint8_t { kCapped = 1, kSkippedRest = 2 };

// start/stop: local epoch. stop = 0 khi mốc bị bỏ (skipped).
struct Event {
    uint32_t start;
    uint32_t stop;
    Source source;
    uint8_t flags;
};
