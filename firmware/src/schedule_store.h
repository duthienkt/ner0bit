#pragma once
#include <stddef.h>
#include <stdint.h>

// Lịch cache (body 200 gần nhất + ngày nhận) theo chế độ storage hiện tại:
// - AT24C32: vùng 0x0800 có CRC (lib/core/schedule_cache.h); NVS không ghi lịch — lần lưu
//   AT24C32 thành công đầu tiên mỗi lần boot xoá khoá sched/fday cũ trong NVS (nếu có).
// - RAM (không có / mất AT24C32): NVS (config_store), fday chỉ ghi khi lịch có dòng 2.
// Tự lấy g_lock khi cần bus I2C; không gọi khi đang giữ g_lock.
namespace schedule_store {

// Lúc boot, sau storageBoot(). false nếu chưa có cache hoặc cache hỏng (magic/len/CRC sai).
bool load(char* body, size_t cap, uint32_t& fetchDay);

// Chỉ ghi khi body hoặc fetchDay đổi. Ghi AT24C32 lỗi ⇒ chuyển sang RAM (storageFailover) và
// ghi NVS ngay. hasLine2: lịch có dòng 2 (Empty hoặc Present).
void saveIfChanged(const char* body, uint32_t fetchDay, bool hasLine2);

}  // namespace schedule_store
