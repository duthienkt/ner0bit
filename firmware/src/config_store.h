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

// Lịch cache trong NVS — chỉ dùng khi không có AT24C32 (xem schedule_store.h).
// false nếu chưa có hoặc không vừa cap. Chưa có khoá fday ⇒ fetchDay = 0 (dòng 2 coi như cũ).
bool loadSchedule(char* body, size_t cap, uint32_t& fetchDay);
// Chỉ ghi khoá nào thay đổi (body so với bản đã lưu; fetchDay so với bản đã lưu). fday chỉ ghi
// khi hasLine2 (lịch có dòng 2, kể cả dòng rỗng): fetchDay chỉ dùng để xét dòng 2 còn hạn, nên
// lịch một dòng không ghi flash mỗi ngày.
void saveScheduleIfChanged(const char* body, uint32_t fetchDay, bool hasLine2);
// Xoá khoá sched và fday (chỉ khoá nào đang có — không có thì không ghi flash).
void clearSchedule();
}  // namespace config
