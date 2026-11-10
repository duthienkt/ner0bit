#include "event_queue.h"

namespace {

constexpr uint16_t kMagic = 0x4E43;
constexpr uint16_t kHeaderAddr = 0x0000;
constexpr uint16_t kHeaderLen = 6;
constexpr uint16_t kLastStopAddr = 0x0008;
constexpr uint16_t kRecordsAddr = 0x0010;
constexpr uint16_t kRecordLen = 10;
constexpr int kHeaderReadAttempts = 3;

void put16(uint8_t* p, uint16_t v) {
    p[0] = (uint8_t)(v & 0xFF);
    p[1] = (uint8_t)(v >> 8);
}

void put32(uint8_t* p, uint32_t v) {
    p[0] = (uint8_t)(v & 0xFF);
    p[1] = (uint8_t)((v >> 8) & 0xFF);
    p[2] = (uint8_t)((v >> 16) & 0xFF);
    p[3] = (uint8_t)(v >> 24);
}

uint16_t get16(const uint8_t* p) { return (uint16_t)(p[0] | ((uint16_t)p[1] << 8)); }

uint32_t get32(const uint8_t* p) {
    return (uint32_t)p[0] | ((uint32_t)p[1] << 8) | ((uint32_t)p[2] << 16) | ((uint32_t)p[3] << 24);
}

uint16_t recordAddr(uint16_t index) { return (uint16_t)(kRecordsAddr + index * kRecordLen); }

}  // namespace

EventQueue::EventQueue(IStorage& s) : storage_(s) {}

void EventQueue::begin() {
    // Header (0x00..0x05) + 2 byte đệm + lastStop (0x08..0x0B): đọc một lần.
    uint8_t buf[12];
    head_ = 0;
    count_ = 0;
    lastStop_ = 0;
    available_ = false;
    bool readOk = false;
    for (int i = 0; i < kHeaderReadAttempts && !readOk; i++) {
        readOk = storage_.read(kHeaderAddr, buf, sizeof(buf));
    }
    // Đọc lỗi mãi ⇒ không biết storage chứa gì: không format, để queue "unavailable".
    if (!readOk) return;
    available_ = true;
    uint16_t magic = get16(buf);
    uint16_t head = get16(buf + 2);
    uint16_t count = get16(buf + 4);
    if (magic != kMagic || head >= kQueueCapacity || count > kQueueCapacity) {
        format();
        return;
    }
    head_ = head;
    count_ = count;
    lastStop_ = get32(buf + (kLastStopAddr - kHeaderAddr));
}

void EventQueue::format() {
    head_ = 0;
    count_ = 0;
    lastStop_ = 0;
    uint8_t ls[4];
    put32(ls, 0);
    storage_.write(kLastStopAddr, ls, sizeof(ls));
    writeHeader();
}

void EventQueue::writeHeader() {
    uint8_t buf[kHeaderLen];
    put16(buf, kMagic);
    put16(buf + 2, head_);
    put16(buf + 4, count_);
    storage_.write(kHeaderAddr, buf, sizeof(buf));
}

void EventQueue::push(const Event& e) {
    if (!available_) return;
    uint16_t slot = (uint16_t)((head_ + count_) % kQueueCapacity);
    uint8_t rec[kRecordLen] = {};
    put32(rec, e.at);
    rec[4] = (uint8_t)e.type;
    rec[5] = (uint8_t)e.source;
    rec[6] = (uint8_t)e.reason;
    storage_.write(recordAddr(slot), rec, sizeof(rec));
    if (count_ < kQueueCapacity) {
        count_++;
    } else {
        // Đầy: slot vừa ghi chính là record cũ nhất ⇒ dời head.
        head_ = (uint16_t)((head_ + 1) % kQueueCapacity);
        dropped_++;
    }
    writeHeader();
}

uint16_t EventQueue::peek(Event* out, uint16_t max) const {
    uint16_t n = max < count_ ? max : count_;
    for (uint16_t i = 0; i < n; i++) {
        uint8_t rec[kRecordLen];
        uint16_t slot = (uint16_t)((head_ + i) % kQueueCapacity);
        if (!storage_.read(recordAddr(slot), rec, sizeof(rec))) return i;
        out[i].at = get32(rec);
        out[i].type = (EventType)rec[4];
        out[i].source = (Source)rec[5];
        out[i].reason = (Reason)rec[6];
    }
    return n;
}

void EventQueue::pop(uint16_t n) {
    if (!available_ || n == 0) return;
    if (n >= count_) {
        head_ = 0;
        count_ = 0;
    } else {
        head_ = (uint16_t)((head_ + n) % kQueueCapacity);
        count_ = (uint16_t)(count_ - n);
    }
    writeHeader();
}

void EventQueue::popSent(uint16_t sent, uint32_t droppedAtPeek) {
    const uint32_t lost = dropped_ - droppedAtPeek;
    pop(lost >= sent ? 0 : (uint16_t)(sent - lost));
}

uint16_t EventQueue::count() const { return count_; }

uint32_t EventQueue::lastStop() const { return lastStop_; }

void EventQueue::setLastStop(uint32_t v) {
    if (!available_) return;
    lastStop_ = v;
    uint8_t buf[4];
    put32(buf, v);
    storage_.write(kLastStopAddr, buf, sizeof(buf));
}
