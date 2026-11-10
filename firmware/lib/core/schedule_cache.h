#pragma once
#include <stddef.h>
#include <stdint.h>

#include "storage.h"

// Lịch cache (body 200 của lần fetch thành công gần nhất) trên IStorage, vùng sau queue events:
//   0x0800 {u16 magic=0x5343, u16 len, u32 fetchDay, u32 crc, body[len]}  (little-endian)
// len ∈ [1, kScheduleCacheMaxLen]; body không chứa '\0'. crc = CRC-32 (IEEE, như zlib) tính trên
// 6 byte {len, fetchDay} rồi body — header và body lệch nhau (mất điện giữa lúc ghi) ⇒ CRC sai ⇒
// coi như không có cache. Logic thuần, không Arduino.

constexpr uint16_t kScheduleCacheAddr = 0x0800;
constexpr uint16_t kScheduleCacheMagic = 0x5343;
constexpr uint16_t kScheduleCacheHeaderLen = 12;
constexpr uint16_t kScheduleCacheMaxLen = 1023;
static_assert(kScheduleCacheAddr + kScheduleCacheHeaderLen + kScheduleCacheMaxLen <= 0x1000,
              "lịch cache phải nằm trong AT24C32 4 KB");

// CRC-32 IEEE (đa thức đảo 0xEDB88320). crc32Update(0, ...) bắt đầu; nối tiếp được nhiều đoạn.
uint32_t crc32Update(uint32_t crc, const uint8_t* data, size_t len);

// Đọc cache vào body (kết thúc '\0'). false nếu đọc lỗi, magic/len/CRC sai, body chứa '\0'
// hoặc không vừa cap (cần cap ≥ len + 1). body[0] = '\0' khi false.
bool scheduleCacheLoad(IStorage& s, char* body, size_t cap, uint32_t& fetchDay);

enum class CacheWrite : uint8_t {
    Unchanged,  // nội dung đã lưu giống hệt (body và fetchDay) ⇒ không ghi gì
    Written,    // đã ghi (body nếu đổi, rồi header)
    Invalid,    // body rỗng hoặc dài hơn kScheduleCacheMaxLen ⇒ không ghi
    Failed,     // storage.write báo lỗi
};

// Chỉ ghi khi body hoặc fetchDay khác bản đã lưu (so từng byte body, không chỉ so CRC). Chỉ đổi
// fetchDay ⇒ chỉ ghi lại header 12 byte. Ghi body trước, header sau.
CacheWrite scheduleCacheSave(IStorage& s, const char* body, uint32_t fetchDay);
