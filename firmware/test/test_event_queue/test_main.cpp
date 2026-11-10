#include <unity.h>
#include <string.h>
#include "event_queue.h"

// EEPROM giả 4 KB (cỡ AT24C32) trong RAM.
class RamStorage : public IStorage {
   public:
    uint8_t mem[4096];
    RamStorage() { memset(mem, 0, sizeof(mem)); }
    bool read(uint16_t addr, uint8_t* buf, uint16_t len) override {
        if ((uint32_t)addr + len > sizeof(mem)) return false;
        memcpy(buf, mem + addr, len);
        return true;
    }
    bool write(uint16_t addr, const uint8_t* buf, uint16_t len) override {
        if ((uint32_t)addr + len > sizeof(mem)) return false;
        memcpy(mem + addr, buf, len);
        return true;
    }
};

void setUp() {}
void tearDown() {}

static Event ev(uint32_t start, uint32_t stop = 0, Source src = Source::Normal, uint8_t flags = 0) {
    Event e;
    e.start = start;
    e.stop = stop;
    e.source = src;
    e.flags = flags;
    return e;
}

void test_push_peek_pop_fifo() {
    RamStorage st;
    EventQueue q(st);
    q.begin();
    q.push(ev(100, 400, Source::Normal, 0));
    q.push(ev(200, 0, Source::Today, kSkippedRest));
    q.push(ev(300, 2100, Source::Failsafe, kCapped));
    TEST_ASSERT_EQUAL_UINT16(3, q.count());

    Event out[5];
    TEST_ASSERT_EQUAL_UINT16(3, q.peek(out, 5));
    TEST_ASSERT_EQUAL_UINT32(100, out[0].start);
    TEST_ASSERT_EQUAL_UINT32(400, out[0].stop);
    TEST_ASSERT_TRUE(Source::Normal == out[0].source);
    TEST_ASSERT_EQUAL_UINT8(0, out[0].flags);
    TEST_ASSERT_EQUAL_UINT32(200, out[1].start);
    TEST_ASSERT_EQUAL_UINT32(0, out[1].stop);
    TEST_ASSERT_TRUE(Source::Today == out[1].source);
    TEST_ASSERT_EQUAL_UINT8(kSkippedRest, out[1].flags);
    TEST_ASSERT_EQUAL_UINT32(300, out[2].start);
    TEST_ASSERT_EQUAL_UINT32(2100, out[2].stop);
    TEST_ASSERT_TRUE(Source::Failsafe == out[2].source);
    TEST_ASSERT_EQUAL_UINT8(kCapped, out[2].flags);

    q.pop(2);
    TEST_ASSERT_EQUAL_UINT16(1, q.count());
    TEST_ASSERT_EQUAL_UINT16(1, q.peek(out, 5));
    TEST_ASSERT_EQUAL_UINT32(300, out[0].start);
}

void test_peek_respects_max() {
    RamStorage st;
    EventQueue q(st);
    q.begin();
    for (uint32_t i = 0; i < 5; i++) q.push(ev(i));
    Event out[2];
    TEST_ASSERT_EQUAL_UINT16(2, q.peek(out, 2));
    TEST_ASSERT_EQUAL_UINT32(0, out[0].start);
    TEST_ASSERT_EQUAL_UINT32(1, out[1].start);
}

void test_overflow_drops_oldest() {
    RamStorage st;
    EventQueue q(st);
    q.begin();
    for (uint32_t i = 0; i < 205; i++) q.push(ev(i));
    TEST_ASSERT_EQUAL_UINT16(kQueueCapacity, q.count());
    Event out[1];
    TEST_ASSERT_EQUAL_UINT16(1, q.peek(out, 1));
    TEST_ASSERT_EQUAL_UINT32(5, out[0].start);
}

void test_wraparound_persists() {
    RamStorage st;
    {
        EventQueue q(st);
        q.begin();
        for (uint32_t i = 0; i < 250; i++) q.push(ev(i, i + 1000, Source::Today, kCapped));
    }
    EventQueue q2(st);
    q2.begin();
    TEST_ASSERT_EQUAL_UINT16(200, q2.count());
    static Event out[kQueueCapacity];
    TEST_ASSERT_EQUAL_UINT16(200, q2.peek(out, kQueueCapacity));
    for (uint16_t i = 0; i < 200; i++) {
        TEST_ASSERT_EQUAL_UINT32(50 + i, out[i].start);
        TEST_ASSERT_EQUAL_UINT32(1050 + i, out[i].stop);
    }
    TEST_ASSERT_EQUAL_UINT32(50, out[0].start);
    TEST_ASSERT_EQUAL_UINT32(249, out[199].start);
    TEST_ASSERT_TRUE(Source::Today == out[199].source);
    TEST_ASSERT_EQUAL_UINT8(kCapped, out[199].flags);
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
    TEST_ASSERT_EQUAL_UINT32(4, out[0].start);
}

void test_bad_magic_resets() {
    RamStorage st;
    memset(st.mem, 0xFF, sizeof(st.mem));
    EventQueue q(st);
    q.begin();
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
    EventQueue q(st);
    q.begin();
    q.setLastStop(0x11223344);
    q.push(ev(0xA1A2A3A4, 0xB1B2B3B4, Source::Failsafe, kCapped));
    // header
    TEST_ASSERT_EQUAL_HEX8(0x42, st.mem[0]);
    TEST_ASSERT_EQUAL_HEX8(0x4E, st.mem[1]);
    TEST_ASSERT_EQUAL_HEX8(0, st.mem[2]);  // head
    TEST_ASSERT_EQUAL_HEX8(0, st.mem[3]);
    TEST_ASSERT_EQUAL_HEX8(1, st.mem[4]);  // count
    TEST_ASSERT_EQUAL_HEX8(0, st.mem[5]);
    // lastStop @0x0008
    TEST_ASSERT_EQUAL_HEX8(0x44, st.mem[8]);
    TEST_ASSERT_EQUAL_HEX8(0x11, st.mem[11]);
    // record 0 @0x0010
    const uint8_t expect[10] = {0xA4, 0xA3, 0xA2, 0xA1, 0xB4, 0xB3, 0xB2, 0xB1, 2, kCapped};
    TEST_ASSERT_EQUAL_HEX8_ARRAY(expect, st.mem + 0x10, 10);
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
    return UNITY_END();
}
