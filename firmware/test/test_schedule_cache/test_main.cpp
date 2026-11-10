#include <unity.h>
#include <string.h>
#include "event_queue.h"
#include "schedule_cache.h"

// EEPROM giả 4 KB (cỡ AT24C32) trong RAM, ghi lại số lần/byte ghi và giả lỗi.
class RamStorage : public IStorage {
   public:
    uint8_t mem[4096];
    int failReads = 0;    // số lần read() kế tiếp trả lỗi
    int failWrites = 0;   // số lần write() kế tiếp trả lỗi (không ghi gì)
    int writes = 0;       // số lần write() thành công
    int bytesWritten = 0;
    uint16_t lastWriteAddr = 0;
    RamStorage() { memset(mem, 0xFF, sizeof(mem)); }  // EEPROM mới: toàn 0xFF
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
        if (failWrites > 0) {
            failWrites--;
            return false;
        }
        if ((uint32_t)addr + len > sizeof(mem)) return false;
        memcpy(mem + addr, buf, len);
        writes++;
        bytesWritten += len;
        lastWriteAddr = addr;
        return true;
    }
};

void setUp() {}
void tearDown() {}

static const char* kBody = "21600,300;64800,600\n25200,120";

void test_crc32_check_value() {
    const char* s = "123456789";
    TEST_ASSERT_EQUAL_HEX32(0xCBF43926u, crc32Update(0, (const uint8_t*)s, 9));
    // Nối tiếp nhiều đoạn = tính một lần.
    const uint32_t head = crc32Update(0, (const uint8_t*)s, 4);
    TEST_ASSERT_EQUAL_HEX32(0xCBF43926u, crc32Update(head, (const uint8_t*)s + 4, 5));
    TEST_ASSERT_EQUAL_HEX32(0u, crc32Update(0, nullptr, 0));
}

void test_region_after_queue_records() {
    // Queue: 0x0010 + 200 record × 10 byte = 0x07E0 ≤ 0x0800.
    TEST_ASSERT_TRUE(0x0010 + kQueueCapacity * 10 <= kScheduleCacheAddr);
    TEST_ASSERT_TRUE(kScheduleCacheAddr + kScheduleCacheHeaderLen + kScheduleCacheMaxLen <= 4096);
}

void test_empty_storage_has_no_cache() {
    RamStorage s;
    char body[1024];
    uint32_t day = 123;
    TEST_ASSERT_FALSE(scheduleCacheLoad(s, body, sizeof body, day));
    TEST_ASSERT_EQUAL_STRING("", body);
    TEST_ASSERT_EQUAL_UINT32(123, day);
    memset(s.mem, 0, sizeof s.mem);  // toàn 0
    TEST_ASSERT_FALSE(scheduleCacheLoad(s, body, sizeof body, day));
}

void test_save_then_load_roundtrip() {
    RamStorage s;
    TEST_ASSERT_EQUAL(CacheWrite::Written, scheduleCacheSave(s, kBody, 20402));
    // Layout: magic, len, fetchDay little-endian ở 0x0800, body ở 0x080C.
    TEST_ASSERT_EQUAL_HEX8(0x43, s.mem[0x800]);
    TEST_ASSERT_EQUAL_HEX8(0x53, s.mem[0x801]);
    TEST_ASSERT_EQUAL_UINT8(strlen(kBody), s.mem[0x802]);
    TEST_ASSERT_EQUAL_UINT8(0, s.mem[0x803]);
    TEST_ASSERT_EQUAL_MEMORY(kBody, s.mem + 0x80C, strlen(kBody));
    char body[1024];
    uint32_t day = 0;
    TEST_ASSERT_TRUE(scheduleCacheLoad(s, body, sizeof body, day));
    TEST_ASSERT_EQUAL_STRING(kBody, body);
    TEST_ASSERT_EQUAL_UINT32(20402, day);
}

void test_does_not_touch_queue_region() {
    RamStorage s;
    TEST_ASSERT_EQUAL(CacheWrite::Written, scheduleCacheSave(s, kBody, 1));
    for (int i = 0; i < 0x0800; i++) TEST_ASSERT_EQUAL_HEX8(0xFF, s.mem[i]);
}

void test_unchanged_writes_nothing() {
    RamStorage s;
    scheduleCacheSave(s, kBody, 20402);
    const int w = s.writes;
    TEST_ASSERT_EQUAL(CacheWrite::Unchanged, scheduleCacheSave(s, kBody, 20402));
    TEST_ASSERT_EQUAL(w, s.writes);
}

void test_only_fetch_day_changed_rewrites_header_only() {
    RamStorage s;
    scheduleCacheSave(s, kBody, 20402);
    const int w = s.writes;
    const int bytes = s.bytesWritten;
    TEST_ASSERT_EQUAL(CacheWrite::Written, scheduleCacheSave(s, kBody, 20403));
    TEST_ASSERT_EQUAL(w + 1, s.writes);
    TEST_ASSERT_EQUAL(bytes + kScheduleCacheHeaderLen, s.bytesWritten);
    TEST_ASSERT_EQUAL_HEX16(kScheduleCacheAddr, s.lastWriteAddr);
    char body[1024];
    uint32_t day = 0;
    TEST_ASSERT_TRUE(scheduleCacheLoad(s, body, sizeof body, day));
    TEST_ASSERT_EQUAL_UINT32(20403, day);
    TEST_ASSERT_EQUAL_STRING(kBody, body);
}

void test_same_length_body_change_detected_byte_by_byte() {
    RamStorage s;
    scheduleCacheSave(s, "21600,300", 5);
    TEST_ASSERT_EQUAL(CacheWrite::Written, scheduleCacheSave(s, "21600,301", 5));
    char body[64];
    uint32_t day = 0;
    TEST_ASSERT_TRUE(scheduleCacheLoad(s, body, sizeof body, day));
    TEST_ASSERT_EQUAL_STRING("21600,301", body);
}

void test_shorter_body_replaces_longer() {
    RamStorage s;
    scheduleCacheSave(s, kBody, 5);
    TEST_ASSERT_EQUAL(CacheWrite::Written, scheduleCacheSave(s, "21600", 5));
    char body[64];
    uint32_t day = 0;
    TEST_ASSERT_TRUE(scheduleCacheLoad(s, body, sizeof body, day));
    TEST_ASSERT_EQUAL_STRING("21600", body);
}

void test_corrupted_body_rejected() {
    RamStorage s;
    scheduleCacheSave(s, kBody, 7);
    s.mem[0x80C + 3] ^= 0x01;
    char body[1024];
    uint32_t day = 99;
    TEST_ASSERT_FALSE(scheduleCacheLoad(s, body, sizeof body, day));
    TEST_ASSERT_EQUAL_STRING("", body);
    TEST_ASSERT_EQUAL_UINT32(99, day);
}

void test_corrupted_fetch_day_rejected() {
    RamStorage s;
    scheduleCacheSave(s, kBody, 7);
    s.mem[0x804] ^= 0x01;  // fetchDay nằm trong CRC
    char body[1024];
    uint32_t day = 0;
    TEST_ASSERT_FALSE(scheduleCacheLoad(s, body, sizeof body, day));
}

void test_bad_magic_rejected() {
    RamStorage s;
    scheduleCacheSave(s, kBody, 7);
    s.mem[0x800] = 0x42;
    char body[1024];
    uint32_t day = 0;
    TEST_ASSERT_FALSE(scheduleCacheLoad(s, body, sizeof body, day));
}

void test_bad_len_rejected() {
    RamStorage s;
    scheduleCacheSave(s, kBody, 7);
    s.mem[0x802] = 0x00;  // len = 1024 > max
    s.mem[0x803] = 0x04;
    char body[2048];
    uint32_t day = 0;
    TEST_ASSERT_FALSE(scheduleCacheLoad(s, body, sizeof body, day));
    s.mem[0x802] = 0;  // len = 0
    s.mem[0x803] = 0;
    TEST_ASSERT_FALSE(scheduleCacheLoad(s, body, sizeof body, day));
}

void test_body_with_nul_rejected() {
    // Header + CRC hợp lệ nhưng body chứa '\0' ⇒ không coi là chuỗi lịch.
    RamStorage s;
    scheduleCacheSave(s, "abc", 1);
    s.mem[0x80C + 1] = 0;
    // Sửa CRC cho khớp để chỉ còn lỗi '\0'.
    uint8_t meta[6] = {3, 0, 1, 0, 0, 0};
    const uint8_t b[3] = {'a', 0, 'c'};
    const uint32_t crc = crc32Update(crc32Update(0, meta, 6), b, 3);
    s.mem[0x808] = (uint8_t)crc;
    s.mem[0x809] = (uint8_t)(crc >> 8);
    s.mem[0x80A] = (uint8_t)(crc >> 16);
    s.mem[0x80B] = (uint8_t)(crc >> 24);
    char body[16];
    uint32_t day = 0;
    TEST_ASSERT_FALSE(scheduleCacheLoad(s, body, sizeof body, day));
}

void test_partial_write_body_new_header_old_rejected() {
    // Mất điện sau khi ghi body mới, trước khi ghi header ⇒ CRC sai ⇒ không cache.
    RamStorage s;
    scheduleCacheSave(s, "21600,300", 5);
    RamStorage copy = s;
    scheduleCacheSave(copy, "21600,999", 5);
    memcpy(s.mem + 0x80C, copy.mem + 0x80C, 9);  // chỉ body mới
    char body[64];
    uint32_t day = 0;
    TEST_ASSERT_FALSE(scheduleCacheLoad(s, body, sizeof body, day));
}

void test_cap_too_small_rejected() {
    RamStorage s;
    scheduleCacheSave(s, "21600,300", 5);
    char body[9];  // cần 10 (kể cả '\0')
    uint32_t day = 0;
    TEST_ASSERT_FALSE(scheduleCacheLoad(s, body, sizeof body, day));
    char ok[10];
    TEST_ASSERT_TRUE(scheduleCacheLoad(s, ok, sizeof ok, day));
}

void test_read_error_on_load() {
    RamStorage s;
    scheduleCacheSave(s, kBody, 5);
    char body[1024];
    uint32_t day = 0;
    s.failReads = 1;  // header
    TEST_ASSERT_FALSE(scheduleCacheLoad(s, body, sizeof body, day));
    s.failReads = 0;
    TEST_ASSERT_TRUE(scheduleCacheLoad(s, body, sizeof body, day));
}

void test_read_error_on_save_rewrites() {
    RamStorage s;
    scheduleCacheSave(s, kBody, 5);
    const int w = s.writes;
    s.failReads = 1;  // không đọc được header ⇒ coi như khác, ghi lại
    TEST_ASSERT_EQUAL(CacheWrite::Written, scheduleCacheSave(s, kBody, 5));
    TEST_ASSERT_EQUAL(w + 2, s.writes);
    char body[1024];
    uint32_t day = 0;
    TEST_ASSERT_TRUE(scheduleCacheLoad(s, body, sizeof body, day));
    TEST_ASSERT_EQUAL_STRING(kBody, body);
}

void test_write_failure_reported() {
    RamStorage s;
    s.failWrites = 1;
    TEST_ASSERT_EQUAL(CacheWrite::Failed, scheduleCacheSave(s, kBody, 5));
    RamStorage t;
    scheduleCacheSave(t, kBody, 5);
    t.failWrites = 1;  // chỉ header cần ghi (đổi fetchDay) ⇒ lỗi
    TEST_ASSERT_EQUAL(CacheWrite::Failed, scheduleCacheSave(t, kBody, 6));
}

void test_invalid_body_not_written() {
    RamStorage s;
    TEST_ASSERT_EQUAL(CacheWrite::Invalid, scheduleCacheSave(s, "", 5));
    TEST_ASSERT_EQUAL(CacheWrite::Invalid, scheduleCacheSave(s, nullptr, 5));
    static char big[kScheduleCacheMaxLen + 2];
    memset(big, '1', kScheduleCacheMaxLen + 1);
    big[kScheduleCacheMaxLen + 1] = '\0';
    TEST_ASSERT_EQUAL(CacheWrite::Invalid, scheduleCacheSave(s, big, 5));
    TEST_ASSERT_EQUAL(0, s.writes);
}

void test_max_len_body_fits() {
    RamStorage s;
    static char big[kScheduleCacheMaxLen + 1];
    memset(big, '7', kScheduleCacheMaxLen);
    big[kScheduleCacheMaxLen] = '\0';
    TEST_ASSERT_EQUAL(CacheWrite::Written, scheduleCacheSave(s, big, 5));
    static char out[kScheduleCacheMaxLen + 1];
    uint32_t day = 0;
    TEST_ASSERT_TRUE(scheduleCacheLoad(s, out, sizeof out, day));
    TEST_ASSERT_EQUAL_STRING(big, out);
    TEST_ASSERT_EQUAL(CacheWrite::Unchanged, scheduleCacheSave(s, big, 5));
}

int main(int, char**) {
    UNITY_BEGIN();
    RUN_TEST(test_crc32_check_value);
    RUN_TEST(test_region_after_queue_records);
    RUN_TEST(test_empty_storage_has_no_cache);
    RUN_TEST(test_save_then_load_roundtrip);
    RUN_TEST(test_does_not_touch_queue_region);
    RUN_TEST(test_unchanged_writes_nothing);
    RUN_TEST(test_only_fetch_day_changed_rewrites_header_only);
    RUN_TEST(test_same_length_body_change_detected_byte_by_byte);
    RUN_TEST(test_shorter_body_replaces_longer);
    RUN_TEST(test_corrupted_body_rejected);
    RUN_TEST(test_corrupted_fetch_day_rejected);
    RUN_TEST(test_bad_magic_rejected);
    RUN_TEST(test_bad_len_rejected);
    RUN_TEST(test_body_with_nul_rejected);
    RUN_TEST(test_partial_write_body_new_header_old_rejected);
    RUN_TEST(test_cap_too_small_rejected);
    RUN_TEST(test_read_error_on_load);
    RUN_TEST(test_read_error_on_save_rewrites);
    RUN_TEST(test_write_failure_reported);
    RUN_TEST(test_invalid_body_not_written);
    RUN_TEST(test_max_len_body_fits);
    return UNITY_END();
}
