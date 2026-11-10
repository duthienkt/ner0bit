#include <unity.h>

#include "led_pattern.h"

void setUp() {}
void tearDown() {}

void test_priority() {
    TEST_ASSERT_TRUE(pickLed(true, false, false) == LedState::Pumping);  // bơm thắng mọi thứ
    TEST_ASSERT_TRUE(pickLed(false, false, true) == LedState::NoClock);
    TEST_ASSERT_TRUE(pickLed(false, false, false) == LedState::NoClock);  // không giờ > không WiFi
    TEST_ASSERT_TRUE(pickLed(false, true, false) == LedState::NoWifi);
    TEST_ASSERT_TRUE(pickLed(false, true, true) == LedState::Idle);
}

void test_pumping_fast_blink() {
    TEST_ASSERT_TRUE(ledOn(LedState::Pumping, 0));
    TEST_ASSERT_TRUE(ledOn(LedState::Pumping, 149));
    TEST_ASSERT_FALSE(ledOn(LedState::Pumping, 150));
    TEST_ASSERT_FALSE(ledOn(LedState::Pumping, 299));
    TEST_ASSERT_TRUE(ledOn(LedState::Pumping, 300));
}

void test_no_wifi_long_blink() {
    TEST_ASSERT_TRUE(ledOn(LedState::NoWifi, 0));
    TEST_ASSERT_TRUE(ledOn(LedState::NoWifi, 999));
    TEST_ASSERT_FALSE(ledOn(LedState::NoWifi, 1000));
    TEST_ASSERT_FALSE(ledOn(LedState::NoWifi, 1999));
    TEST_ASSERT_TRUE(ledOn(LedState::NoWifi, 2000));
}

void test_no_clock_double_blink() {
    TEST_ASSERT_TRUE(ledOn(LedState::NoClock, 50));
    TEST_ASSERT_FALSE(ledOn(LedState::NoClock, 150));
    TEST_ASSERT_TRUE(ledOn(LedState::NoClock, 250));
    TEST_ASSERT_FALSE(ledOn(LedState::NoClock, 350));
    TEST_ASSERT_FALSE(ledOn(LedState::NoClock, 1500));
    TEST_ASSERT_TRUE(ledOn(LedState::NoClock, 2050));
}

void test_idle_short_blip() {
    TEST_ASSERT_TRUE(ledOn(LedState::Idle, 10));
    TEST_ASSERT_FALSE(ledOn(LedState::Idle, 60));
    TEST_ASSERT_FALSE(ledOn(LedState::Idle, 4999));
    TEST_ASSERT_TRUE(ledOn(LedState::Idle, 5010));
}

void test_millis_wrap_safe() {
    // millis() quay vòng sau ~49,7 ngày: pattern vẫn tuần hoàn, không treo ở một mức.
    const uint32_t big = 0xFFFFFF00u;
    int changes = 0;
    bool prev = ledOn(LedState::Pumping, big);
    for (uint32_t t = big; t != big + 1000; t += 10) {
        const bool cur = ledOn(LedState::Pumping, t);
        if (cur != prev) changes++;
        prev = cur;
    }
    TEST_ASSERT_TRUE(changes >= 4);
}

int main() {
    UNITY_BEGIN();
    RUN_TEST(test_priority);
    RUN_TEST(test_pumping_fast_blink);
    RUN_TEST(test_no_wifi_long_blink);
    RUN_TEST(test_no_clock_double_blink);
    RUN_TEST(test_idle_short_blip);
    RUN_TEST(test_millis_wrap_safe);
    return UNITY_END();
}
