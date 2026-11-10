#pragma once
#include <stdint.h>

// RTC DS3231 qua Wire (địa chỉ 0x68). Lưu giờ UTC, chế độ 24 h.
// Wire.begin(...) phải được gọi trước. Không tự khoá: caller giữ mutex bus I2C.
namespace ds3231 {
// true nếu chip trả ACK.
bool begin();
// Đọc giờ UTC (Unix epoch, giây). false nếu lỗi I2C hoặc thanh ghi chứa giá trị vô lý.
bool readUtc(uint32_t& utc);
// Ghi giờ UTC và xoá cờ OSF. false nếu lỗi I2C.
bool writeUtc(uint32_t utc);
// Đọc cờ OSF (bit 7 thanh ghi 0x0F) vào stopped: dao động đã dừng (mất nguồn, pin hết) ⇒ giờ
// không tin được. false nếu lỗi I2C (stopped không đổi).
bool oscStopped(bool& stopped);
// Nhiệt độ cảm biến trong chip, đơn vị 0,25 °C: thanh ghi 0x11 (°C, có dấu) + bit 7:6 của 0x12
// (phần tư độ). false nếu lỗi I2C.
bool readTempQuarters(int16_t& quarters);
}  // namespace ds3231
