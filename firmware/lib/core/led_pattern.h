#pragma once
// Nhịp đèn LED onboard theo trạng thái thiết bị (thuần, test native).
#include <stdint.h>

enum class LedState : uint8_t {
    Pumping,  // bơm đang chạy: chớp nhanh 150 ms / 150 ms
    NoClock,  // chưa có giờ ⇒ không tưới được: chớp đôi mỗi 2 s
    NoWifi,   // đang kết nối / mất WiFi: chớp dài 1 s / 1 s
    Idle,     // bình thường: nháy ngắn 50 ms mỗi 5 s
};

// Ưu tiên: bơm > chưa có giờ > mất WiFi > bình thường. (Setup mode do portal tự giữ đèn sáng.)
LedState pickLed(bool pumpOn, bool clockValid, bool wifiConnected);

// Đèn sáng hay tắt tại thời điểm ms (millis()).
bool ledOn(LedState s, uint32_t ms);
