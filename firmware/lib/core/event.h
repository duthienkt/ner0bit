#pragma once
#include <stdint.h>

enum class Source : uint8_t { Normal = 0, Today = 1, Failsafe = 2 };

// Một event cho mỗi lần bơm đổi trạng thái, cộng event bỏ mốc.
enum class EventType : uint8_t { Start = 0, Stop = 1, Skipped = 2 };

// Start: None. Stop: Done | Capped (phiên bị cắt bởi hard cap) | Abort (Scheduler::abort).
// Skipped: Rest | Busy.
enum class Reason : uint8_t { None = 0, Done = 1, Capped = 2, Abort = 3, Rest = 4, Busy = 5 };

// at: local epoch. Start = lúc bật, Stop = lúc tắt, Skipped = mốc (absStart) bị bỏ.
struct Event {
    uint32_t at;
    EventType type;
    Source source;
    Reason reason;
};
