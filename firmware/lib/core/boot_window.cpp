#include "boot_window.h"

BootDecision BootWindow::update(uint32_t nowMs, bool pressed) {
    const uint32_t elapsed = nowMs - start_;  // an toàn khi millis() quay vòng
    if (pressed) {
        if (!holding_) {
            if (elapsed >= kBootWindowMs) return BootDecision::Normal;  // nhấn sau cửa sổ: bỏ qua
            holding_ = true;
            pressedSince_ = nowMs;
        }
        if (nowMs - pressedSince_ >= kBootHoldMs) return BootDecision::Setup;
        return BootDecision::Waiting;  // đang giữ: chờ tiếp, kể cả quá cửa sổ
    }
    if (holding_) {
        holding_ = false;  // nhả trước khi đủ 3 s
        if (elapsed >= kBootWindowMs) return BootDecision::Normal;
    }
    return elapsed >= kBootWindowMs ? BootDecision::Normal : BootDecision::Waiting;
}
