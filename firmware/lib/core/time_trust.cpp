#include "time_trust.h"

namespace {

uint32_t absDiff(uint32_t a, uint32_t b) { return a > b ? a - b : b - a; }

bool inBounds(uint64_t utc) { return utc >= kMinValidUtc && utc < kMaxValidUtc; }

}  // namespace

DateVerdict TimeTrust::onServerDate(uint32_t dateUtc, uint32_t monoAtResp, uint32_t rtcUtc,
                                    bool rtcOk, uint32_t monoNow, uint32_t& writeUtc) {
    const uint64_t now = (uint64_t)dateUtc + (uint32_t)(monoNow - monoAtResp);
    if (!inBounds(dateUtc) || !inBounds(now)) return DateVerdict::Reject;
    anchored_ = true;
    anchorUtc_ = dateUtc;
    anchorMono_ = monoAtResp;
    const uint32_t dateNow = (uint32_t)now;
    if (rtcOk && absDiff(rtcUtc, dateNow) <= kTrustWindowS) return DateVerdict::NoChange;
    writeUtc = dateNow;
    return DateVerdict::Write;
}

bool TimeTrust::anchorNow(uint32_t mono, uint32_t& utc) const {
    if (!anchored_) return false;
    const uint32_t age = mono - anchorMono_;
    if (age >= kAnchorMaxAgeS) return false;
    utc = anchorUtc_ + age;
    return true;
}

NtpVerdict TimeTrust::onNtp(uint32_t ntpUtc, uint32_t rtcUtc, bool rtcOk, uint32_t mono) const {
    if (!inBounds(ntpUtc)) return NtpVerdict::Reject;
    uint32_t ref = 0;
    if (anchorNow(mono, ref)) {
        return absDiff(ntpUtc, ref) <= kTrustWindowS ? NtpVerdict::Accept : NtpVerdict::Reject;
    }
    if (!rtcOk) return NtpVerdict::Accept;  // bootstrap: đồng hồ chưa hợp lệ, chưa có gì tốt hơn
    return absDiff(ntpUtc, rtcUtc) <= kTrustWindowS ? NtpVerdict::Accept : NtpVerdict::Reject;
}
