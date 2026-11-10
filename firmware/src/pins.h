#pragma once
#include <stdint.h>

// Gán chân ESP32-C3 Super Mini (tạm, chưa có sơ đồ mạch).
// Không dùng GPIO2/8/9 cho bơm: là chân strapping / LED / nút BOOT.
// Tên tránh trùng symbol của core Arduino (core đã có BOOT_PIN, SDA, SCL, LED_BUILTIN).
constexpr uint8_t PUMP_PIN = 4;          // gate MOSFET, HIGH = bơm chạy
constexpr uint8_t I2C_SDA = 5;
constexpr uint8_t I2C_SCL = 6;
constexpr uint8_t LED_PIN = 8;           // active-LOW
constexpr uint8_t BOOT_BUTTON_PIN = 9;   // active-LOW (nút BOOT)

constexpr uint32_t I2C_FREQ = 100000;  // Hz
