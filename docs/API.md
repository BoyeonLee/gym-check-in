# API 명세

체육관 체크인 시스템의 백엔드 HTTP API 단일 출처. 라우팅·핸들러 구현은 `backend/CLAUDE.md`, 도메인 규칙은 `docs/PRD.md`/`backend/CLAUDE.md` 참고.

## 공통 규칙

### 베이스 URL
- 개발: `http://localhost:8080`
- 운영: `https://api.example.com` (HTTPS 강제)

### 인증
- 모든 `/api/*`는 별도 명시 없으면 `Authorization: Bearer <access JWT>` 헤더 필요.
- 미인증·만료·서명 불일치 → 401 `UNAUTHORIZED`.
- `must_change_password=true` 상태의 토큰은 `/api/admin/password`, `/api/admin/logout`, `/api/admin/refresh` 외 모든 라우트에서 403 `MUST_CHANGE_PASSWORD`.

### 응답 포맷
- 성공: 핸들러별 JSON.
- 에러: 통일 포맷.
  ```json
  { "error": { "code": "ERROR_CODE", "message": "사람이 읽을 수 있는 메시지" } }
  ```
- 422는 비즈니스 규칙 위반(예: 활성 회원권 없음), 400은 요청 형식·파라미터 오류, 401은 인증, 403은 권한, 404는 리소스 없음(존재하지 않거나 soft-deleted, 또는 다른 지점 리소스), 409는 충돌(중복·상태 불일치), 429는 rate limit, 500 `INTERNAL`은 panic·예기치 못한 오류(stack trace는 응답에 노출되지 않음).
- **다른 지점의 리소스에 지점 관리자가 접근 시 404로 응답**(403이 아닌 404 — 존재 노출 방지).
- 모든 `timestamptz` 응답은 KST 오프셋(`+09:00`) 형식으로 직렬화한다(예: `2026-04-27T18:23:00+09:00`).
- 모든 응답에 `X-Request-ID` 헤더가 포함된다(요청에서 받았으면 그 값, 없으면 서버가 UUIDv4 생성). 디버깅·문의 시 이 값을 함께 전달.

### 페이지네이션 (cursor 기반)
- 쿼리: `?cursor=<base64>&limit=<int>` (기본 20, 최대 100).
- cursor는 JSON `{"t": "<RFC3339>", "id": <bigint>}`을 base64로 인코딩한 opaque 문자열. 디코딩 실패 시 400 `INVALID_CURSOR`.
- 응답:
  ```json
  { "items": [...], "next_cursor": "eyJ0IjoiMjAyNi0wNC0xNVQxMDowMDowMFoiLCJpZCI6MTIzNH0=" }
  ```
- `next_cursor`가 `null`이면 마지막 페이지.
- `limit` 범위 밖이면 400 `INVALID_LIMIT`.
- `GET /api/check-ins?aggregate=daily`는 페이지네이션 없음. `from`~`to` 간격 최대 92일, 초과 시 400 `RANGE_TOO_LARGE`.

### Idempotency-Key
- 적용 엔드포인트(헤더 누락 시 400 `IDEMPOTENCY_KEY_REQUIRED`):
  - `POST /api/members/:id/memberships` (회원권 부여 + 결제 — 이중 클릭 시 결제 row 중복 방지)
  - `POST /api/memberships/:id/refund` (환불 — 음수 결제 row 중복 방지)
  - `POST /api/memberships/bulk-extend` (대량 연장)
- 헤더: `Idempotency-Key: <UUID>`.
- 같은 키로 24시간 안에 같은 body로 재호출하면 서버 처리 없이 첫 응답을 그대로 반환.
- 같은 키인데 body가 다르면 409 `IDEMPOTENCY_KEY_CONFLICT`.

### Rate limit
- IP 기준 15분당 60회(인증 라우트). 초과 시 429 `RATE_LIMITED`.
- 동일 계정 로그인 5회 연속 실패 시 15분 잠금. 잠금 동안 정확한 비번도 401 `ACCOUNT_LOCKED`.

---

## 인증·세션

### POST `/api/admin/login`
요청:
```json
{ "username": "owner", "password": "..." }
```
응답 200:
```json
{
  "access_token": "...",
  "refresh_token": "...",
  "expires_in": 1800,
  "must_change_password": true,
  "role": "global",
  "branch_id": null,
  "username": "owner"
}
```
에러: 401 `UNAUTHORIZED` (자격 불일치), 401 `ACCOUNT_LOCKED` (잠금 중), 401 `TEMP_PASSWORD_EXPIRED` (임시 비번 24h 만료 — 전역 관리자가 reset-password 재발급 필요), 429 `RATE_LIMITED`.

### POST `/api/admin/refresh`
요청:
```json
{ "refresh_token": "..." }
```
응답 200:
```json
{ "access_token": "...", "expires_in": 1800 }
```
에러: 401 `INVALID_REFRESH` (만료·서명 오류·무효화 목록 일치).

### POST `/api/admin/logout`
헤더: `Authorization: Bearer <access>` + body의 refresh.
요청:
```json
{ "refresh_token": "..." }
```
응답 204. refresh 토큰의 `jti`를 무효화 목록에 추가.

### POST `/api/admin/password`
요청:
```json
{ "current_password": "...", "new_password": "..." }
```
응답 204. 성공 시 `must_change_password=false` + `password_updated_at=now()` + `temp_password_expires_at=NULL` + 해당 사용자 모든 refresh 토큰 무효화.
에러: 401 `WRONG_CURRENT_PASSWORD`, 400 `WEAK_PASSWORD` (8자 미만 또는 영문/숫자 미혼합).

---

## 관리자 (전역 전용)

### GET `/api/admins`
응답 200:
```json
{ "items": [
  { "id": 1, "username": "owner", "role": "global", "branch_id": null, "must_change_password": false, "last_login_at": "..." }
], "next_cursor": null }
```

### POST `/api/admins`
요청:
```json
{ "username": "kim", "password": "...", "role": "branch", "branch_id": 2 }
```
응답 201: 생성된 관리자(비번·해시 제외). `must_change_password=true` + `temp_password_expires_at=now()+24h` 자동.
에러: 400 `WEAK_PASSWORD` (8자 미만 또는 영문/숫자 미혼합), 409 `USERNAME_DUPLICATE`, 400 `INVALID_ROLE_BRANCH` (role/branch_id 조합 불일치).

### PATCH `/api/admins/:id`
전역 전용. 변경 가능 필드: `username`, `role`, `branch_id`. 비밀번호·잠금 관련 컬럼은 본 엔드포인트로 변경 불가.
요청:
```json
{ "username?": "...", "role?": "branch", "branch_id?": 2 }
```
응답 200: 갱신된 관리자.
- `branch_id` 변경 시 해당 사용자의 refresh 토큰 모두 무효화(권한 변동 즉시 반영).
- 본인 계정의 `role`/`branch_id` 변경은 허용하지 않는다(자기 강등·이전 방지) → 409 `CANNOT_MODIFY_SELF_ROLE`.

에러: 409 `USERNAME_DUPLICATE`, 400 `INVALID_ROLE_BRANCH`, 409 `CANNOT_MODIFY_SELF_ROLE`.

### DELETE `/api/admins/:id`
응답 204 (soft delete). 호출자 본인이면 409 `CANNOT_DELETE_SELF`. 삭제 시 해당 사용자 refresh 토큰 모두 무효화.

### POST `/api/admins/:id/reset-password`
응답 200:
```json
{ "temporary_password": "Aa3-Kp9-Mn2-Xy", "expires_at": "2026-04-29T10:00:00+09:00" }
```
12자리 영숫자(헷갈리는 0/O/I/l/1, o/i 제외). 응답 1회만 노출. `must_change_password=true` + `temp_password_expires_at=now()+24h` + `failed_login_count=0` + `locked_until=NULL` 자동 세팅. 24시간이 지나면 로그인 시 401 `TEMP_PASSWORD_EXPIRED` — 전역 관리자가 본 엔드포인트로 재발급해야 한다.

---

## 지점

### GET `/api/branches`
응답 200:
```json
{ "items": [
  { "id": 1, "name": "강남점", "address": "서울 강남구 ..." }
] }
```
키오스크 초기화에서도 사용. 페이지네이션 없음(지점 수가 적음).

### POST `/api/branches` (전역)
요청: `{ "name": "...", "address": "..." }`. 응답 201.
에러: 409 `ADDRESS_DUPLICATE`.

### PATCH `/api/branches/:id` (전역)
요청: `{ "name?": "...", "address?": "..." }`. 응답 200.
에러: 409 `ADDRESS_DUPLICATE`.

### DELETE `/api/branches/:id` (전역)
응답 204 (soft delete).
에러: 409 `BRANCH_IN_USE` (활성 회원/관리자 존재).

---

## 회원

### GET `/api/members/search` (키오스크용)
쿼리: `q=<값>&branchId=<int>&mode=name|phone|memberId`.

매칭 규칙:
- `mode=name`: prefix 일치 (`name LIKE 'q%'`), 최소 2글자. 미만은 400 `QUERY_TOO_SHORT`.
- `mode=phone`: 4자리 정확 일치 (`phone_last4 = q`). 4자리 아니면 400 `INVALID_PHONE_QUERY`.
- `mode=memberId`: `id = q::bigint` 정확 일치. 숫자 아니면 400 `INVALID_MEMBER_ID`.

**활성 회원권(`status='active'`)이 있는 회원만** 반환. 결과 limit 20.

응답 200:
```json
{
  "items": [
    {
      "id": 1234,
      "name": "김민수",
      "phone_masked": "010-****-1234",
      "birth_md": "**-04-15",
      "member_id_display": "#1234"
    }
  ],
  "truncated": false
}
```
정렬: 해당 회원의 가장 최근 `check_ins.checked_in_at` DESC, NULL은 마지막. 결과가 20을 초과하면 `truncated: true`로 응답하고 클라이언트는 "회원 번호 또는 전화 4자리로 검색하세요" 안내를 표시한다.

### GET `/api/members` (관리자)
쿼리: `branchId?` (전역만 미지정 가능), `q?` (이름·전화 검색), cursor 페이지네이션.
응답 200:
```json
{ "items": [
  {
    "id": 1234, "name": "김민수", "phone": "01012345678",
    "birth_date": "1990-04-15", "branch_id": 1, "branch_name": "강남점",
    "active_membership": {
      "id": 99, "type": "monthly", "status": "active",
      "start_date": "2026-04-01", "end_date": "2026-05-01",
      "remaining": null
    }
  }
], "next_cursor": null }
```
관리자 화면이므로 phone·birth_date 풀 표시. 전역 관리자가 지점을 식별할 수 있도록 `branch_name`을 함께 내려준다.

### GET `/api/members/:id` (관리자)
회원 단건 상세. 관리자 회원 상세 페이지에서 한 번의 호출로 헤더·회원권 이력·결제 이력을 함께 그릴 수 있게 한다.
응답 200:
```json
{
  "member": {
    "id": 1234, "name": "김민수", "phone": "01012345678",
    "birth_date": "1990-04-15", "branch_id": 1, "branch_name": "강남점",
    "created_at": "2026-01-10T10:00:00+09:00"
  },
  "active_membership": {
    "id": 99, "type": "monthly", "status": "active",
    "start_date": "2026-04-01", "end_date": "2026-05-01",
    "remaining": null, "pause_used": false,
    "pause_start_date": null, "pause_end_date": null
  },
  "memberships": [
    { "id": 99, "type": "monthly", "status": "active",
      "start_date": "2026-04-01", "end_date": "2026-05-01",
      "remaining": null, "pause_used": false, "created_at": "..." }
  ],
  "payments": [
    { "id": 555, "membership_id": 99, "amount": 150000, "method": "card",
      "paid_at": "2026-04-01", "performed_by": 7, "memo": null, "created_at": "..." }
  ]
}
```
- `active_membership`: 현재 active/paused 회원권 1개(없으면 `null`).
- `memberships`: 회원의 모든 회원권을 `created_at DESC`로 최근 20개. 페이지네이션 없음. 더 많은 이력이 필요하면 향후 별도 엔드포인트.
- `payments`: 위 `memberships`에 연결된 결제(부여 양수 + 환불 음수). `paid_at DESC`.

지점 관리자가 다른 지점 회원 ID를 호출하면 404. 회원 soft-deleted도 404.
에러: 404.

### POST `/api/members` (관리자)
요청:
```json
{ "name": "...", "phone": "01012345678", "birth_date": "1990-04-15", "branch_id": 1 }
```
지점 관리자는 `branch_id`를 자기 지점으로 강제(요청에 다른 값 와도 무시·자기 지점으로 덮어씀).
응답 201.
에러: 409 `PHONE_DUPLICATE` (같은 지점 내 중복), 400 `INVALID_PHONE` (11자리 숫자 아님).

### PATCH `/api/members/:id` (관리자)
변경 가능 필드: **`name`, `phone`, `birth_date`만**. `branch_id`·기타 필드는 요청 body에 와도 무시. 회원이 다른 지점으로 이동하면 새 지점에 신규 등록.
요청:
```json
{ "name?": "...", "phone?": "01012345678", "birth_date?": "1990-04-15" }
```
응답 200.
에러: 400 `INVALID_PHONE`, 409 `PHONE_DUPLICATE`.

### DELETE `/api/members/:id` (관리자)
응답 204 (soft delete). 활성 회원권 있어도 삭제 가능. 매출·체크인 이력은 보존.

---

## 회원권

### GET `/api/memberships/:id` (관리자)
회원권 단건 상세. pause/unpause/cancel-pause/refund 폼이 현재 상태를 조회하기 위해 사용.
응답 200:
```json
{
  "membership": {
    "id": 99, "member_id": 1234, "branch_id": 1,
    "type": "monthly", "months": 1, "remaining": null,
    "status": "active",
    "start_date": "2026-04-01", "end_date": "2026-05-01",
    "pause_start_date": null, "pause_end_date": null, "pause_used": false,
    "created_at": "..."
  },
  "payments": [
    { "id": 555, "amount": 150000, "method": "card", "paid_at": "2026-04-01",
      "performed_by": 7, "memo": null, "created_at": "..." }
  ],
  "events": [
    { "id": 777, "action": "pause",
      "pause_start_date": "2026-04-10", "pause_end_date": "2026-04-15",
      "actual_pause_end": null, "extend_days": null,
      "reason": "여행", "performed_by": 7, "created_at": "..." }
  ]
}
```
- `payments`: 부여 양수 + 환불 음수 모두 `paid_at DESC`.
- `events`: `membership_events` 전체를 `created_at DESC`. action·관련 컬럼만 채워짐.

지점 관리자가 다른 지점 회원권을 호출하면 404. 회원이 soft-deleted여도 404.
에러: 404.

### POST `/api/members/:id/memberships`
헤더: `Idempotency-Key: <UUIDv4>` 필수.
요청 (monthly):
```json
{
  "type": "monthly",
  "months": 3,
  "start_date": "2026-04-01",
  "payment": { "amount": 150000, "method": "card" }
}
```
요청 (pass10):
```json
{
  "type": "pass10",
  "start_date": "2026-04-01",
  "payment": { "amount": 100000, "method": "cash" }
}
```
- `start_date >= 오늘`. 과거 날짜는 400 `INVALID_START_DATE`.
- `monthly`: `end_date = start_date + months month` 자동. `months >= 1`.
- `pass10`: `remaining=10`, `end_date = start_date + 2 month` 자동.
- `payment.amount`는 양수 정수(원 단위). 0/음수 거부.
- **`payment.paid_at`은 클라가 보내지 않는다** — 서버가 KST 오늘로 자동 설정(회원권 등록일 = 결제일). 미리 결제 시에도 `paid_at`은 오늘이고 `start_date`만 미래.
- **`payment.branch_id`도 클라가 보내지 않는다** — 서버가 회원의 `branch_id`로 자동.

**다음 회원권 미리 등록 허용**: 회원에 active/paused 회원권이 있어도 기간이 겹치지 않으면 새 회원권 등록 가능(예: 5/30 만료 + 6/1~ 시작 미리 등록). DB EXCLUDE 제약이 강제. 기간이 겹치면 409 `MEMBERSHIP_PERIOD_OVERLAP`.

응답 201: 생성된 회원권 + 결제(서버가 채운 `paid_at`/`branch_id` 포함).
에러: 400 `IDEMPOTENCY_KEY_REQUIRED`·`INVALID_IDEMPOTENCY_KEY`(UUID 형식 아님)·409 `IDEMPOTENCY_KEY_CONFLICT`, 409 `MEMBERSHIP_PERIOD_OVERLAP` (기간 겹침), 400 `INVALID_AMOUNT`, 400 `INVALID_MONTHS`, 400 `INVALID_START_DATE`, 404 (회원이 없거나 soft-deleted, 또는 다른 지점 회원).

### POST `/api/memberships/:id/pause`
요청:
```json
{ "start_date": "2026-04-01", "end_date": "2026-04-07", "reason": "여행" }
```
검증: `start_date <= end_date`, `start_date >= 오늘`, **`start_date >= memberships.start_date`**(미래 시작 회원권의 시작 전 정지 차단), `end_date <= 회원권 end_date`. 위반 시 400 `INVALID_PAUSE_RANGE`.
- `start_date <= 오늘`: 즉시 `paused`.
- `start_date > 오늘`: `active` 유지(자정 배치가 도래일에 paused로 전환).
- `end_date += (pause_end_date - pause_start_date)` (전체 기간 만큼 만료일 연장).
- `pause_used=true` (회원권당 1회 제한).
- **EXCLUDE 충돌**: 연장된 `end_date`가 같은 회원의 미래 회원권과 겹치면 PostgreSQL `23P01` → 409 `MEMBERSHIP_PERIOD_OVERLAP`(롤백).

응답 200: 갱신된 회원권.
에러: 409 `PAUSE_ALREADY_USED`, 400 `INVALID_PAUSE_RANGE`, 409 `MEMBERSHIP_PERIOD_OVERLAP`, 404.

### POST `/api/memberships/:id/unpause` (정지 도달 후 조기 활성화)
요청:
```json
{ "reason": "회원이 일찍 복귀 요청" }
```
조건: 현재 `status='paused'` (정지가 도달해 적용 중인 상태). `actual_pause_end = 오늘`로 잡고 `end_date -= (pause_end_date - actual_pause_end)`로 단축.
예: 4/1~4/7 정지·원 만료 5/30 → 4/6 호출 시 만료 5/29.

응답 200: 갱신된 회원권 객체.
에러: 409 `NOT_PAUSED` (active/expired/refunded 상태), 404.

### POST `/api/memberships/:id/cancel-pause` (미래 예약된 정지 취소)
요청:
```json
{ "reason": "회원이 정지 신청 철회" }
```
조건: `status='active'` 이면서 `pause_used=true` 이고 `pause_start_date > 오늘` (등록은 했지만 아직 도래 전). 그 외에는 409 `PAUSE_NOT_SCHEDULED`.

처리: 정지 등록 시 늘렸던 만큼 `end_date -= (pause_end_date - pause_start_date)`로 되돌림 + `pause_start_date/pause_end_date = NULL` + `pause_used=false`(다시 정지 등록 가능). `membership_events`(action='cancel_pause') 기록.

응답 200: 갱신된 회원권 객체.
에러: 409 `PAUSE_NOT_SCHEDULED`, 404.

### POST `/api/memberships/:id/refund`
헤더: `Idempotency-Key: <UUIDv4>` 필수.
요청:
```json
{ "reason": "..." }
```
환불 row(`payments` 음수)는 **서버가 자동으로 채운다** — 클라는 `reason`만 보낸다:
- `paid_at` = 서버 시각의 KST 오늘
- `method` = **원본 결제 row의 `method`와 동일**(cash 결제면 cash 환불, card 결제면 card 환불)
- `amount` = **원본 결제 row의 `amount`의 부호 반전**(원본 150000 → 환불 -150000). MVP는 전체 환불만 지원.

호출 가능 status:
- `active` (사용 중)
- `paused` (정지 중)
- `active` + `start_date > 오늘` (미래 시작 — 사전 결제 후 마음 바꿈)
- `expired` → 409 `MEMBERSHIP_ALREADY_EXPIRED`
- `refunded` → 409 (재환불 차단. 단 같은 Idempotency-Key 재호출은 첫 응답)

응답 200: `status='refunded'`로 갱신된 회원권 + 새로 추가된 음수 결제 row.

MVP는 전체 환불만 지원한다. 부분 환불(잔여 일수만 환불)은 Phase 5+.

에러: 400 `IDEMPOTENCY_KEY_REQUIRED`·`INVALID_IDEMPOTENCY_KEY`, 409 `IDEMPOTENCY_KEY_CONFLICT`, 409 `MEMBERSHIP_ALREADY_EXPIRED`, 404.

### POST `/api/memberships/bulk-extend` (전역)
헤더: `Idempotency-Key: <UUID>` 필수.
요청:
```json
{ "branch_id": 1, "type": "monthly", "days": 3, "reason": "연휴 보상" }
```
- `branch_id`/`type` 미지정 시 전체. `branch_id` 미지정 → 모든 지점, `type` 미지정 → monthly + pass10.
- `days`는 **양수 1~90**(음수·0·91 이상은 400 `INVALID_EXTEND_DAYS`). 단축은 미지원.
- 대상: `status IN ('active', 'paused')`인 memberships 모두. paused 회원권은 `end_date`와 `pause_end_date`를 같이 +days 이동(연휴 보상은 정지 중 회원에게도 적용).

처리:
- 모든 대상의 `end_date += days`
- `status='paused'` 또는 `status='active'` + 미래 예약 정지(`pause_used=true` AND `pause_start_date > 오늘`)인 회원권은 `pause_start_date += days`, `pause_end_date += days`도 함께 이동
- `membership_events`(action='bulk_extend') 기록

응답 200:
```json
{ "extended_count": 142 }
```

EXCLUDE 충돌 시 응답 409:
```json
{ "error": { "code": "MEMBERSHIP_PERIOD_OVERLAP", "message": "..." }, "first_conflict_membership_id": 99 }
```
연장 결과가 같은 회원의 미래 회원권과 겹치면 전체 트랜잭션 롤백 + `extended_count` 응답 없음. 운영자는 `first_conflict_membership_id`로 충돌 회원권을 찾아 `start_date` 조정 후 새 Idempotency-Key로 재시도.

에러: 400 `IDEMPOTENCY_KEY_REQUIRED`·`INVALID_IDEMPOTENCY_KEY`, 409 `IDEMPOTENCY_KEY_CONFLICT` (같은 키인데 body가 다름), 400 `INVALID_EXTEND_DAYS`, 409 `MEMBERSHIP_PERIOD_OVERLAP`.

---

## 체크인

### POST `/api/check-ins`
요청:
```json
{ "member_id": 1234, "branch_id": 1 }
```
처리:
1. **이중 클릭 방지**: `(member_id, branch_id)`로 직전 5초 안에 성공한 체크인이 있으면 그 응답을 그대로 재반환(서버 메모리 LRU, TTL 5초).
2. 활성 회원권 잠금: `WHERE member_id=? AND branch_id=? AND status='active' AND start_date <= 오늘 AND end_date >= 오늘 FOR UPDATE`.
3. 없으면 422 — 사유 분기:
   - status가 active가 아니거나 회원권이 없음 → `NO_ACTIVE_MEMBERSHIP`
   - active이지만 `start_date > 오늘`(아직 시작 전) → `MEMBERSHIP_NOT_STARTED`
4. `check_ins` 삽입(`membership_id` NOT NULL이므로 잠긴 row의 id 사용).
5. `pass10`이면 같은 회원·날짜·지점 기존 row 없을 때만 `remaining -= 1`. `remaining=0`이면 같은 트랜잭션에서 `status='expired'`.

응답 201:
```json
{
  "id": 5678,
  "checked_in_at": "2026-04-27T18:23:00+09:00",
  "membership": { "type": "pass10", "remaining": 7, "end_date": "2026-06-01" }
}
```

### GET `/api/check-ins` (관리자)
쿼리: `from`, `to` (ISO 날짜), `branchId?` (전역만 미지정 가능), `aggregate=raw|daily` (기본 raw).
- `raw`: `check_ins` row 그대로. cursor 페이지네이션 적용.
- `daily`: `(member_id, checked_in_at::date)` DISTINCT 1회 집계. 페이지네이션 없음. `(from, to)` 간격 최대 92일 — 초과 시 400 `RANGE_TOO_LARGE`.

응답 200 (raw):
```json
{ "items": [
  { "id": 5678, "member_id": 1234, "member_name": "김민수",
    "branch_id": 1, "branch_name": "강남점",
    "membership_id": 99, "checked_in_at": "..." }
], "next_cursor": null }
```

응답 200 (daily):
```json
{ "items": [
  { "member_id": 1234, "member_name": "김민수",
    "branch_id": 1, "branch_name": "강남점",
    "date": "2026-04-15", "checkin_count": 2,
    "first_checked_in_at": "2026-04-15T09:00:00+09:00" }
] }
```
에러: 400 `INVALID_AGGREGATE` (`raw|daily` 외 값), 400 `RANGE_TOO_LARGE`.

### GET `/api/check-ins/today-count` (키오스크용)
쿼리: `branchId=<int>`.
응답 200: `{ "count": 42 }`.
KST 기준 오늘 해당 지점 체크인 row 수: `WHERE branch_id = ? AND (checked_in_at AT TIME ZONE 'Asia/Seoul')::date = (now() AT TIME ZONE 'Asia/Seoul')::date`. 같은 회원이 두 번 체크인하면 2로 카운트(raw 기준 — 키오스크 헤더는 "오늘 입장 횟수").

---

## 매출 (전역 전용)

### GET `/api/sales/summary`
쿼리: `from`, `to` (ISO 날짜), `branchId?`.
응답 200:
```json
{
  "gross_total": 13000000,
  "refund_total": 500000,
  "net_total": 12500000,
  "by_method": {
    "cash": { "gross": 4700000, "refund": 200000, "net": 4500000 },
    "card": { "gross": 8300000, "refund": 300000, "net": 8000000 }
  },
  "by_day": [
    { "date": "2026-04-01", "gross": 250000, "refund": 50000, "net": 200000,
      "cash": { "gross": 50000, "refund": 0, "net": 50000 },
      "card": { "gross": 200000, "refund": 50000, "net": 150000 } }
  ]
}
```
`payments.paid_at` 기준. `gross_total`은 양수 row 합, `refund_total`은 음수 row의 절대값 합, `net_total = gross - refund`.

---

## 에러 코드 카탈로그

| 코드 | HTTP | 의미 |
|------|------|-----|
| `UNAUTHORIZED` | 401 | 인증 실패(자격 불일치·토큰 만료/무효·계정 soft-deleted·비번 변경 후 stale access) |
| `ACCOUNT_LOCKED` | 401 | 비번 5회 연속 실패로 잠금. 응답 body에 `unlock_at` |
| `TEMP_PASSWORD_EXPIRED` | 401 | 임시 비번 24h 만료 — 전역 관리자가 reset-password 재발급 필요 |
| `INVALID_REFRESH` | 401 | refresh 토큰 만료·서명 오류·무효화됨 |
| `WRONG_CURRENT_PASSWORD` | 401 | 비번 변경 시 현재 비번 불일치 |
| `MUST_CHANGE_PASSWORD` | 403 | 강제 변경 화면 외 라우트 차단 |
| `FORBIDDEN` | 403 | 권한 없음(예: branch가 global 라우트 호출) |
| `RATE_LIMITED` | 429 | IP 단위 rate limit 초과 |
| `WEAK_PASSWORD` | 400 | 비번 8자 미만 또는 영문/숫자 미혼합 |
| `INVALID_PHONE` | 400 | 전화번호가 11자리 숫자 아님 |
| `INVALID_PHONE_QUERY` | 400 | 키오스크 전화 검색이 4자리 아님 |
| `INVALID_MEMBER_ID` | 400 | 회원 번호 검색이 숫자 아님 |
| `QUERY_TOO_SHORT` | 400 | 이름 검색이 2자 미만 |
| `INVALID_AGGREGATE` | 400 | aggregate 파라미터 잘못된 값 |
| `INVALID_LIMIT` | 400 | limit 1~100 범위 밖 |
| `INVALID_CURSOR` | 400 | cursor 디코딩 실패·필드 누락·타입 오류 |
| `RANGE_TOO_LARGE` | 400 | aggregate=daily 또는 매출의 from~to 간격이 92일 초과 |
| `INVALID_AMOUNT` | 400 | 결제 금액이 양수가 아님 |
| `INVALID_MONTHS` | 400 | monthly의 months가 1 미만 |
| `INVALID_START_DATE` | 400 | 회원권 부여 start_date가 과거 |
| `INVALID_EXTEND_DAYS` | 400 | bulk-extend의 days가 1~90 범위 밖 |
| `INVALID_PAUSE_RANGE` | 400 | 정지 시작/종료 잘못됨 |
| `INVALID_ROLE_BRANCH` | 400 | role/branch_id 조합 불일치 |
| `IDEMPOTENCY_KEY_REQUIRED` | 400 | 회원권 부여·환불·bulk-extend에 헤더 없음 |
| `INVALID_IDEMPOTENCY_KEY` | 400 | Idempotency-Key가 UUIDv4 형식 아님 |
| `IDEMPOTENCY_KEY_CONFLICT` | 409 | 같은 키인데 body가 다름 |
| `PHONE_DUPLICATE` | 409 | 같은 지점 내 동일 phone 중복 |
| `USERNAME_DUPLICATE` | 409 | 관리자 username 중복 |
| `ADDRESS_DUPLICATE` | 409 | 지점 주소 중복 |
| `BRANCH_IN_USE` | 409 | 지점에 활성 회원/관리자 존재해 삭제 불가 |
| `CANNOT_DELETE_SELF` | 409 | 본인 관리자 계정 삭제 시도 |
| `CANNOT_MODIFY_SELF_ROLE` | 409 | 본인 계정의 role/branch_id 변경 시도 |
| `MEMBERSHIP_PERIOD_OVERLAP` | 409 | 회원권 부여·정지·bulk-extend 결과가 기존 active/paused 회원권과 기간 겹침 |
| `MEMBERSHIP_ALREADY_EXPIRED` | 409 | expired 회원권에 환불 시도 (refunded는 IDEMPOTENCY_KEY_CONFLICT 또는 일반 409) |
| `PAUSE_ALREADY_USED` | 409 | 회원권당 정지 1회 제한 위반 |
| `NOT_PAUSED` | 409 | unpause 호출했는데 paused 아님 |
| `PAUSE_NOT_SCHEDULED` | 409 | cancel-pause 호출했는데 미래 예약 정지 상태 아님 |
| `NO_ACTIVE_MEMBERSHIP` | 422 | 체크인했는데 active 회원권 없음 |
| `MEMBERSHIP_NOT_STARTED` | 422 | active이지만 `start_date > 오늘` (시작 전) |
| `INTERNAL` | 500 | 예기치 못한 서버 오류(panic 등). stack trace는 응답에 노출되지 않음 |
