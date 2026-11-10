#include <unity.h>

#include "net_watch.h"

void setUp() {}
void tearDown() {}

// Dấu vừa đóng (cùng giây, vài giây trước) ⇒ còn sống.
void test_fresh() {
    TEST_ASSERT_FALSE(netStale(1000, 1000, kNetStaleS));
    TEST_ASSERT_FALSE(netStale(1000, 999, kNetStaleS));
    TEST_ASSERT_FALSE(netStale(1000, 1000 - 45, kNetStaleS));  // POST HTTPS chậm ~40 s
}

// Dấu cũ hẳn ⇒ treo.
void test_stale() {
    TEST_ASSERT_TRUE(netStale(10000, 1000, kNetStaleS));
    TEST_ASSERT_TRUE(netStale(0x7FFFFFFFu, 0, kNetStaleS));  // cũ nhất còn so được (~68 năm)
}

// Biên 180 s: đúng 180 s chưa tính là treo, 181 s mới tính.
void test_boundary() {
    TEST_ASSERT_FALSE(netStale(1180, 1000, 180));
    TEST_ASSERT_TRUE(netStale(1181, 1000, 180));
    TEST_ASSERT_FALSE(netStale(180, 0, 180));
    TEST_ASSERT_TRUE(netStale(181, 0, 180));
}

// monoSec() wrap qua 2^32: hiệu vẫn đúng.
void test_wrap_safe() {
    TEST_ASSERT_FALSE(netStale(5, 0xFFFFFFF0u, kNetStaleS));     // 21 s
    TEST_ASSERT_FALSE(netStale(164, 0xFFFFFFF0u, kNetStaleS));   // đúng 180 s
    TEST_ASSERT_TRUE(netStale(165, 0xFFFFFFF0u, kNetStaleS));    // 181 s
    TEST_ASSERT_TRUE(netStale(0x10, 0xFFFFFF00u, kNetStaleS));   // 272 s
}

// Net task đóng dấu ngay sau khi loop() lấy giờ ⇒ dấu mới hơn now vài giây: còn sống, không phải
// "cũ 4 tỉ giây".
void test_stamp_newer_than_now() {
    TEST_ASSERT_FALSE(netStale(100, 101, kNetStaleS));
    TEST_ASSERT_FALSE(netStale(0xFFFFFFFFu, 0, kNetStaleS));  // dấu đã qua wrap, now chưa
}

// Chỉ khởi động lại khi bơm tắt; bơm chạy ⇒ chờ dù net task treo.
void test_restart_only_when_pump_off() {
    TEST_ASSERT_TRUE(netRestartDue(10000, 1000, kNetStaleS, false));
    TEST_ASSERT_FALSE(netRestartDue(10000, 1000, kNetStaleS, true));
    TEST_ASSERT_FALSE(netRestartDue(1000, 1000, kNetStaleS, false));
    TEST_ASSERT_FALSE(netRestartDue(1000, 1000, kNetStaleS, true));
    TEST_ASSERT_FALSE(netRestartDue(1180, 1000, kNetStaleS, false));
    TEST_ASSERT_TRUE(netRestartDue(1181, 1000, kNetStaleS, false));
}

int main(int, char**) {
    UNITY_BEGIN();
    RUN_TEST(test_fresh);
    RUN_TEST(test_stale);
    RUN_TEST(test_boundary);
    RUN_TEST(test_wrap_safe);
    RUN_TEST(test_stamp_newer_than_now);
    RUN_TEST(test_restart_only_when_pump_off);
    return UNITY_END();
}
