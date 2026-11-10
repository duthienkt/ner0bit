#pragma once
#include <stddef.h>
#include <stdint.h>
#include "event.h"

// Trạng thái module ngoài. JSON: "absent" | "ok" | "lost_power" | "failed".
// LostPower: chỉ DS3231 — cờ OSF bật lúc boot (thường do pin CR2032 hết).
// Failed: chip có mặt nhưng lỗi I/O lúc chạy.
enum class ModuleState : uint8_t { Absent = 0, Ok = 1, LostPower = 2, Failed = 3 };

// tempQuarterC = kTempUnknown ⇒ bỏ khoá "temp_c".
constexpr int16_t kTempUnknown = INT16_MIN;

struct HeartbeatInfo {
    const char* id;     // MAC 12 hex chữ thường
    const char* fw;     // vd. "0.2.0"
    uint32_t uptimeS;
    int rssi;
    bool pumpOn;
    uint32_t heapFree;  // esp_get_free_heap_size(), byte
    uint32_t heapMin;   // esp_get_minimum_free_heap_size(), byte
    ModuleState ds3231;
    ModuleState at24c32;
    int16_t tempQuarterC;  // nhiệt độ DS3231, đơn vị 0,25 °C
    uint16_t queueLen;     // số event đang chờ gửi
    uint32_t rtcLocal;     // local epoch (UTC + 25200)
};

constexpr uint16_t kMaxEventsPerHeartbeat = 20;
// Buffer đủ cho trường hợp xấu nhất (mọi số dài nhất, 20 event skipped+failsafe+capped, epoch
// max): 2058 byte + '\0' — test_heartbeat/test_max_events_fits_buf_size kiểm tra.
constexpr size_t kHeartbeatBufSize = 2304;

// ISO 8601 "YYYY-MM-DDTHH:MM:SS+07:00" từ local epoch; buf ≥ 26 byte
void formatIsoLocal(uint32_t local, char* buf);

// JSON heartbeat (không khoảng trắng, đúng thứ tự khoá của spec) vào buf.
// Trả số byte (không tính '\0'), 0 nếu không đủ chỗ.
size_t buildHeartbeat(char* buf, size_t cap, const HeartbeatInfo& h, const Event* ev, uint16_t n);
