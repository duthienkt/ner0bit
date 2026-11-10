#include <unity.h>
#include <stdio.h>
#include <string.h>
#include "schedule.h"

void setUp() {}
void tearDown() {}

static void expectEntry(const Line& l, int i, uint32_t start, uint32_t dur) {
    TEST_ASSERT_EQUAL_UINT32(start, l.e[i].start);
    TEST_ASSERT_EQUAL_UINT32(dur, l.e[i].duration);
}

void test_basic_two_lines() {
    Schedule s;
    TEST_ASSERT_TRUE(parseSchedule("25200:900,61200\n28800,46800:600", s));
    TEST_ASSERT_EQUAL_UINT8(2, s.line1.n);
    expectEntry(s.line1, 0, 25200, 900);
    expectEntry(s.line1, 1, 61200, 300);
    TEST_ASSERT_TRUE(s.l2 == Line2State::Present);
    TEST_ASSERT_EQUAL_UINT8(2, s.line2.n);
    expectEntry(s.line2, 0, 28800, 300);
    expectEntry(s.line2, 1, 46800, 600);
}

void test_trailing_newline_is_empty_line2() {
    Schedule s;
    TEST_ASSERT_TRUE(parseSchedule("25200\n", s));
    TEST_ASSERT_EQUAL_UINT8(1, s.line1.n);
    TEST_ASSERT_TRUE(s.l2 == Line2State::Empty);
}

void test_no_newline_is_missing() {
    Schedule s;
    TEST_ASSERT_TRUE(parseSchedule("25200", s));
    TEST_ASSERT_TRUE(s.l2 == Line2State::Missing);
}

void test_line2_all_invalid_is_missing() {
    Schedule s;
    TEST_ASSERT_TRUE(parseSchedule("25200\nabc", s));
    TEST_ASSERT_TRUE(s.l2 == Line2State::Missing);
}

void test_invalid_entries_skipped() {
    Schedule s;
    TEST_ASSERT_TRUE(parseSchedule("abc,25200,90000,3600:0,-5,7200:60", s));
    TEST_ASSERT_EQUAL_UINT8(2, s.line1.n);
    expectEntry(s.line1, 0, 25200, 300);
    expectEntry(s.line1, 1, 7200, 60);
}

void test_max_20_entries() {
    char buf[256];
    int p = 0;
    for (int i = 0; i < 25; i++) p += snprintf(buf + p, sizeof buf - p, i ? ",%d" : "%d", i * 60);
    Schedule s;
    TEST_ASSERT_TRUE(parseSchedule(buf, s));
    TEST_ASSERT_EQUAL_UINT8(20, s.line1.n);
    TEST_ASSERT_EQUAL_UINT32(1140, s.line1.e[19].start);
}

void test_crlf_and_spaces() {
    Schedule s;
    TEST_ASSERT_TRUE(parseSchedule("  25200 : 900 , 61200 \r\n 28800 \r\n", s));
    TEST_ASSERT_EQUAL_UINT8(2, s.line1.n);
    expectEntry(s.line1, 0, 25200, 900);
    expectEntry(s.line1, 1, 61200, 300);
    TEST_ASSERT_TRUE(s.l2 == Line2State::Present);
    TEST_ASSERT_EQUAL_UINT8(1, s.line2.n);
    expectEntry(s.line2, 0, 28800, 300);
}

void test_extra_lines_ignored() {
    Schedule s;
    TEST_ASSERT_TRUE(parseSchedule("100\n200\n300", s));
    TEST_ASSERT_EQUAL_UINT8(1, s.line1.n);
    TEST_ASSERT_EQUAL_UINT32(100, s.line1.e[0].start);
    TEST_ASSERT_TRUE(s.l2 == Line2State::Present);
    TEST_ASSERT_EQUAL_UINT8(1, s.line2.n);
    TEST_ASSERT_EQUAL_UINT32(200, s.line2.e[0].start);
}

void test_both_invalid_returns_false() {
    Schedule s;
    TEST_ASSERT_FALSE(parseSchedule("abc\n", s));
    TEST_ASSERT_FALSE(parseSchedule("", s));
    TEST_ASSERT_FALSE(parseSchedule("\n\n", s));
}

void test_line1_empty_line2_present_ok() {
    Schedule s;
    TEST_ASSERT_TRUE(parseSchedule("\n25200", s));
    TEST_ASSERT_EQUAL_UINT8(0, s.line1.n);
    TEST_ASSERT_TRUE(s.l2 == Line2State::Present);
    TEST_ASSERT_EQUAL_UINT8(1, s.line2.n);
}

int main() {
    UNITY_BEGIN();
    RUN_TEST(test_basic_two_lines);
    RUN_TEST(test_trailing_newline_is_empty_line2);
    RUN_TEST(test_no_newline_is_missing);
    RUN_TEST(test_line2_all_invalid_is_missing);
    RUN_TEST(test_invalid_entries_skipped);
    RUN_TEST(test_max_20_entries);
    RUN_TEST(test_crlf_and_spaces);
    RUN_TEST(test_extra_lines_ignored);
    RUN_TEST(test_both_invalid_returns_false);
    RUN_TEST(test_line1_empty_line2_present_ok);
    return UNITY_END();
}
