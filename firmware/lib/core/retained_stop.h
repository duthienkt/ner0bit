#pragma once
#include <stdint.h>

// Bản sao lastStop trong RAM giữ qua reset mềm (RTC_NOINIT_ATTR ở src/shared.cpp): còn nguyên qua
// ESP.restart(), WDT, panic, brownout; mất (rác) khi cấp điện lại. Nhờ đó quy tắc nghỉ 180 s vẫn
// đúng sau reset mềm cả ở chế độ RAM (không có AT24C32). Mất điện ⇒ bản sao coi như không có:
// lần tắt do mất điện được miễn quy tắc nghỉ. Logic thuần, không Arduino.

constexpr uint32_t kRetainedStopMagic = 0x4E30534Cu;  // "N0SL"

struct RetainedStop {
    uint32_t magic;
    uint32_t lastStop;  // giờ VN, epoch; 0 = chưa từng
    uint32_t check;     // ~(magic ^ lastStop)
};

// Ghi lastStop kèm magic và check.
void retainedStopWrite(RetainedStop& r, uint32_t lastStop);

// true nếu bản sao dùng được: không phải cấp điện lại (powerOn = esp_reset_reason() ==
// ESP_RST_POWERON), magic đúng và check khớp.
bool retainedStopValid(const RetainedStop& r, bool powerOn);

// lastStop nạp cho Scheduler lúc boot: giá trị mới hơn giữa bản trong storage (AT24C32; chế độ
// RAM thì 0) và bản sao giữ qua reset (nếu hợp lệ).
uint32_t retainedStopSeed(uint32_t stored, const RetainedStop& r, bool powerOn);
