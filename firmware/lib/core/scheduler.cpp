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
        } else {
            r.pumpOn = true;
        }
        return r;
    }

    if (!hasSchedule_) return r;

    const uint32_t today = now / 86400;
    bool found = false;
    uint32_t bestStart = 0, bestCap = 0;
    Source bestSrc = Source::Normal;
    bool bestCapped = false;

    // Hôm qua trước (absStart nhỏ hơn), rồi hôm nay; chọn absStart nhỏ nhất còn mở.
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
            if (!(absStart <= now && now < absStart + capDur)) continue;
            if (absStart <= handledUpTo_) continue;
            if (found && absStart >= bestStart) continue;
            found = true;
            bestStart = absStart;
            bestCap = capDur;
            bestSrc = src;
            bestCapped = e.duration > kHardCap;
        }
    }
    if (!found) return r;

    handledUpTo_ = bestStart;
    if (restViolated(now, mono)) {
        r.hasEvent = true;
        r.event = Event{bestStart, 0, bestSrc, kSkippedRest};
    } else {
        running_ = true;
        startActual_ = now;
        stopAt_ = bestStart + bestCap;
        runDur_ = stopAt_ - now;
        monoStart_ = mono;
        runSource_ = bestSrc;
        runFlags_ = bestCapped ? kCapped : 0;
        r.pumpOn = true;
    }
    return r;
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
