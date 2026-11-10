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
// Cờ OSF (bit 7 thanh ghi 0x0F): dao động đã dừng ⇒ giờ không tin được.
// Lỗi I2C cũng trả true (coi như đồng hồ chưa hợp lệ).
bool lostPower();
}  // namespace ds3231
