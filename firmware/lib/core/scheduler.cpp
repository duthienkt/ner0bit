#include "scheduler.h"

void Scheduler::setSchedule(const Schedule& s, uint32_t fetchDay) {
    sched_ = s;
    fetchDay_ = fetchDay;
    hasSchedule_ = true;
}

void Scheduler::setFetchOk(bool ok) { fetchOk_ = ok; }
void Scheduler::setLastStop(uint32_t local) { lastStop_ = local; }

TickResult Scheduler::tick(uint32_t now, uint32_t mono) {
    TickResult r{};
    r.lastStop = lastStop_;

    // Đồng hồ lùi xa (vd. RTC chạy trước vài ngày rồi NTP chỉnh lại): handledUpTo_ chỉ tiến,
    // không kéo về thì mọi mốc bị chặn tới khi đồng hồ đuổi kịp.
    if (now + 86400 < handledUpTo_) handledUpTo_ = now;

    if (running_) {
        if (now >= stopAt_ || (uint32_t)(mono - monoStart_) >= runDur_) {
            running_ = false;
            lastStop_ = now;
            haveRunStop_ = true;
            monoLastStop_ = mono;
            r.hasEvent = true;
            r.event = Event{startActual_, now, runSource_, runFlags_};
            r.lastStopChanged = true;
            r.lastStop = now;
            return r;
        }
        r.pumpOn = true;
        // Mốc tới giờ khi bơm đang chạy phiên khác ⇒ bỏ, báo "busy" (mỗi tick tối đa 1 event).
        Candidate c;
        if (hasSchedule_ && findDue(now, false, c)) {
            handledUpTo_ = c.start;
            r.hasEvent = true;
            r.event = Event{c.start, 0, c.src, kSkippedBusy};
        }
        return r;
    }

    if (!hasSchedule_) return r;

    Candidate c;
    if (!findDue(now, true, c)) return r;

    handledUpTo_ = c.start;
    if (restViolated(now, mono)) {
        r.hasEvent = true;
        r.event = Event{c.start, 0, c.src, kSkippedRest};
    } else {
        running_ = true;
        startActual_ = now;
        stopAt_ = c.start + c.cap;
        runDur_ = stopAt_ - now;
        monoStart_ = mono;
        runSource_ = c.src;
        runFlags_ = c.capped ? kCapped : 0;
        r.pumpOn = true;
    }
    return r;
}

bool Scheduler::findDue(uint32_t now, bool requireOpen, Candidate& best) const {
    const uint32_t today = now / 86400;
    bool found = false;
    // Hôm qua (mốc qua 0h) và hôm nay; chọn absStart nhỏ nhất trong (handledUpTo_, now].
    for (int back = 1; back >= 0; back--) {
        if (back == 1 && today == 0) continue;
        const uint32_t day = today - back;
        Source src;
        const Line* line = lineForDay(day, src);
        if (!line) continue;
        for (uint8_t i = 0; i < line->n; i++) {
            const Entry& e = line->e[i];
            const uint32_t absStart = day * 86400 + e.start;
            const uint32_t capDur = e.duration > kHardCap ? kHardCap : e.duration;
            if (absStart > now || absStart <= handledUpTo_) continue;
            if (requireOpen && now >= absStart + capDur) continue;
            if (found && absStart >= best.start) continue;
            found = true;
            best.start = absStart;
            best.cap = capDur;
            best.src = src;
            best.capped = e.duration > kHardCap;
        }
    }
    return found;
}

const Line* Scheduler::lineForDay(uint32_t day, Source& src) const {
    src = Source::Normal;
    const Line* line = &sched_.line1;
    if (fetchDay_ == day) {
        if (sched_.l2 == Line2State::Present) {
            line = &sched_.line2;
            src = Source::Today;
        } else if (sched_.l2 == Line2State::Empty) {
            return nullptr;
        }
    }
    if (!fetchOk_) src = Source::Failsafe;
    return line;
}

bool Scheduler::restViolated(uint32_t now, uint32_t mono) const {
    if (haveRunStop_) return (uint32_t)(mono - monoLastStop_) < kMinRest;
    if (lastStop_ == 0) return false;
    const uint32_t diff = now >= lastStop_ ? now - lastStop_ : lastStop_ - now;
    return diff < kMinRest;
}
