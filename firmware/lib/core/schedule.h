#pragma once
#include <stdint.h>

constexpr int kMaxEntries = 20;
constexpr uint32_t kDefaultDuration = 300;

struct Entry {
    uint32_t start;
    uint32_t duration;  // chưa cap
};

struct Line {
    Entry e[kMaxEntries];
    uint8_t n;
};

enum class Line2State : uint8_t { Missing, Empty, Present };

struct Schedule {
    Line line1;
    Line line2;
    Line2State l2;
};

// false <=> cả hai dòng không có mốc hợp lệ (coi như fetch lỗi). out chỉ hợp lệ khi true.
bool parseSchedule(const char* body, Schedule& out);
