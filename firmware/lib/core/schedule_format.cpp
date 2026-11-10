#include "schedule_format.h"

#include <stdarg.h>
#include <stdio.h>
#include <string.h>

#include "scheduler.h"  // kHardCap

namespace {

struct Out {
    char* buf;
    size_t cap;
    size_t len;
    bool full;

    void add(const char* fmt, ...) {
        if (full) return;
        va_list ap;
        va_start(ap, fmt);
        const int n = vsnprintf(buf + len, cap - len, fmt, ap);
        va_end(ap);
        if (n < 0 || (size_t)n >= cap - len) {
            full = true;
            len = cap - 1;  // vsnprintf đã ghi phần vừa và '\0'
            if (cap >= 4) memcpy(buf + cap - 4, "...", 4);
            return;
        }
        len += (size_t)n;
    }
};

void addDuration(Out& o, uint32_t d) {
    if (d % 60 == 0) o.add("%lup", (unsigned long)(d / 60));
    else o.add("%lus", (unsigned long)d);
}

void addLine(Out& o, const Line& l) {
    if (l.n == 0) {
        o.add("(trống)");
        return;
    }
    for (uint8_t i = 0; i < l.n; i++) {
        const uint32_t t = l.e[i].start;
        const uint32_t d = l.e[i].duration;
        o.add(i ? ", %02lu:%02lu" : "%02lu:%02lu", (unsigned long)(t / 3600),
              (unsigned long)(t / 60 % 60));
        if (t % 60) o.add(":%02lu", (unsigned long)(t % 60));
        o.add(" ");
        addDuration(o, d);
        if (d > kHardCap) {
            o.add(" (cắt ");
            addDuration(o, kHardCap);
            o.add(")");
        }
    }
}

}  // namespace

size_t formatSchedule(const Schedule& s, char* buf, size_t cap) {
    if (!buf || cap == 0) return 0;
    buf[0] = '\0';
    Out o{buf, cap, 0, false};
    o.add("hằng ngày: ");
    addLine(o, s.line1);
    o.add(" | hôm nay: ");
    switch (s.l2) {
        case Line2State::Missing: o.add("theo hằng ngày"); break;
        case Line2State::Empty: o.add("KHÔNG TƯỚI"); break;
        case Line2State::Present: addLine(o, s.line2); break;
    }
    return o.len;
}
