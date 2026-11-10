#pragma once

// Điều khiển bơm qua MOSFET ở PUMP_PIN (HIGH = bật).
namespace pump {
// Gọi đầu tiên trong setup(): đặt chân OUTPUT và kéo LOW ngay (bơm tắt).
void begin();
void set(bool on);
bool isOn();
}  // namespace pump
