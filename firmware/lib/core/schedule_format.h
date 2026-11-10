#pragma once
// Định dạng lịch đã parse thành một dòng dễ đọc cho Serial log, vd.
//   "hằng ngày: 07:00 15p, 17:00 5p | hôm nay: KHÔNG TƯỚI"
#include <stddef.h>

#include "schedule.h"

constexpr size_t kScheduleLogMax = 1024;  // đủ cho 2 × 20 mốc dạng dài nhất

// Ghi vào buf (luôn kết thúc '\0'), trả số byte đã ghi. Không đủ chỗ ⇒ cụt, kết thúc bằng "...".
size_t formatSchedule(const Schedule& s, char* buf, size_t cap);
