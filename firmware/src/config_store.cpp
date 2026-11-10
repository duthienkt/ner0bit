#include "config_store.h"

#include <Preferences.h>
#include <stdlib.h>
#include <string.h>

namespace config {

static constexpr const char* kNs = "ner0bit";
static constexpr const char* kSsid = "ssid";
static constexpr const char* kPass = "pass";
static constexpr const char* kUrl = "url";
static constexpr const char* kToken = "token";
static constexpr const char* kSched = "sched";
static constexpr const char* kFetchDay = "fday";

template <size_t N>
static void getField(Preferences& p, const char* key, char (&out)[N]) {
    if (!p.isKey(key) || p.getString(key, out, N) == 0) out[0] = '\0';
    out[N - 1] = '\0';
}

// Chép ra buffer tạm để chắc chắn có '\0' dù caller điền tràn mảng.
template <size_t N>
static void putField(Preferences& p, const char* key, const char (&in)[N]) {
    char tmp[N];
    memcpy(tmp, in, N);
    tmp[N - 1] = '\0';
    p.putString(key, tmp);
}

bool load(NetConfig& cfg) {
    memset(&cfg, 0, sizeof cfg);
    Preferences p;
    if (!p.begin(kNs, true)) return false;  // namespace chưa tồn tại (lần đầu)
    getField(p, kSsid, cfg.ssid);
    getField(p, kPass, cfg.pass);
    getField(p, kUrl, cfg.url);
    getField(p, kToken, cfg.token);
    p.end();
    return cfg.ssid[0] != '\0' && cfg.url[0] != '\0';
}

void save(const NetConfig& cfg) {
    Preferences p;
    if (!p.begin(kNs, false)) return;
    putField(p, kSsid, cfg.ssid);
    putField(p, kPass, cfg.pass);
    putField(p, kUrl, cfg.url);
    putField(p, kToken, cfg.token);
    p.end();
}

bool hasConfig() {
    NetConfig cfg;
    return load(cfg);
}

bool loadSchedule(char* body, size_t cap, uint32_t& fetchDay) {
    if (!body || cap == 0) return false;
    body[0] = '\0';
    Preferences p;
    if (!p.begin(kNs, true)) return false;
    bool ok = p.isKey(kSched) && p.isKey(kFetchDay) && p.getString(kSched, body, cap) > 1;
    if (ok) {
        fetchDay = p.getUInt(kFetchDay, 0);
    } else {
        body[0] = '\0';
    }
    p.end();
    return ok;
}

// true nếu chuỗi đã lưu ở key giống hệt s.
static bool sameString(Preferences& p, const char* key, const char* s) {
    if (!p.isKey(key)) return false;
    const size_t want = strlen(s) + 1;
    if (p.getStringLength(key) != want) return false;
    char* old = (char*)malloc(want);
    if (!old) return false;  // không so được ⇒ coi như khác, ghi đè
    const bool same = p.getString(key, old, want) == want && memcmp(old, s, want) == 0;
    free(old);
    return same;
}

void saveScheduleIfChanged(const char* body, uint32_t fetchDay) {
    if (!body) return;
    Preferences p;
    if (!p.begin(kNs, false)) return;
    // Ghi body trước fetchDay: mất điện giữa chừng thì body mới + ngày cũ ⇒ dòng 2 bị coi là
    // cũ (an toàn), không bao giờ body cũ + ngày mới (dòng 2 cũ bị áp cho hôm nay).
    if (!sameString(p, kSched, body)) p.putString(kSched, body);
    if (!p.isKey(kFetchDay) || p.getUInt(kFetchDay, 0) != fetchDay) p.putUInt(kFetchDay, fetchDay);
    p.end();
}

}  // namespace config
