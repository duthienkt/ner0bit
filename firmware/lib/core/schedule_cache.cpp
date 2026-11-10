#include "schedule_cache.h"

#include <string.h>

namespace {

constexpr uint16_t kBodyAddr = kScheduleCacheAddr + kScheduleCacheHeaderLen;
constexpr uint16_t kCompareChunk = 32;

void put16(uint8_t* p, uint16_t v) {
    p[0] = (uint8_t)(v & 0xFF);
    p[1] = (uint8_t)(v >> 8);
}

void put32(uint8_t* p, uint32_t v) {
    p[0] = (uint8_t)(v & 0xFF);
    p[1] = (uint8_t)((v >> 8) & 0xFF);
    p[2] = (uint8_t)((v >> 16) & 0xFF);
    p[3] = (uint8_t)(v >> 24);
}

uint16_t get16(const uint8_t* p) { return (uint16_t)(p[0] | ((uint16_t)p[1] << 8)); }

uint32_t get32(const uint8_t* p) {
    return (uint32_t)p[0] | ((uint32_t)p[1] << 8) | ((uint32_t)p[2] << 16) | ((uint32_t)p[3] << 24);
}

struct Header {
    uint16_t magic;
    uint16_t len;
    uint32_t fetchDay;
    uint32_t crc;
};

bool readHeader(IStorage& s, Header& h) {
    uint8_t b[kScheduleCacheHeaderLen];
    if (!s.read(kScheduleCacheAddr, b, sizeof b)) return false;
    h.magic = get16(b);
    h.len = get16(b + 2);
    h.fetchDay = get32(b + 4);
    h.crc = get32(b + 8);
    return true;
}

uint32_t cacheCrc(uint16_t len, uint32_t fetchDay, const uint8_t* body) {
    uint8_t meta[6];
    put16(meta, len);
    put32(meta + 2, fetchDay);
    return crc32Update(crc32Update(0, meta, sizeof meta), body, len);
}

// true nếu body đã lưu (len byte) giống hệt body. Đọc lỗi ⇒ false (coi như khác, ghi lại).
bool sameBody(IStorage& s, const uint8_t* body, uint16_t len) {
    uint8_t buf[kCompareChunk];
    for (uint16_t off = 0; off < len; off = (uint16_t)(off + kCompareChunk)) {
        const uint16_t n = (uint16_t)(len - off < kCompareChunk ? len - off : kCompareChunk);
        if (!s.read((uint16_t)(kBodyAddr + off), buf, n)) return false;
        if (memcmp(buf, body + off, n) != 0) return false;
    }
    return true;
}

}  // namespace

uint32_t crc32Update(uint32_t crc, const uint8_t* data, size_t len) {
    uint32_t c = ~crc;
    for (size_t i = 0; i < len; i++) {
        c ^= data[i];
        for (int k = 0; k < 8; k++) c = (c >> 1) ^ (0xEDB88320u & (0u - (c & 1u)));
    }
    return ~c;
}

bool scheduleCacheLoad(IStorage& s, char* body, size_t cap, uint32_t& fetchDay) {
    if (!body || cap == 0) return false;
    body[0] = '\0';
    Header h;
    if (!readHeader(s, h)) return false;
    if (h.magic != kScheduleCacheMagic || h.len == 0 || h.len > kScheduleCacheMaxLen) return false;
    if ((size_t)h.len + 1 > cap) return false;
    if (!s.read(kBodyAddr, (uint8_t*)body, h.len)) {
        body[0] = '\0';
        return false;
    }
    const bool hasNul = memchr(body, '\0', h.len) != nullptr;
    if (hasNul || cacheCrc(h.len, h.fetchDay, (const uint8_t*)body) != h.crc) {
        body[0] = '\0';
        return false;
    }
    body[h.len] = '\0';
    fetchDay = h.fetchDay;
    return true;
}

CacheWrite scheduleCacheSave(IStorage& s, const char* body, uint32_t fetchDay) {
    if (!body) return CacheWrite::Invalid;
    const size_t n = strlen(body);
    if (n == 0 || n > kScheduleCacheMaxLen) return CacheWrite::Invalid;
    const uint16_t len = (uint16_t)n;
    const uint8_t* b = (const uint8_t*)body;
    const uint32_t crc = cacheCrc(len, fetchDay, b);

    Header old;
    const bool oldOk = readHeader(s, old) && old.magic == kScheduleCacheMagic && old.len == len;
    // Header cũ khớp độ dài thì mới đáng so body (đọc ~len byte qua I2C).
    const bool bodySame = oldOk && sameBody(s, b, len);
    if (bodySame && old.fetchDay == fetchDay && old.crc == crc) return CacheWrite::Unchanged;

    if (!bodySame && !s.write(kBodyAddr, b, len)) return CacheWrite::Failed;
    uint8_t h[kScheduleCacheHeaderLen];
    put16(h, kScheduleCacheMagic);
    put16(h + 2, len);
    put32(h + 4, fetchDay);
    put32(h + 8, crc);
    if (!s.write(kScheduleCacheAddr, h, sizeof h)) return CacheWrite::Failed;
    return CacheWrite::Written;
}
