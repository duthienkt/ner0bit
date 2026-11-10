#include <unity.h>
#include <string.h>

#include "schedule.h"
#include "schedule_format.h"

void setUp() {}
void tearDown() {}

static const char* fmt(const char* body, char* buf, size_t cap) {
    Schedule s;
    TEST_ASSERT_TRUE(parseSchedule(body, s));
    formatSchedule(s, buf, cap);
    return buf;
}

void test_line1_only() {
    char b[256];
    TEST_ASSERT_EQUAL_STRING("hằng ngày: 07:00 15p, 17:00 5p | hôm nay: theo hằng ngày",
                             fmt("25200:900,61200", b, sizeof b));
}

void test_line2_empty_means_no_watering() {
    char b[256];
    TEST_ASSERT_EQUAL_STRING("hằng ngày: 07:00 15p | hôm nay: KHÔNG TƯỚI", fmt("25200:900\n", b, sizeof b));
}

void test_line2_present() {
    char b[256];
    TEST_ASSERT_EQUAL_STRING("hằng ngày: 07:00 5p | hôm nay: 08:00 5p, 13:00 10p",
                             fmt("25200\n28800,46800:600", b, sizeof b));
}

void test_seconds_and_odd_durations() {
    char b[256];
    TEST_ASSERT_EQUAL_STRING("hằng ngày: 07:00:30 90s, 23:59 1p | hôm nay: theo hằng ngày",
                             fmt("25230:90,86340:60", b, sizeof b));
}

void test_capped_duration_shown() {
    char b[256];
    TEST_ASSERT_EQUAL_STRING("hằng ngày: 06:00 60p (cắt 30p) | hôm nay: theo hằng ngày",
                             fmt("21600:3600", b, sizeof b));
}

void test_line1_empty() {
    char b[256];
    TEST_ASSERT_EQUAL_STRING("hằng ngày: (trống) | hôm nay: 08:00 5p", fmt("\n28800", b, sizeof b));
}

void test_truncates_safely() {
    char b[24];
    memset(b, 'x', sizeof b);
    const size_t n = formatSchedule(*[] {
        static Schedule s;
        parseSchedule("25200:900,61200,3600,7200", s);
        return &s;
    }(), b, sizeof b);
    TEST_ASSERT_EQUAL(strlen(b), n);
    TEST_ASSERT_TRUE(n < sizeof b);
    TEST_ASSERT_EQUAL_STRING("...", b + n - 3);  // cụt ⇒ kết thúc bằng "..."
}

void test_full_schedule_fits_log_buffer() {
    // 20 mốc mỗi dòng, dạng dài nhất, phải vừa kScheduleLogMax.
    char body[1024] = "";
    for (int i = 0; i < 20; i++) strcat(body, i ? ",86399:1799" : "86399:1799");
    strcat(body, "\n");
    for (int i = 0; i < 20; i++) strcat(body, i ? ",86399:3600" : "86399:3600");
    char b[kScheduleLogMax];
    const char* out = fmt(body, b, sizeof b);
    TEST_ASSERT_NULL(strstr(out, "..."));
}

int main() {
    UNITY_BEGIN();
    RUN_TEST(test_line1_only);
    RUN_TEST(test_line2_empty_means_no_watering);
    RUN_TEST(test_line2_present);
    RUN_TEST(test_seconds_and_odd_durations);
    RUN_TEST(test_capped_duration_shown);
    RUN_TEST(test_line1_empty);
    RUN_TEST(test_truncates_safely);
    RUN_TEST(test_full_schedule_fits_log_buffer);
    return UNITY_END();
}
