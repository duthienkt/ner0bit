#include <unity.h>

#include "boot_window.h"

void setUp() {}
void tearDown() {}

// Chạy giả lập: nút nhấn trong [pressAt, releaseAt), lấy mẫu mỗi 10 ms; trả kết quả và thời điểm chốt.
static BootDecision run(uint32_t pressAt, uint32_t releaseAt, uint32_t* decidedAt = nullptr) {
    BootWindow w(0);
    for (uint32_t t = 0; t < 20000; t += 10) {
        const bool pressed = t >= pressAt && t < releaseAt;
        const BootDecision d = w.update(t, pressed);
        if (d != BootDecision::Waiting) {
            if (decidedAt) *decidedAt = t;
            return d;
        }
    }
    return BootDecision::Waiting;
}

void test_no_press_normal_after_window() {
    uint32_t at = 0;
    TEST_ASSERT_TRUE(run(UINT32_MAX, UINT32_MAX, &at) == BootDecision::Normal);
    TEST_ASSERT_EQUAL_UINT32(kBootWindowMs, at);
}

void test_press_early_hold_3s_setup() {
    uint32_t at = 0;
    TEST_ASSERT_TRUE(run(500, 10000, &at) == BootDecision::Setup);
    TEST_ASSERT_EQUAL_UINT32(500 + kBootHoldMs, at);
}

// Lỗi cũ: nhấn ở giây 3 thì cửa sổ 5 s hết trước khi đủ 3 s giữ. Giờ đang giữ thì chờ tiếp.
void test_press_late_in_window_still_setup() {
    uint32_t at = 0;
    TEST_ASSERT_TRUE(run(4500, 10000, &at) == BootDecision::Setup);
    TEST_ASSERT_EQUAL_UINT32(4500 + kBootHoldMs, at);
}

void test_release_before_3s_normal() {
    TEST_ASSERT_TRUE(run(1000, 2500) == BootDecision::Normal);
}

void test_release_late_press_after_window_normal() {
    // Nhả nút sau cửa sổ mà chưa đủ 3 s ⇒ chạy thường ngay lúc nhả.
    uint32_t at = 0;
    TEST_ASSERT_TRUE(run(4000, 6000, &at) == BootDecision::Normal);
    TEST_ASSERT_EQUAL_UINT32(6000, at);
}

void test_press_after_window_ignored() {
    TEST_ASSERT_TRUE(run(5100, 10000) == BootDecision::Normal);
}

void test_millis_wrap() {
    BootWindow w(0xFFFFF000u);
    BootDecision d = BootDecision::Waiting;
    uint32_t t = 0xFFFFF000u;
    for (int i = 0; i < 400 && d == BootDecision::Waiting; i++, t += 10) d = w.update(t, true);
    TEST_ASSERT_TRUE(d == BootDecision::Setup);
}

int main() {
    UNITY_BEGIN();
    RUN_TEST(test_no_press_normal_after_window);
    RUN_TEST(test_press_early_hold_3s_setup);
    RUN_TEST(test_press_late_in_window_still_setup);
    RUN_TEST(test_release_before_3s_normal);
    RUN_TEST(test_release_late_press_after_window_normal);
    RUN_TEST(test_press_after_window_ignored);
    RUN_TEST(test_millis_wrap);
    return UNITY_END();
}
