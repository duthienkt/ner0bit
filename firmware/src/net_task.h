#pragma once

#include "config_store.h"

// Tạo FreeRTOS task mạng (stack 12 KB): WiFi STA (không bao giờ mở AP), NTP chỉnh DS3231
// mỗi 6 giờ, heartbeat HTTPS mỗi 300 s. cfg được chép lại; gọi một lần, sau sharedInit().
void startNetTask(const NetConfig& cfg);
