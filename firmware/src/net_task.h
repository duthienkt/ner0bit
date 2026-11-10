#pragma once

#include "config_store.h"

// Tạo FreeRTOS task mạng (stack 12 KB): WiFi STA (không bao giờ mở AP), heartbeat HTTPS mỗi
// 300 s (header Date của response 200 là neo giờ: DS3231 lệch > 5 s ⇒ chỉnh theo Date), NTP mỗi
// 6 giờ chỉ tinh chỉnh DS3231 trong ±5 s quanh neo. cfg được chép lại; gọi một lần, sau
// sharedInit().
void startNetTask(const NetConfig& cfg);
