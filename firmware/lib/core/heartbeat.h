#pragma once
#include <stddef.h>
#include <stdint.h>
#include "event.h"

struct HeartbeatInfo {
    const char* id;     // MAC 12 hex chữ thường
    const char* fw;     // "0.1.0"
    uint32_t uptimeS;
    int rssi;
    bool pumpOn;
    uint32_t rtcLocal;  // local epoch (UTC + 25200)
};

constexpr uint16_t kMaxEventsPerHeartbeat = 20;
// Buffer đủ cho trường hợp xấu nhất (20 event failsafe+capped, uptime 10 chữ số): ~2270 byte.
constexpr size_t kHeartbeatBufSize = 2560;

// ISO 8601 "YYYY-MM-DDTHH:MM:SS+07:00" từ local epoch; buf ≥ 26 byte
void formatIsoLocal(uint32_t local, char* buf);

// JSON heartbeat (không khoảng trắng, đúng thứ tự khoá của spec) vào buf.
// Trả số byte (không tính '\0'), 0 nếu không đủ chỗ.
size_t buildHeartbeat(char* buf, size_t cap, const HeartbeatInfo& h, const Event* ev, uint16_t n);
