#include <unity.h>
#include <string.h>
#include "event_queue.h"

// EEPROM giả 4 KB (cỡ AT24C32) trong RAM.
class RamStorage : public IStorage {
   public:
    uint8_t mem[4096];
    int failReads = 0;  // số lần read() kế tiếp trả lỗi (giả lỗi bus I2C)
    int writes = 0;     // số lần write() được gọi
    RamStorage() { memset(mem, 0, sizeof(mem)); }
    bool read(uint16_t addr, uint8_t* buf, uint16_t len) override {
        if (failReads > 0) {
            failReads--;
            return false;
        }
        if ((uint32_t)addr + len > sizeof(mem)) return false;
        memcpy(buf, mem + addr, len);
        return true;
    }
    bool write(uint16_t addr, const uint8_t* buf, uint16_t len) override {
        writes++;
        if ((uint32_t)addr + len > sizeof(mem)) return false;
        memcpy(mem + addr, buf, len);
        return true;
    }
};

void setUp() {}
void tearDown() {}

static Event ev(uint32_t at, EventType type = EventType::Stop, Source src = Source::Normal,
                Reason reason = Reason::Done) {
    Event e;
    e.at = at;
    e.type = type;
    e.source = src;
    e.reason = reason;
    return e;
}

static void expectEv(const Event& e, uint32_t at, EventType type, Source src, Reason reason) {
    TEST_ASSERT_EQUAL_UINT32(at, e.at);
    TEST_ASSERT_EQUAL_UINT8((uint8_t)type, (uint8_t)e.type);
    TEST_ASSERT_EQUAL_UINT8((uint8_t)src, (uint8_t)e.source);
    TEST_ASSERT_EQUAL_UINT8((uint8_t)reason, (uint8_t)e.reason);
}

void test_push_peek_pop_fifo() {
    RamStorage st;
    EventQueue q(st);
    q.begin();
    q.push(ev(100, EventType::Start, Source::Normal, Reason::None));
    q.push(ev(200, EventType::Skipped, Source::Today, Reason::Busy));
    q.push(ev(400, EventType::Stop, Source::Normal, Reason::Done));
    q.push(ev(500, EventType::Skipped, Source::Today, Reason::Rest));
    q.push(ev(2100, EventType::Stop, Source::Failsafe, Reason::Capped));
    q.push(ev(2200, EventType::Stop, Source::Today, Reason::Abort));
    TEST_ASSERT_EQUAL_UINT16(6, q.count());

    Event out[8];
    TEST_ASSERT_EQUAL_UINT16(6, q.peek(out, 8));
    expectEv(out[0], 100, EventType::Start, Source::Normal, Reason::None);
    expectEv(out[1], 200, EventType::Skipped, Source::Today, Reason::Busy);
    expectEv(out[2], 400, EventType::Stop, Source::Normal, Reason::Done);
    expectEv(out[3], 500, EventType::Skipped, Source::Today, Reason::Rest);
    expectEv(out[4], 2100, EventType::Stop, Source::Failsafe, Reason::Capped);
    expectEv(out[5], 2200, EventType::Stop, Source::Today, Reason::Abort);

    q.pop(5);
    TEST_ASSERT_EQUAL_UINT16(1, q.count());
    TEST_ASSERT_EQUAL_UINT16(1, q.peek(out, 8));
    expectEv(out[0], 2200, EventType::Stop, Source::Today, Reason::Abort);
}

void test_peek_respects_max() {
    RamStorage st;
    EventQueue q(st);
    q.begin();
    for (uint32_t i = 0; i < 5; i++) q.push(ev(i));
    Event out[2];
    TEST_ASSERT_EQUAL_UINT16(2, q.peek(out, 2));
    TEST_ASSERT_EQUAL_UINT32(0, out[0].at);
    TEST_ASSERT_EQUAL_UINT32(1, out[1].at);
}

void test_overflow_drops_oldest() {
    RamStorage st;
    EventQueue q(st);
    q.begin();
    for (uint32_t i = 0; i < 205; i++) q.push(ev(i));
    TEST_ASSERT_EQUAL_UINT16(kQueueCapacity, q.count());
    Event out[1];
    TEST_ASSERT_EQUAL_UINT16(1, q.peek(out, 1));
    TEST_ASSERT_EQUAL_UINT32(5, out[0].at);
}

void test_wraparound_persists() {
    RamStorage st;
    {
        EventQueue q(st);
        q.begin();
        for (uint32_t i = 0; i < 250; i++) q.push(ev(i, EventType::Stop, Source::Today, Reason::Capped));
    }
    EventQueue q2(st);
    q2.begin();
    TEST_ASSERT_EQUAL_UINT16(200, q2.count());
    static Event out[kQueueCapacity];
    TEST_ASSERT_EQUAL_UINT16(200, q2.peek(out, kQueueCapacity));
    for (uint16_t i = 0; i < 200; i++) expectEv(out[i], 50 + i, EventType::Stop, Source::Today, Reason::Capped);
}

void test_pop_persists() {
    RamStorage st;
    {
        EventQueue q(st);
        q.begin();
        for (uint32_t i = 0; i < 10; i++) q.push(ev(i));
        q.pop(4);
    }
    EventQueue q2(st);
    q2.begin();
    TEST_ASSERT_EQUAL_UINT16(6, q2.count());
    Event out[1];
    q2.peek(out, 1);
    TEST_ASSERT_EQUAL_UINT32(4, out[0].at);
}

void test_bad_magic_resets() {
    RamStorage st;
    memset(st.mem, 0xFF, sizeof(st.mem));
    EventQueue q(st);
    q.begin();
    TEST_ASSERT_TRUE(q.available());
    TEST_ASSERT_EQUAL_UINT16(0, q.count());
    TEST_ASSERT_EQUAL_UINT32(0, q.lastStop());
    Event out[1];
    TEST_ASSERT_EQUAL_UINT16(0, q.peek(out, 1));
    // Đã format: mở lại vẫn rỗng, không đọc rác.
    EventQueue q2(st);
    q2.begin();
    TEST_ASSERT_EQUAL_UINT16(0, q2.count());
    TEST_ASSERT_EQUAL_UINT32(0, q2.lastStop());
}

void test_corrupt_header_resets() {
    // Magic đúng nhưng head/count vượt dung lượng ⇒ coi như hỏng.
    RamStorage st;
    {
        EventQueue q(st);
        q.begin();
        q.push(ev(1));
    }
    st.mem[4] = 0xFF;  // count low byte
    st.mem[5] = 0xFF;  // count high byte
    EventQueue q2(st);
    q2.begin();
    TEST_ASSERT_EQUAL_UINT16(0, q2.count());
}

void test_last_stop_persists() {
    RamStorage st;
    {
        EventQueue q(st);
        q.begin();
        q.setLastStop(12345);
        TEST_ASSERT_EQUAL_UINT32(12345, q.lastStop());
    }
    EventQueue q2(st);
    q2.begin();
    TEST_ASSERT_EQUAL_UINT32(12345, q2.lastStop());
}

void test_pop_more_than_count() {
    RamStorage st;
    EventQueue q(st);
    q.begin();
    q.push(ev(1));
    q.push(ev(2));
    q.pop(5);
    TEST_ASSERT_EQUAL_UINT16(0, q.count());
}

void test_layout_little_endian() {
    RamStorage st;
    memset(st.mem, 0xFF, sizeof(st.mem));  // byte dự trữ phải được ghi 0, không để rác
    EventQueue q(st);
    q.begin();
    q.setLastStop(0x11223344);
    q.push(ev(0xA1A2A3A4, EventType::Skipped, Source::Failsafe, Reason::Busy));
    q.push(ev(0xB1B2B3B4, EventType::Start, Source::Today, Reason::None));
    // header
    TEST_ASSERT_EQUAL_HEX8(0x43, st.mem[0]);
    TEST_ASSERT_EQUAL_HEX8(0x4E, st.mem[1]);
    TEST_ASSERT_EQUAL_HEX8(0, st.mem[2]);  // head
    TEST_ASSERT_EQUAL_HEX8(0, st.mem[3]);
    TEST_ASSERT_EQUAL_HEX8(2, st.mem[4]);  // count
    TEST_ASSERT_EQUAL_HEX8(0, st.mem[5]);
    // lastStop @0x0008
    TEST_ASSERT_EQUAL_HEX8(0x44, st.mem[8]);
    TEST_ASSERT_EQUAL_HEX8(0x11, st.mem[11]);
    // record 0 @0x0010, record 1 @0x001A: {u32 at, u8 type, u8 source, u8 reason, 3 byte 0}
    const uint8_t expect[20] = {0xA4, 0xA3, 0xA2, 0xA1, 2, 2, 5, 0, 0, 0,
                                0xB4, 0xB3, 0xB2, 0xB1, 0, 1, 0, 0, 0, 0};
    TEST_ASSERT_EQUAL_HEX8_ARRAY(expect, st.mem + 0x10, 20);
}

// Layout record đổi ⇒ magic mới; nội dung magic cũ 0x4E42 bị format một lần.
void test_old_magic_formatted() {
    RamStorage st;
    const uint8_t oldHeader[12] = {0x42, 0x4E, 0, 0, 3, 0, 0, 0, 0x39, 0x30, 0, 0};
    memcpy(st.mem, oldHeader, sizeof(oldHeader));
    EventQueue q(st);
    q.begin();
    TEST_ASSERT_TRUE(q.available());
    TEST_ASSERT_EQUAL_UINT16(0, q.count());
    TEST_ASSERT_EQUAL_UINT32(0, q.lastStop());
    TEST_ASSERT_EQUAL_HEX8(0x43, st.mem[0]);
    TEST_ASSERT_EQUAL_HEX8(0x4E, st.mem[1]);
    TEST_ASSERT_EQUAL_HEX8(0, st.mem[4]);
}

// M1: bộ đếm record bị ghi đè khi queue đầy.
void test_dropped_counts_overwrites() {
    RamStorage st;
    EventQueue q(st);
    q.begin();
    for (uint32_t i = 0; i < kQueueCapacity; i++) q.push(ev(i));
    TEST_ASSERT_EQUAL_UINT32(0, q.dropped());
    for (uint32_t i = 0; i < 5; i++) q.push(ev(1000 + i));
    TEST_ASSERT_EQUAL_UINT32(5, q.dropped());
    q.pop(10);
    TEST_ASSERT_EQUAL_UINT32(5, q.dropped());
    q.push(ev(2000));  // còn chỗ ⇒ không ghi đè
    TEST_ASSERT_EQUAL_UINT32(5, q.dropped());
}

// M1: peek → (task khác push làm tràn) → 200 OK ⇒ chỉ pop phần đã gửi còn trong queue.
void test_pop_sent_after_concurrent_overflow() {
    RamStorage st;
    EventQueue q(st);
    q.begin();
    for (uint32_t i = 0; i < kQueueCapacity; i++) q.push(ev(i));
    static Event out[50];
    const uint16_t n = q.peek(out, 50);  // gửi 0..49
    TEST_ASSERT_EQUAL_UINT16(50, n);
    const uint32_t droppedAtPeek = q.dropped();
    for (uint32_t i = 0; i < 3; i++) q.push(ev(kQueueCapacity + i));  // 0,1,2 bị ghi đè
    q.popSent(n, droppedAtPeek);
    TEST_ASSERT_EQUAL_UINT16(kQueueCapacity - 47, q.count());
    Event first[1];
    TEST_ASSERT_EQUAL_UINT16(1, q.peek(first, 1));
    TEST_ASSERT_EQUAL_UINT32(50, first[0].at);  // event chưa gửi đầu tiên vẫn còn
}

void test_pop_sent_when_drops_exceed_sent() {
    RamStorage st;
    EventQueue q(st);
    q.begin();
    for (uint32_t i = 0; i < kQueueCapacity; i++) q.push(ev(i));
    Event out[2];
    const uint16_t n = q.peek(out, 2);
    const uint32_t droppedAtPeek = q.dropped();
    for (uint32_t i = 0; i < 5; i++) q.push(ev(kQueueCapacity + i));
    q.popSent(n, droppedAtPeek);
    TEST_ASSERT_EQUAL_UINT16(kQueueCapacity, q.count());
    Event first[1];
    q.peek(first, 1);
    TEST_ASSERT_EQUAL_UINT32(5, first[0].at);
}

void test_pop_sent_without_overflow() {
    RamStorage st;
    EventQueue q(st);
    q.begin();
    for (uint32_t i = 0; i < 10; i++) q.push(ev(i));
    Event out[4];
    const uint16_t n = q.peek(out, 4);
    const uint32_t droppedAtPeek = q.dropped();
    q.push(ev(100));
    q.popSent(n, droppedAtPeek);
    TEST_ASSERT_EQUAL_UINT16(7, q.count());
    Event first[1];
    q.peek(first, 1);
    TEST_ASSERT_EQUAL_UINT32(4, first[0].at);
}

// M4: lỗi đọc thoáng qua lúc boot ⇒ đọc lại, không format.
void test_transient_header_read_failure_retries() {
    RamStorage st;
    {
        EventQueue q(st);
        q.begin();
        for (uint32_t i = 0; i < 3; i++) q.push(ev(i));
        q.setLastStop(999);
    }
    st.failReads = 2;
    EventQueue q2(st);
    q2.begin();
    TEST_ASSERT_TRUE(q2.available());
    TEST_ASSERT_EQUAL_UINT16(3, q2.count());
    TEST_ASSERT_EQUAL_UINT32(999, q2.lastStop());
}

// M4: đọc lỗi mãi ⇒ queue "unavailable", không ghi gì xuống storage, dữ liệu cũ còn nguyên.
void test_persistent_header_read_failure_leaves_storage_untouched() {
    RamStorage st;
    {
        EventQueue q(st);
        q.begin();
        for (uint32_t i = 0; i < 3; i++) q.push(ev(i));
        q.setLastStop(999);
    }
    st.failReads = 1000;
    st.writes = 0;
    EventQueue q2(st);
    q2.begin();
    TEST_ASSERT_FALSE(q2.available());
    TEST_ASSERT_EQUAL_UINT16(0, q2.count());
    TEST_ASSERT_EQUAL_UINT32(0, q2.lastStop());
    q2.push(ev(50));
    q2.pop(1);
    q2.popSent(1, 0);
    q2.setLastStop(777);
    TEST_ASSERT_EQUAL_INT(0, st.writes);
    TEST_ASSERT_EQUAL_UINT16(0, q2.count());
    TEST_ASSERT_EQUAL_UINT32(0, q2.dropped());
    Event out[1];
    TEST_ASSERT_EQUAL_UINT16(0, q2.peek(out, 1));

    st.failReads = 0;
    EventQueue q3(st);
    q3.begin();
    TEST_ASSERT_TRUE(q3.available());
    TEST_ASSERT_EQUAL_UINT16(3, q3.count());
    TEST_ASSERT_EQUAL_UINT32(999, q3.lastStop());
}

int main() {
    UNITY_BEGIN();
    RUN_TEST(test_push_peek_pop_fifo);
    RUN_TEST(test_peek_respects_max);
    RUN_TEST(test_overflow_drops_oldest);
    RUN_TEST(test_wraparound_persists);
    RUN_TEST(test_pop_persists);
    RUN_TEST(test_bad_magic_resets);
    RUN_TEST(test_corrupt_header_resets);
    RUN_TEST(test_last_stop_persists);
    RUN_TEST(test_pop_more_than_count);
    RUN_TEST(test_layout_little_endian);
    RUN_TEST(test_old_magic_formatted);
    RUN_TEST(test_dropped_counts_overwrites);
    RUN_TEST(test_pop_sent_after_concurrent_overflow);
    RUN_TEST(test_pop_sent_when_drops_exceed_sent);
    RUN_TEST(test_pop_sent_without_overflow);
    RUN_TEST(test_transient_header_read_failure_retries);
    RUN_TEST(test_persistent_header_read_failure_leaves_storage_untouched);
    return UNITY_END();
}
