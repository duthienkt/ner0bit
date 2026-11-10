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
    void begin();  // đọc header (thử lại khi lỗi đọc); đọc được mà hỏng ⇒ format (rỗng, lastStop=0)
    void push(const Event& e);                      // đầy ⇒ bỏ cũ nhất
    uint16_t peek(Event* out, uint16_t max) const;  // cũ nhất trước
    void pop(uint16_t n);                           // n > count ⇒ pop hết
    uint16_t count() const;
    uint32_t lastStop() const;
    void setLastStop(uint32_t v);                   // ghi xuống storage
    // false khi begin() không đọc được header sau kHeaderReadAttempts lần: queue rỗng,
    // lastStop = 0, push/pop/setLastStop không làm gì và không ghi gì xuống storage
    // (tránh format mất events chỉ vì lỗi bus thoáng qua).
    bool available() const { return available_; }
    // Số record cũ nhất đã bị push ghi đè khi đầy, tăng dần (RAM, từ lúc boot; wrap mod 2^32).
    uint32_t dropped() const { return dropped_; }
    // Pop `sent` events đã peek lúc dropped() == droppedAtPeek. Record bị ghi đè sau peek nằm ở
    // đầu phần đã gửi nên chỉ pop sent − min(sent, số record bị ghi đè từ lúc peek) — không pop
    // nhầm event chưa gửi.
    void popSent(uint16_t sent, uint32_t droppedAtPeek);

   private:
    void writeHeader();
    void format();

    IStorage& storage_;
    uint16_t head_ = 0;   // index record cũ nhất
    uint16_t count_ = 0;
    uint32_t lastStop_ = 0;
    uint32_t dropped_ = 0;
    bool available_ = false;
};
