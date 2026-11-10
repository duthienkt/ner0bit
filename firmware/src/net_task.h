#pragma once

#include "config_store.h"

// Tạo FreeRTOS task mạng (stack 12 KB): WiFi STA (không bao giờ mở AP), heartbeat HTTPS mỗi
// 300 s và ~3 s sau khi scheduler có event mới (header Date của response 200 là neo giờ: đồng
// hồ hệ thống lệch > 5 s ⇒ chỉnh theo Date), NTP mỗi 6 giờ chỉ tinh chỉnh trong ±5 s quanh neo.
// Mỗi lần chỉnh đồng hồ hệ thống cũng ghi DS3231 (nếu có). cfg được chép lại; gọi một lần, sau
// sharedInit().
void startNetTask(const NetConfig& cfg);

// Báo net task có event mới trong queue ⇒ heartbeat sau ~3 s (nhiều event trong khoảng đó gộp một
// lần gửi). Chưa có task (chế độ cấu hình) ⇒ không làm gì. Không gọi từ ISR.
void notifyNetTask();
