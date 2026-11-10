#include "schedule.h"

#include <stdlib.h>
#include <string.h>

namespace {

constexpr size_t kMaxLineLen = 512;

bool isSpace(char c) { return c == ' ' || c == '\t' || c == '\r' || c == '\n'; }

// Trim [b, e): chỉ dịch con trỏ, không sửa dữ liệu.
void trim(const char*& b, const char*& e) {
    while (b < e && isSpace(*b)) b++;
    while (e > b && isSpace(*(e - 1))) e--;
}

// Token toàn chữ số (không dấu, không rỗng), vừa uint32.
bool parseNumber(const char* b, const char* e, uint32_t& out) {
    trim(b, e);
    if (b == e) return false;
    char buf[16];
    size_t len = (size_t)(e - b);
    if (len >= sizeof buf) return false;
    for (size_t i = 0; i < len; i++) {
        if (b[i] < '0' || b[i] > '9') return false;
        buf[i] = b[i];
    }
    buf[len] = '\0';
    unsigned long long v = strtoull(buf, nullptr, 10);
    if (v > 0xFFFFFFFFULL) return false;
    out = (uint32_t)v;
    return true;
}

// Token dạng "<giây>[:<duration>]"; sai ⇒ false.
bool parseEntry(const char* b, const char* e, Entry& out) {
    const char* colon = (const char*)memchr(b, ':', (size_t)(e - b));
    uint32_t start, dur = kDefaultDuration;
    if (!parseNumber(b, colon ? colon : e, start)) return false;
    if (start >= 86400) return false;
    if (colon && (!parseNumber(colon + 1, e, dur) || dur == 0)) return false;
    out.start = start;
    out.duration = dur;
    return true;
}

// Parse một dòng (đã copy, kết thúc bằng '\0'). Trả true nếu dòng rỗng sau trim.
bool parseLine(const char* s, size_t len, Line& line) {
    line.n = 0;
    const char* b = s;
    const char* end = s + len;
    trim(b, end);
    if (b == end) return true;
    while (b <= end && line.n < kMaxEntries) {
        const char* comma = (const char*)memchr(b, ',', (size_t)(end - b));
        const char* te = comma ? comma : end;
        Entry en;
        if (parseEntry(b, te, en)) line.e[line.n++] = en;
        if (!comma) break;
        b = comma + 1;
    }
    return false;
}

// Copy dòng [b, e) vào buf (cắt ở kMaxLineLen).
size_t copyLine(const char* b, const char* e, char* buf) {
    size_t len = (size_t)(e - b);
    if (len > kMaxLineLen) len = kMaxLineLen;
    memcpy(buf, b, len);
    buf[len] = '\0';
    return len;
}

}  // namespace

bool parseSchedule(const char* body, Schedule& out) {
    memset(&out, 0, sizeof out);
    out.l2 = Line2State::Missing;
    if (!body) return false;

    char buf[kMaxLineLen + 1];
    const char* nl1 = strchr(body, '\n');
    size_t n1 = copyLine(body, nl1 ? nl1 : body + strlen(body), buf);
    parseLine(buf, n1, out.line1);

    if (nl1) {
        const char* l2 = nl1 + 1;
        const char* nl2 = strchr(l2, '\n');
        size_t n2 = copyLine(l2, nl2 ? nl2 : l2 + strlen(l2), buf);
        bool empty = parseLine(buf, n2, out.line2);
        out.l2 = empty ? Line2State::Empty : (out.line2.n > 0 ? Line2State::Present : Line2State::Missing);
    }
    return out.line1.n > 0 || out.line2.n > 0;
}
