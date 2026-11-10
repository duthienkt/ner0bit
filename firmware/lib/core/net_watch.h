#pragma once
#include <stdint.h>

// Giám sát network task. Net task không nằm trong task watchdog (một lần POST HTTPS có thể chặn
// ~30 s hợp lệ) mà mỗi vòng lặp đóng dấu monoSec() "còn sống"; loop() so dấu đó với giờ hiện tại.
// Logic thuần, không Arduino.

constexpr uint32_t kNetStaleS = 180;  // dấu cũ hơn 180 s ⇒ net task coi như treo

// true nếu dấu lastAlive cũ hơn limit giây (nowMono − lastAlive > limit; đúng limit thì chưa).
// Hiệu tính bằng phép trừ uint32 rồi xét có dấu ⇒ đúng qua wrap của monoSec(), và dấu "mới hơn"
// nowMono (net task vừa đóng dấu sau khi caller lấy giờ) được coi là còn sống. limit < 2^31.
bool netStale(uint32_t nowMono, uint32_t lastAlive, uint32_t limit);

// true nếu nên khởi động lại vì net task treo: dấu cũ hơn limit và bơm đang tắt. Bơm đang chạy ⇒
// chờ phiên kết thúc — tưới được ưu tiên hơn mạng.
bool netRestartDue(uint32_t nowMono, uint32_t lastAlive, uint32_t limit, bool pumpOn);
