#include "at24c32.h"

#include <Arduino.h>
#include <Wire.h>

static constexpr uint8_t kAddr = 0x57;
static constexpr uint16_t kReadChunk = 30;
static constexpr uint32_t kWriteCycleMs = 5;
static constexpr uint32_t kPollTimeoutMs = 20;  // bản AT24C32 cũ có tWR tới 10–20 ms

static bool inRange(uint16_t addr, uint16_t len) {
    return (uint32_t)addr + len <= At24c32::kSize;
}

// Sau chu kỳ ghi nội bộ chip không ACK; chờ 5 ms rồi thăm dò tới khi ACK.
static bool waitReady() {
    delay(kWriteCycleMs);
    const uint32_t t0 = millis();
    for (;;) {
        Wire.beginTransmission(kAddr);
        if (Wire.endTransmission() == 0) return true;
        if (millis() - t0 >= kPollTimeoutMs) return false;
        delay(1);
    }
}

bool At24c32::read(uint16_t addr, uint8_t* buf, uint16_t len) {
    if (!inRange(addr, len)) return false;
    while (len > 0) {
        const uint16_t n = len < kReadChunk ? len : kReadChunk;
        Wire.beginTransmission(kAddr);
        Wire.write((uint8_t)(addr >> 8));
        Wire.write((uint8_t)(addr & 0xFF));
        if (Wire.endTransmission(false) != 0) return false;
        if (Wire.requestFrom(kAddr, (size_t)n) != n) return false;
        for (uint16_t i = 0; i < n; i++) buf[i] = (uint8_t)Wire.read();
        addr += n;
        buf += n;
        len -= n;
    }
    return true;
}

bool At24c32::write(uint16_t addr, const uint8_t* buf, uint16_t len) {
    if (!inRange(addr, len)) return false;
    while (len > 0) {
        const uint16_t room = kPageSize - (addr % kPageSize);  // byte còn lại tới hết page
        const uint16_t n = len < room ? len : room;
        Wire.beginTransmission(kAddr);
        Wire.write((uint8_t)(addr >> 8));
        Wire.write((uint8_t)(addr & 0xFF));
        Wire.write(buf, n);
        if (Wire.endTransmission() != 0) return false;
        if (!waitReady()) return false;
        addr += n;
        buf += n;
        len -= n;
    }
    return true;
}
