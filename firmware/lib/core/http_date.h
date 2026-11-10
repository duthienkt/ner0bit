#pragma once
#include <stdint.h>

// Parse header HTTP `Date` dạng IMF-fixdate (RFC 7231 §7.1.1.1), vd.
// "Sun, 06 Nov 1994 08:49:37 GMT" -> Unix epoch UTC (giây).
// Chặt: đúng 29 ký tự, tên thứ/tháng tiếng Anh đúng hoa-thường, "GMT"; không nhận
// định dạng cũ (RFC 850, asctime). Thứ trong tuần chỉ cần là tên hợp lệ, không đối chiếu ngày.
// Giây = 60 (leap second) được nhận như RFC cho phép. false nếu sai định dạng, trường ngoài
// khoảng (ngày không có trong tháng, giờ ≥ 24…) hoặc ngoài khoảng uint32_t (trước 1970, từ 2106).
bool parseHttpDate(const char* s, uint32_t& utc);
