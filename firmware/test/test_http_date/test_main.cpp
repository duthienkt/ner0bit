#include <unity.h>
#include "http_date.h"

void setUp() {}
void tearDown() {}

static void expectOk(const char* s, uint32_t want) {
    uint32_t got = 12345;
    TEST_ASSERT_TRUE_MESSAGE(parseHttpDate(s, got), s);
    TEST_ASSERT_EQUAL_UINT32_MESSAGE(want, got, s);
}

static void expectBad(const char* s) {
    uint32_t got = 12345;
    TEST_ASSERT_FALSE_MESSAGE(parseHttpDate(s, got), s);
}

void test_valid_dates() {
    expectOk("Sun, 06 Nov 1994 08:49:37 GMT", 784111777);  // ví dụ trong RFC 7231
    expectOk("Thu, 01 Jan 1970 00:00:00 GMT", 0);
    expectOk("Tue, 10 Nov 2026 08:05:00 GMT", 1794297900);
    expectOk("Thu, 01 Jan 2026 00:00:00 GMT", 1767225600);
    expectOk("Thu, 31 Dec 2099 23:59:59 GMT", 4102444799);
    expectOk("Fri, 01 Jan 2100 00:00:00 GMT", 4102444800);
    expectOk("Sat, 31 Dec 2016 23:59:60 GMT", 1483228800);  // leap second ⇒ giây kế tiếp
}

void test_leap_day() {
    expectOk("Thu, 29 Feb 2024 12:00:00 GMT", 1709208000);
    expectOk("Tue, 29 Feb 2000 00:00:00 GMT", 951782400);   // chia hết 400 ⇒ nhuận
    expectBad("Sat, 29 Feb 2025 00:00:00 GMT");             // 2025 không nhuận
    expectBad("Mon, 29 Feb 2100 00:00:00 GMT");             // chia hết 100, không 400
    expectBad("Fri, 30 Feb 2024 00:00:00 GMT");
}

void test_garbage_and_short() {
    expectBad(nullptr);
    expectBad("");
    expectBad("garbage");
    expectBad("Sun, 06 Nov 1994");
    expectBad("Sun, 06 Nov 1994 08:49:37 GM");
    expectBad("Sun, 06 Nov 1994 08:49:37 GMT ");     // thừa ký tự
    expectBad("Sun, 06 Nov 1994 08:49:37 UTC");      // phải là GMT
    expectBad("Sun, 06 Nov 1994 08:49:37 +0000");
    expectBad("Sunday, 06-Nov-94 08:49:37 GMT");     // RFC 850, không nhận
    expectBad("Sun Nov  6 08:49:37 1994");           // asctime, không nhận
    expectBad("Sun, 6 Nov 1994 08:49:37 GMT");       // ngày phải 2 chữ số
    expectBad("Sun, 06 Nov 1994 8:49:37 GMT");
    expectBad("Sun,06 Nov 1994 08:49:37 GMT");
    expectBad("Sun, 06 Nov 1994 08-49-37 GMT");
    expectBad("Sun, 0x Nov 1994 08:49:37 GMT");
    expectBad("Sun, 06 Nov 19a4 08:49:37 GMT");
    expectBad("Sun, 06 Nov 1994 08:4 :37 GMT");
    expectBad("Sun, -6 Nov 1994 08:49:37 GMT");
    expectBad("Xyz, 06 Nov 1994 08:49:37 GMT");      // tên thứ sai
    expectBad("sun, 06 Nov 1994 08:49:37 GMT");      // phân biệt hoa-thường
}

void test_wrong_month_name() {
    expectBad("Sun, 06 Nob 1994 08:49:37 GMT");
    expectBad("Sun, 06 nov 1994 08:49:37 GMT");
    expectBad("Sun, 06 NOV 1994 08:49:37 GMT");
    expectBad("Sun, 06 Sept 1994 08:49:37 GM");
    // Đủ 12 tháng đều nhận.
    const char* months[12] = {"Jan", "Feb", "Mar", "Apr", "May", "Jun",
                              "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"};
    // Ngày 01 mỗi tháng năm 2026 (không nhuận).
    const uint32_t firstDay[12] = {0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334};
    for (int i = 0; i < 12; i++) {
        char buf[32] = "Mon, 01 XXX 2026 00:00:00 GMT";
        buf[8] = months[i][0];
        buf[9] = months[i][1];
        buf[10] = months[i][2];
        expectOk(buf, 1767225600u + firstDay[i] * 86400u);
    }
}

void test_out_of_range_fields() {
    expectBad("Sun, 00 Nov 1994 08:49:37 GMT");
    expectBad("Sun, 31 Nov 1994 08:49:37 GMT");  // tháng 11 có 30 ngày
    expectBad("Sun, 32 Jan 1994 08:49:37 GMT");
    expectBad("Sun, 06 Nov 1994 24:00:00 GMT");
    expectBad("Sun, 06 Nov 1994 08:60:37 GMT");
    expectBad("Sun, 06 Nov 1994 08:49:61 GMT");
    expectBad("Wed, 31 Dec 1969 23:59:59 GMT");  // trước 1970
    expectBad("Sun, 07 Feb 2106 06:28:16 GMT");  // vượt uint32_t
    expectBad("Sat, 01 Jan 9999 00:00:00 GMT");
    expectOk("Sun, 07 Feb 2106 06:28:15 GMT", 4294967295u);  // biên trên uint32_t
}

int main(int, char**) {
    UNITY_BEGIN();
    RUN_TEST(test_valid_dates);
    RUN_TEST(test_leap_day);
    RUN_TEST(test_garbage_and_short);
    RUN_TEST(test_wrong_month_name);
    RUN_TEST(test_out_of_range_fields);
    return UNITY_END();
}
