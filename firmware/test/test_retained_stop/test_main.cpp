#include <unity.h>

#include "retained_stop.h"

void setUp() {}
void tearDown() {}

static const uint32_t kStop = 1794322800;  // giờ VN epoch bất kỳ

static RetainedStop written(uint32_t v) {
    RetainedStop r;
    retainedStopWrite(r, v);
    return r;
}

// Ghi rồi đọc lại sau reset mềm ⇒ hợp lệ, giữ đúng giá trị (kể cả 0).
void test_written_copy_is_valid() {
    RetainedStop r = written(kStop);
    TEST_ASSERT_TRUE(retainedStopValid(r, false));
    TEST_ASSERT_EQUAL_UINT32(kStop, r.lastStop);
    TEST_ASSERT_TRUE(retainedStopValid(written(0), false));
}

// Cấp điện lại ⇒ không tin bản sao dù magic/check đúng.
void test_power_on_invalidates() {
    TEST_ASSERT_FALSE(retainedStopValid(written(kStop), true));
}

// RAM rác (toàn 0, toàn 1, magic đúng mà check sai, giá trị bị lật bit) ⇒ không hợp lệ.
void test_garbage_is_invalid() {
    RetainedStop zero = {0, 0, 0};
    TEST_ASSERT_FALSE(retainedStopValid(zero, false));
    RetainedStop ones = {0xFFFFFFFFu, 0xFFFFFFFFu, 0xFFFFFFFFu};
    TEST_ASSERT_FALSE(retainedStopValid(ones, false));
    RetainedStop badCheck = {kRetainedStopMagic, kStop, 0};
    TEST_ASSERT_FALSE(retainedStopValid(badCheck, false));
    RetainedStop flipped = written(kStop);
    flipped.lastStop ^= 0x10;
    TEST_ASSERT_FALSE(retainedStopValid(flipped, false));
    RetainedStop badMagic = written(kStop);
    badMagic.magic ^= 1;
    TEST_ASSERT_FALSE(retainedStopValid(badMagic, false));
}

// Chế độ RAM: storage = 0 ⇒ lấy bản sao (tắt 06:30, reset 06:31 ⇒ mốc 06:35 vẫn bị nghỉ).
void test_seed_ram_mode_uses_copy() {
    TEST_ASSERT_EQUAL_UINT32(kStop, retainedStopSeed(0, written(kStop), false));
}

// AT24C32: lấy giá trị mới hơn giữa storage và bản sao.
void test_seed_takes_newer() {
    TEST_ASSERT_EQUAL_UINT32(kStop + 60, retainedStopSeed(kStop + 60, written(kStop), false));
    TEST_ASSERT_EQUAL_UINT32(kStop + 60, retainedStopSeed(kStop, written(kStop + 60), false));
    TEST_ASSERT_EQUAL_UINT32(kStop, retainedStopSeed(kStop, written(0), false));
}

// Bản sao không hợp lệ hoặc cấp điện lại ⇒ chỉ dùng storage.
void test_seed_invalid_copy_uses_storage() {
    RetainedStop zero = {0, 0, 0};
    TEST_ASSERT_EQUAL_UINT32(kStop, retainedStopSeed(kStop, zero, false));
    TEST_ASSERT_EQUAL_UINT32(0, retainedStopSeed(0, zero, false));
    TEST_ASSERT_EQUAL_UINT32(0, retainedStopSeed(0, written(kStop), true));
    TEST_ASSERT_EQUAL_UINT32(kStop, retainedStopSeed(kStop, written(kStop + 60), true));
}

int main(int, char**) {
    UNITY_BEGIN();
    RUN_TEST(test_written_copy_is_valid);
    RUN_TEST(test_power_on_invalidates);
    RUN_TEST(test_garbage_is_invalid);
    RUN_TEST(test_seed_ram_mode_uses_copy);
    RUN_TEST(test_seed_takes_newer);
    RUN_TEST(test_seed_invalid_copy_uses_storage);
    return UNITY_END();
}
