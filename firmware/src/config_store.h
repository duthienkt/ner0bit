#pragma once
#include <stddef.h>
#include <stdint.h>

// Cấu hình mạng lưu trong NVS (Preferences namespace "ner0bit"). Mỗi trường kết thúc '\0'.
struct NetConfig {
    char ssid[33];
    char pass[65];
    char url[200];
    char token[65];
};

namespace config {
// true nếu đã có cấu hình (ssid và url khác rỗng). cfg luôn được xoá về 0 trước khi đọc.
bool load(NetConfig& cfg);
void save(const NetConfig& cfg);
bool hasConfig();

// Lịch cache của lần fetch thành công gần nhất. false nếu chưa có hoặc không vừa cap.
bool loadSchedule(char* body, size_t cap, uint32_t& fetchDay);
// Chỉ ghi khóa nào thay đổi (body so với bản đã lưu; fetchDay so với bản đã lưu).
void saveScheduleIfChanged(const char* body, uint32_t fetchDay);
}  // namespace config
