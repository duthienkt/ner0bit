#pragma once
#include <stdint.h>
#include <string.h>

#include "storage.h"

// IStorage trong RAM (4 KB, cùng layout với AT24C32) cho chế độ không có / mất AT24C32: queue
// events và lastStop vẫn chạy cùng code EventQueue nhưng mất khi reset. Khởi tạo toàn 0 ⇒
// EventQueue::begin() thấy magic sai và format.
class RamStorage : public IStorage {
public:
    static constexpr uint16_t kSize = 4096;

    bool read(uint16_t addr, uint8_t* buf, uint16_t len) override {
        if ((uint32_t)addr + len > kSize) return false;
        memcpy(buf, mem_ + addr, len);
        return true;
    }
    bool write(uint16_t addr, const uint8_t* buf, uint16_t len) override {
        if ((uint32_t)addr + len > kSize) return false;
        memcpy(mem_ + addr, buf, len);
        return true;
    }

private:
    uint8_t mem_[kSize] = {};
};
