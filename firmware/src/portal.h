#pragma once

#include "config_store.h"

// Chế độ cấu hình: cửa sổ nút BOOT lúc boot + captive portal "Ner0bit-Setup".
namespace portal {

// Cửa sổ 5 s lúc boot: LED nháy nhanh (100 ms). true ngay khi nút BOOT được giữ liên tục ≥ 3 s.
// Hết cửa sổ (hoặc phát hiện giữ nút) thì LED tắt.
bool bootButtonHeld();

// AP mở "Ner0bit-Setup" + DNS captive về 192.168.4.1 + form SSID / password / Server URL / Token.
// current = cấu hình cũ (nullptr nếu chưa có): để trống password/token ⇒ giữ giá trị cũ;
// có cấu hình cũ mà 10 phút không lưu ⇒ restart. Lưu ⇒ config::save rồi restart.
// Scheduler bơm vẫn tick trong vòng lặp portal. Không bao giờ return.
[[noreturn]] void run(const NetConfig* current);

}  // namespace portal
