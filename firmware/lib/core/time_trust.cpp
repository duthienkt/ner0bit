#include "time_trust.h"

namespace {

uint32_t absDiff(uint32_t a, uint32_t b) { return a > b ? a - b : b - a; }

}  // namespace

NtpVerdict TimeTrust::onNtp(uint32_t ntpUtc, uint32_t rtcUtc, bool rtcValid, uint32_t mono) {
    if (ntpUtc < kMinValidUtc || ntpUtc >= kMaxValidUtc) return NtpVerdict::Reject;
    if (!rtcValid || absDiff(ntpUtc, rtcUtc) <= kMaxClockSkewS) {
        pending_ = false;
        return NtpVerdict::Accept;
    }
    pending_ = true;
    candUtc_ = ntpUtc;
    candMono_ = mono;
    return NtpVerdict::Pending;
}

DateVerdict TimeTrust::onServerDate(uint32_t dateUtc, uint32_t mono, uint32_t& utcNow) {
    if (!pending_) return DateVerdict::NoCandidate;
    pending_ = false;
    const uint32_t projected = candUtc_ + (mono - candMono_);
    if (absDiff(dateUtc, projected) > kMaxClockSkewS) return DateVerdict::Drop;
    utcNow = projected;
    return DateVerdict::Confirm;
}
