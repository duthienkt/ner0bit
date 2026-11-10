#pragma once
#include <stdint.h>
#include "event.h"
#include "storage.h"

constexpr uint16_t kQueueCapacity = 200;

// Ring buffer events trên IStorage (layout AT24C32):
//   0x0000 header {u16 magic=0x4E42, u16 head, u16 count}
//   0x0008 u32 lastStop
//   0x0010 records, mỗi record 10 byte {u32 start, u32 stop, u8 source, u8 flags}
// Tất cả little-endian. Ghi record trước, header sau.
class EventQueue {
   public:
    explicit EventQueue(IStorage& s);
    void begin();                                   // đọc header; hỏng ⇒ format (rỗng, lastStop=0)
    void push(const Event& e);                      // đầy ⇒ bỏ cũ nhất
    uint16_t peek(Event* out, uint16_t max) const;  // cũ nhất trước
    void pop(uint16_t n);                           // n > count ⇒ pop hết
    uint16_t count() const;
    uint32_t lastStop() const;
    void setLastStop(uint32_t v);                   // ghi xuống storage

   private:
    void writeHeader();
    void format();

    IStorage& storage_;
    uint16_t head_ = 0;   // index record cũ nhất
    uint16_t count_ = 0;
    uint32_t lastStop_ = 0;
};
