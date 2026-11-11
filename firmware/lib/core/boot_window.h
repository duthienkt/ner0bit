#pragma once
// Cửa sổ nút BOOT lúc khởi động (thuần, test native).
// Bắt đầu nhấn trong kBootWindowMs đầu ⇒ giữ liên tục kBootHoldMs là vào setup, kể cả khi việc giữ
// kéo dài quá cửa sổ. Nhả trước khi đủ ⇒ chạy thường. Không nhấn trong cửa sổ ⇒ chạy thường.
#include <stdint.h>

constexpr uint32_t kBootWindowMs = 5000;
constexpr uint32_t kBootHoldMs = 3000;

enum class BootDecision : uint8_t { Waiting, Setup, Normal };

class BootWindow {
   public:
    explicit BootWindow(uint32_t startMs) : start_(startMs) {}
    BootDecision update(uint32_t nowMs, bool pressed);
    bool holding() const { return holding_; }

   private:
    uint32_t start_;
    uint32_t pressedSince_ = 0;
    bool holding_ = false;
};
