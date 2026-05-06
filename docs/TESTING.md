# 테스트 전략

API 구현은 **TDD(Test-Driven Development)**로 진행한다. 각 핸들러·도메인 함수는 실패 테스트 먼저 작성한 뒤 최소 구현으로 통과시키고 리팩토링한다. 본 문서는 워크플로우·테스트 계층·에러/엣지 케이스 카탈로그·에러 핸들링 패턴을 정리한다.

스택: `go test` + `testify` + 실제 PostgreSQL(테스트 DB) + Gin `httptest`. 모킹 라이브러리는 도입하지 않는다.

---

## TDD 워크플로우

각 작업 단위(엔드포인트 1개·도메인 함수 1개)는 다음 사이클을 따른다.

1. **Red** — 실패 테스트 작성. 컴파일 에러도 Red. 여러 테스트 케이스(정상·에러·엣지)를 한 번에 적되, 가장 간단한 정상 케이스부터 작성.
2. **Green** — 테스트가 통과할 **최소 구현**. 일반화·예쁜 코드는 미루고 단순 if/else로 시작 가능.
3. **Refactor** — 통과 상태를 유지하며 중복 제거·이름 정리. 테스트는 매번 통과해야 함.
4. 다음 케이스로 이동(에러 케이스 → 엣지 케이스 순).

원칙:
- **테스트 없는 코드는 머지 금지.** PR마다 변경된 핸들러의 테스트가 함께 추가되어야 한다.
- **에러 케이스가 정상 케이스보다 많다.** 정상 1개당 에러/엣지 N개. 본 문서의 카탈로그를 체크리스트로 사용.
- **테스트는 결정적이어야 한다.** `time.Now()`·랜덤·외부 IO에 의존하지 않게 시간/UUID는 주입(`Clock`/`UUIDGen` 인터페이스).
- **느린 테스트는 분리한다.** `go test -short`로 단위만 실행, `go test ./...`로 통합 포함.

---

## 테스트 계층

### 1. 단위 테스트 (`internal/domain`, `internal/auth`, `internal/util`)
순수 함수·도메인 규칙에 대한 빠른 테스트. 외부 의존성 없음.
- 회원권 만료일 계산(monthly +N month / pass10 +2 month)
- 비번 강도 검증(8자 + 영문 + 숫자)
- cursor 인코딩/디코딩 round-trip
- KST 날짜 계산(자정 경계)
- 임시 비번 생성기(charset·길이·랜덤성)
- JWT 발급/검증(claim 필드)
- 멱등성 키 검증(UUID 형식)

테이블 기반 테스트 우선 (`tests := []struct{ name string; in...; want... }`).

### 2. 리포지토리 테스트 (`internal/repo`)
실제 PostgreSQL 테스트 DB에 goose로 스키마 적용한 뒤 실행. **모킹 금지**(이미 backend/CLAUDE.md에 명시).
- 각 repo 메서드의 행동(insert/update/select)
- DB 제약 위반(unique·CHECK·EXCLUDE·FK) → 에러 반환
- 트랜잭션 내 행동(잠금·롤백)
- 인덱스 사용 여부는 테스트하지 않음(통합 테스트 범주 밖)

격리 전략: 각 테스트는 트랜잭션 내에서 실행하고 마지막에 `ROLLBACK`. 또는 매 테스트 전 `TRUNCATE TABLE ... RESTART IDENTITY CASCADE`. 후자가 단순.

### 3. 핸들러 테스트 (`internal/http`)
Gin `httptest`로 라우터·미들웨어·repo까지 실제로 통과하는 end-to-end 테스트.
- 인증 미들웨어 통과 → 핸들러 → DB 반영 → 응답 검증
- 에러 응답 포맷(`{"error":{"code":"...","message":"..."}}`) 일관성
- 응답 헤더(`X-Request-ID`, CORS) 검증

매 테스트는 새 admin 토큰 + 깨끗한 DB 상태에서 시작.

---

## 테스트 인프라

### 테스트 DB
- 별도 데이터베이스 `gym_test`. 운영/개발 DB와 격리.
- `.env.example`의 `TEST_DATABASE_URL` 환경변수 사용.
- `docker-compose.yml`에 별도 컨테이너 없이, 같은 Postgres 컨테이너에 두 번째 DB 생성:
  ```sql
  CREATE DATABASE gym_test;
  ```
- 각 테스트 패키지의 `TestMain`에서 goose up → 테스트 → 종료 시 cleanup.

### 헬퍼 패키지 (`internal/testutil`)
fixture 생성·정리 유틸. 모든 테스트에서 재사용.
- `SetupDB(t)` — 테스트 DB 연결 + 마이그레이션 적용 보장 (한 번만)
- `TruncateAll(t, db)` — 매 테스트 전 모든 도메인 테이블 비우기 (시드 admin/branch는 유지)
- `CreateAdmin(t, db, opts)` — `{ role, branch_id, must_change_password, ... }` 옵션
- `CreateBranch(t, db, opts)`
- `CreateMember(t, db, branchID, opts)`
- `CreateMembership(t, db, memberID, opts)` — type, start_date, status 등 자유 설정
- `Login(t, server, username, password) → (accessToken, refreshToken)`
- `AuthRequest(t, server, method, path, body, token) → *http.Response`
- `FreezeTime(t, instant)` — `Clock` 인터페이스 주입으로 시간 고정
- `RandomIdempotencyKey()` — UUIDv4

### CI에서 실행
- `go test -race ./...`로 race detector 동시 실행.
- 통합 테스트가 포함된 패키지는 `//go:build integration` 빌드 태그로 분리해 단위만 빠르게 돌릴 수 있게.
- 커버리지 목표: 핸들러 80%, 도메인 90%(기준선이지 절대 기준 아님).

---

## 에러 케이스 카탈로그 (모든 인증 엔드포인트 공통)

각 핸들러 테스트에 **이 목록의 해당 케이스를 빠짐없이 작성**한다.

### 인증·세션
- [ ] 토큰 없음 → 401 `UNAUTHORIZED`
- [ ] 토큰 만료 → 401 `UNAUTHORIZED`
- [ ] 토큰 서명 무효 → 401 `UNAUTHORIZED`
- [ ] refresh 토큰을 access 자리에 사용 → 401 `UNAUTHORIZED` (claim audience 불일치)
- [ ] `revoked_refresh_tokens`에 등재된 jti로 refresh 시도 → 401 `INVALID_REFRESH`
- [ ] `must_change_password=true` 토큰으로 차단 라우트 호출 → 403 `MUST_CHANGE_PASSWORD`
- [ ] 계정 soft delete된 admin의 access 토큰으로 호출 → **401 즉시** (Auth 미들웨어가 admin row 존재 확인, 30분 자연 만료 대기 안 함)
- [ ] access claim 필수 필드(`sub`/`role`/`iat`/`exp`) 누락된 토큰 → 401 `UNAUTHORIZED`
- [ ] access claim의 `role`이 `'global'`/`'branch'` 외 값 → 401 `UNAUTHORIZED`
- [ ] `temp_password_expires_at < now()`로 로그인 시도 → 401 `TEMP_PASSWORD_EXPIRED`
- [ ] 비번 5회 연속 실패 → 401 `ACCOUNT_LOCKED` 응답 + `unlock_at`. 6번째는 정확한 비번이어도 잠금 동안 거부
- [ ] 잠금 시간 경과 후 정확한 비번 → 성공 + `failed_login_count=0`
- [ ] 잠금 시간 경과 후 틀린 비번 → 401, 카운터 1부터 다시 누적

### 권한 (multi-tenancy)
- [ ] `role='branch'` 토큰으로 `/api/sales/*` 호출 → 403 `FORBIDDEN`
- [ ] `role='branch'` 토큰으로 `/api/admins/*` 호출 → 403
- [ ] `role='branch'` 토큰으로 `/api/memberships/bulk-extend` 호출 → 403
- [ ] `role='branch'` 토큰으로 `/api/branches POST/PATCH/DELETE` → 403
- [ ] `role='branch'` 토큰으로 **다른 지점 회원** GET/PATCH/DELETE → **404** (403이 아닌 404 — 존재 노출 방지)
- [ ] `role='branch'` 토큰으로 **다른 지점 회원권** PATCH(pause/unpause/cancel-pause/refund) → **404**
- [ ] `role='branch'` 토큰으로 회원 등록 시 다른 지점 `branch_id` 지정 → 핸들러가 자기 지점으로 강제
- [ ] 존재하지 않는 `:id` → **404** (소속 지점 무관)
- [ ] soft-deleted 리소스 `:id` → **404**

### 입력 검증 (공통)
- [ ] 필수 필드 누락 → 400 + 검증 메시지
- [ ] JSON 파싱 불가 (잘못된 본문) → 400
- [ ] body 1MB 초과 → 400
- [ ] 잘못된 타입 (예: 숫자 자리에 문자열) → 400
- [ ] 알 수 없는 필드는 무시 (엄격 검증 안 함)

### 멱등성 (`POST /api/members/:id/memberships`, `/refund`, `/bulk-extend`)
- [ ] `Idempotency-Key` 헤더 누락 → 400 `IDEMPOTENCY_KEY_REQUIRED`
- [ ] `Idempotency-Key` 값이 UUIDv4 형식 아님(빈 문자열·임의 string) → 400 `INVALID_IDEMPOTENCY_KEY`
- [ ] 같은 키 + 같은 body 재호출 → 첫 응답 그대로 (DB 변동 없음)
- [ ] 같은 키 + 다른 body 재호출 → 409 `IDEMPOTENCY_KEY_CONFLICT`
- [ ] 24시간 지난 키 재호출 → 신규 처리(첫 응답이 아닌 새 응답)

### 페이지네이션 (`GET /api/members`, `/api/check-ins?aggregate=raw`)
- [ ] limit 0 또는 -1 → 400 `INVALID_LIMIT`
- [ ] limit 101 → 400 `INVALID_LIMIT`
- [ ] limit 미지정 → 기본 20
- [ ] cursor 무효(base64 아님, JSON 아님, 필드 누락) → 400 `INVALID_CURSOR`
- [ ] 정확히 limit개 결과 → `next_cursor` 채워짐
- [ ] limit + 1개 결과의 마지막 페이지 → `next_cursor: null`
- [ ] 빈 결과 → `items: []`, `next_cursor: null`
- [ ] 마지막 페이지 cursor로 다시 호출 → 빈 결과

### Rate limit
- [ ] 같은 IP에서 15분당 60회 초과 → 429 `RATE_LIMITED`
- [ ] 잠금 + rate limit 상호 작용 (rate limit이 먼저 트리거되면 잠금 카운터는 안 오름)

### 응답 공통
- [ ] 모든 응답에 `X-Request-ID` 헤더 포함
- [ ] 클라가 `X-Request-ID` 보낸 경우 그 값 그대로 사용
- [ ] 모든 timestamptz 필드가 `+09:00` 오프셋
- [ ] panic 발생 시 500 `INTERNAL`만 응답, stack 노출 없음

### CORS
- [ ] `OPTIONS` preflight → 200 + 허용 헤더
- [ ] 다른 origin에서 호출 → CORS 헤더가 `CORS_ORIGIN`만 허용
- [ ] 미들웨어가 `Authorization`, `Idempotency-Key`, `X-Request-ID` 허용

---

## 도메인 별 에러·엣지 케이스

### 회원 (`POST/PATCH/DELETE /api/members`, `/api/members/search`)
- [ ] phone 11자리 아님 → 400 `INVALID_PHONE`
- [ ] phone 숫자 아닌 문자 포함 → 400
- [ ] name 빈 문자열·101자 → 400
- [ ] birth_date 누락 → 400
- [ ] 같은 지점에 같은 phone으로 두 번째 등록 → 409 `PHONE_DUPLICATE`
- [ ] 같은 phone으로 다른 지점에 등록 → 성공 (지점별 unique)
- [ ] soft-deleted 회원의 phone으로 같은 지점에 새 회원 등록 → 성공 (부분 unique가 deleted_at IS NULL만 체크)
- [ ] PATCH로 `branch_id` 변경 시도 → 무시 (응답에는 기존 branch_id)
- [ ] PATCH로 알 수 없는 필드 전달 → 무시
- [ ] DELETE 후 `GET /api/members?q=...` 결과에 미포함
- [ ] DELETE된 회원의 체크인 이력은 그대로 조회됨

#### `/api/members/search` (키오스크)
- [ ] `mode=name` 1자 → 400 `QUERY_TOO_SHORT`
- [ ] `mode=name` 2자 → 검색 실행
- [ ] `mode=phone` 3자리 → 400 `INVALID_PHONE_QUERY`
- [ ] `mode=phone` 5자리 → 400
- [ ] `mode=memberId` 숫자 아님 → 400 `INVALID_MEMBER_ID`
- [ ] 결과 0건 → `items: []`, `truncated: false`
- [ ] 결과 20건 → `truncated: false`
- [ ] 결과 21건 → 상위 20개 반환, `truncated: true`
- [ ] 활성 회원권 없는 회원은 결과에 미포함 (status가 expired/refunded/paused/없음 모두)
- [ ] 활성 회원권 있지만 `start_date > 오늘`인 회원도 결과에서 제외
- [ ] 결과 정렬: 최근 체크인 DESC, NULL은 마지막
- [ ] 응답 필드 마스킹 검증: `phone_masked`, `birth_md`, `member_id_display`만 노출

### 회원권 부여 (`POST /api/members/:id/memberships`)
- [ ] amount 0 → 400 `INVALID_AMOUNT`
- [ ] amount 음수 → 400
- [ ] monthly 인데 months 누락 → 400 `INVALID_MONTHS`
- [ ] monthly months 0/-1 → 400
- [ ] pass10 인데 months 지정 → 무시 (서버가 무시)
- [ ] start_date 어제 → 400 `INVALID_START_DATE`
- [ ] start_date 오늘 → 성공
- [ ] start_date 1년 후 → 성공 (먼 미래도 허용)
- [ ] **클라가 `payment.paid_at`을 보내도 무시** — 응답의 `paid_at`은 항상 서버 시각의 KST 오늘
- [ ] **클라가 `payment.branch_id`를 보내도 무시** — 응답의 `branch_id`는 회원의 `branch_id`로 자동
- [ ] method가 `cash`/`card` 외 → 400
- [ ] member.deleted_at IS NOT NULL인 회원에 부여 시도 → 404
- [ ] 다른 지점 회원에 부여 시도(지점 관리자) → 404
- [ ] 같은 회원에 기간 겹치는 active/paused 회원권 존재 → 409 `MEMBERSHIP_PERIOD_OVERLAP`
- [ ] 같은 회원이지만 기간이 안 겹치는 미래 회원권 등록 → 성공
- [ ] 트랜잭션 내 `payments` row도 같이 INSERT 검증
- [ ] 트랜잭션 실패(예: payments INSERT 실패) → memberships도 롤백
- [ ] 동시에 같은 회원에 겹치는 회원권 두 개 INSERT → EXCLUDE가 한 쪽 거부 (race 보호)

### 회원권 정지 (`/pause`, `/unpause`, `/cancel-pause`)
#### pause
- [ ] `pause_used=true` 회원권에 호출 → 409 `PAUSE_ALREADY_USED`
- [ ] start_date > end_date → 400 `INVALID_PAUSE_RANGE`
- [ ] start_date 어제 → 400
- [ ] start_date < memberships.start_date (미래 시작 회원권의 시작 전 정지) → 400 `INVALID_PAUSE_RANGE`
- [ ] end_date > 회원권 end_date → 400
- [ ] start_date = 오늘 → status='paused' 즉시 전환
- [ ] start_date = 내일 → status='active' 유지, pause_* 세팅
- [ ] end_date 연장 정확성 (`end_date += pause_end - pause_start`)
- [ ] **연장된 end_date가 같은 회원의 미래 회원권과 기간 겹침** → 409 `MEMBERSHIP_PERIOD_OVERLAP`(롤백)
- [ ] `membership_events`에 action='pause' 기록

#### unpause
- [ ] status가 active/expired/refunded → 409 `NOT_PAUSED`
- [ ] 정확히 `actual_pause_end = 오늘`로 단축 (예제 4/1~4/7, 4/6 호출 → end_date -1)
- [ ] pause_*는 NULL로 리셋
- [ ] `pause_used`는 그대로(재정지 불가)
- [ ] `membership_events`에 action='unpause' + `actual_pause_end` 기록

#### cancel-pause
- [ ] `status='paused'` → 409 `PAUSE_NOT_SCHEDULED`
- [ ] `pause_used=false` → 409
- [ ] `pause_start_date <= 오늘` → 409 (이미 도래)
- [ ] 정상 케이스: end_date 복원, pause_* NULL, `pause_used=false` (재정지 가능)
- [ ] `membership_events`에 action='cancel_pause' 기록

### 환불 (`/refund`)
- [ ] active 회원권 → 성공
- [ ] paused 회원권 → 성공
- [ ] active + `start_date > 오늘` (미래 시작) → 성공
- [ ] expired 회원권 → 409 `MEMBERSHIP_ALREADY_EXPIRED`
- [ ] 이미 refunded → 409 (재환불 차단). 단 같은 Idempotency-Key면 첫 응답 반환
- [ ] payments 음수 row의 `amount`가 원래 결제 합과 일치 (전체 환불)
- [ ] **환불 row의 `paid_at`은 항상 서버 KST 오늘** (클라가 `requested_at` 보내도 무시)
- [ ] 응답에 갱신된 `status='refunded'` 회원권 + 새 음수 결제 row 모두 포함

### 대량 연장 (`/bulk-extend`)
- [ ] days 0/-1/91 → 400 `INVALID_EXTEND_DAYS`
- [ ] branch_id/type 미지정 → 모든 지점·모든 type 대상
- [ ] active + paused 모두 `end_date += days`
- [ ] paused는 `pause_start_date`/`pause_end_date`도 같이 +days 이동
- [ ] active + 미래 예약 정지(`pause_used=true` AND `pause_start_date > 오늘`)도 `pause_*` 같이 +days 이동
- [ ] active + 정지 안 함(pause_used=false)은 end_date만 +days, pause_* 그대로(NULL)
- [ ] expired/refunded 회원권은 대상 제외(건드리지 않음)
- [ ] 연장 결과가 미래 회원권과 기간 겹침 → 409 `MEMBERSHIP_PERIOD_OVERLAP`, 전체 롤백, 응답 body에 `first_conflict_membership_id` 포함, `extended_count` 응답 없음
- [ ] `membership_events`에 action='bulk_extend' + `extend_days` 기록 (대상 row 수만큼)
- [ ] `idempotency_keys`에 응답 저장
- [ ] 같은 키 재호출 → 처리 없이 첫 응답

### 체크인 (`POST /api/check-ins`)
- [ ] 활성 회원권 없음 → 422 `NO_ACTIVE_MEMBERSHIP`
- [ ] 활성 회원권은 있지만 `start_date > 오늘` → 422 `MEMBERSHIP_NOT_STARTED`
- [ ] 활성 회원권은 있지만 `end_date < 오늘`(자정 배치 전 race) → 422 `NO_ACTIVE_MEMBERSHIP`
- [ ] 정지 중 → 422
- [ ] 횟수권 첫 체크인 → row 1, remaining -= 1
- [ ] 횟수권 같은 날 두 번째 체크인 → row 2, remaining 변동 없음
- [ ] 횟수권 마지막 사용(remaining=1 → 0) → 같은 트랜잭션에 status='expired'
- [ ] remaining=0 상태 회원권 체크인 시도 → 422 (status가 expired이므로 NO_ACTIVE_MEMBERSHIP)
- [ ] 같은 회원이 다른 지점에서 동시에 체크인 (각 지점에 활성 회원권 보유) → 둘 다 성공
- [ ] 5초 안에 같은 (member_id, branch_id) 두 번 호출 → 같은 응답, row 1개
- [ ] 6초 후 같은 호출 → 새 row
- [ ] 동시 체크인 race(같은 회원·횟수권) → 한 쪽만 차감, 다른 쪽은 row만 추가 (또는 retry로 자동 복구)
- [ ] response의 `checked_in_at`이 `+09:00`
- [ ] response의 `membership.remaining` 정확

### 체크인 조회 (`GET /api/check-ins`)
#### raw
- [ ] from > to → 400
- [ ] aggregate=raw + cursor 페이지네이션 정상
- [ ] aggregate=raw + 결과 정렬 `checked_in_at DESC, id DESC`
- [ ] branch 토큰이 다른 branch_id 쿼리 → 무시 또는 403 (자기 지점 강제)

#### daily
- [ ] aggregate=daily + 92일 범위 → 통과
- [ ] aggregate=daily + 93일 범위 → 400 `RANGE_TOO_LARGE`
- [ ] 같은 회원이 같은 날 두 번 체크인 → daily에서는 1 row, `checkin_count: 2`
- [ ] aggregate=daily + cursor 파라미터 → 무시 (페이지네이션 없음)
- [ ] aggregate=invalid → 400 `INVALID_AGGREGATE`

### today-count (`GET /api/check-ins/today-count`)
- [ ] branchId 누락 → 400
- [ ] 정확히 KST 자정 직후 → 0
- [ ] KST 23:59:59에 체크인 후 0:00:01 today-count → 0 (다른 날)
- [ ] 같은 회원 두 번 체크인 → count 2 (raw 기준)

### 매출 (`GET /api/sales/summary`)
- [ ] role=branch → 403
- [ ] from > to → 400
- [ ] from~to 92일 초과 → 400 `RANGE_TOO_LARGE`
- [ ] 빈 기간 → `gross_total=0`, `refund_total=0`, `net_total=0`
- [ ] 환불 row 포함 시 `refund_total`은 절대값, `net_total = gross - refund`
- [ ] by_method가 cash/card 모두 분리
- [ ] branchId 미지정(전역) + branchId 지정 결과 비교

### 관리자 CRUD (`/api/admins`, `/api/admins/:id`, `/reset-password`)
- [ ] 비번 7자 → 400 `WEAK_PASSWORD`
- [ ] 비번 영문 없음 → 400
- [ ] 비번 숫자 없음 → 400
- [ ] role='global'인데 branch_id 지정 → 400 `INVALID_ROLE_BRANCH`
- [ ] role='branch'인데 branch_id 미지정 → 400
- [ ] username 중복 (deleted_at IS NULL 중) → 409 `USERNAME_DUPLICATE`
- [ ] soft-deleted username과 같은 username으로 새 admin 생성 → 성공
- [ ] PATCH로 본인 role 변경 → 409 `CANNOT_MODIFY_SELF_ROLE`
- [ ] PATCH로 본인 branch_id 변경 → 409
- [ ] PATCH로 다른 admin의 branch_id 변경 → 그 사용자의 refresh 토큰 무효화 검증
- [ ] DELETE 본인 → 409 `CANNOT_DELETE_SELF`
- [ ] DELETE 후 그 사용자 refresh 토큰으로 refresh 시도 → 401
- [ ] reset-password 응답에 `temporary_password` 12자 + `expires_at`(+24h)
- [ ] reset-password 후 `must_change_password=true`, `temp_password_expires_at` 세팅, `failed_login_count=0`, `locked_until=NULL`
- [ ] 발급 후 24시간 지나서 임시 비번 로그인 → 401 `TEMP_PASSWORD_EXPIRED`

### 지점 CRUD (`/api/branches`, `/api/branches/:id`)
- [ ] address 중복 (NULL 제외) → 409 `ADDRESS_DUPLICATE`
- [ ] address NULL 두 개 → 둘 다 성공
- [ ] address 빈 문자열 → 400 (CHECK 위반)
- [ ] name 빈 문자열·51자 → 400
- [ ] PATCH로 다른 지점 address와 충돌 → 409
- [ ] DELETE 시 해당 지점에 활성 회원 존재 → 409 `BRANCH_IN_USE`
- [ ] DELETE 시 해당 지점에 활성 admin 존재 → 409
- [ ] DELETE 시 모든 회원·admin이 soft-deleted → 성공

---

## 엣지 케이스 (자정·동시성·경계)

### 자정 KST 경계
- [ ] 23:59:59에 체크인 → `checked_in_at`이 어제 날짜로 들어가는지(KST 변환 일관)
- [ ] 0:00:30에 체크인 → 오늘 날짜
- [ ] 자정 배치(00:01) 직전 만료 → status='active' 유지
- [ ] 자정 배치 후 만료 → status='expired'
- [ ] 자정 배치 이후 횟수권 마지막 체크인 → 같은 트랜잭션에서 expired (배치 대기 안 함)

### unpause 만료 경계 (수학 안전성)
unpause는 `end_date_new = end_date_orig + (actual_pause_end - pause_start_date)`로 정리되어 항상 `>= end_date_orig`. 즉 unpause로 인해 만료일이 원래보다 짧아지는 경우는 없음.
- [ ] 정지 시작날 즉시 unpause(`actual_pause_end == pause_start_date`) → end_date 변동 없음, status='active'
- [ ] 정지 마지막 날 unpause → end_date -1, status='active'
- [ ] 정지 등록 후 즉시 unpause(같은 날) → end_date 변동 없음 (잔여 정지 0)

### EXCLUDE 제약 상호작용
- [ ] 동시 회원권 부여(기간 겹치는) 두 트랜잭션 → EXCLUDE가 한 쪽 거부 → 409 (race 보호)
- [ ] pause 등록으로 end_date 연장 결과가 미래 회원권과 겹침 → 409 `MEMBERSHIP_PERIOD_OVERLAP`(pause 트랜잭션 롤백, pause_used=false 유지)
- [ ] cancel-pause로 end_date 단축 시 EXCLUDE 충돌은 발생할 수 없음(검증)
- [ ] bulk-extend가 미래 회원권과 충돌 → 응답에 `first_conflict_membership_id` 포함, 전체 롤백

### 동시성·트랜잭션 retry
- [ ] 동일 회원 동시 체크인 2건 → SELECT FOR UPDATE로 직렬화, row 2개 + remaining 1만 차감(횟수권)
- [ ] 동시 회원권 부여 (기간 겹치는) → EXCLUDE가 한 쪽 거부, 409
- [ ] 동시 pause + bulk-extend → 한 쪽이 `40P01`이면 retry 헬퍼가 자동 재시도
- [ ] 트랜잭션 내 panic → recovery + 롤백 + 500 `INTERNAL`

### 멀티 테넌시 race
- [ ] 회원이 두 지점에 같은 phone으로 가입 → 각 지점 회원권 독립 관리

### 멱등성 race
- [ ] 같은 키로 거의 동시에 두 번 호출 → 한 번만 처리, 다른 쪽은 첫 응답 받기

---

## 에러 핸들링 패턴

### `internal/apperr` 패키지
도메인 에러를 일관된 응답 코드로 변환하는 유일 통로.

```go
type AppError struct {
    Code    string  // 카탈로그의 에러 코드
    Message string  // 사람이 읽는 메시지
    Status  int     // HTTP status
    Cause   error   // 원본 에러 (로그용)
}

func New(code string, message string) *AppError
func IsCode(err error, code string) bool
func FromDBError(err error) *AppError    // pgx 에러 → AppError
```

### DB 에러 코드 매핑 (`apperr.FromDBError`)
- `23505` (unique_violation) → 컨텍스트별 코드
  - `members_branch_phone_unique` → `PHONE_DUPLICATE` (409)
  - `admins_username_key` → `USERNAME_DUPLICATE` (409)
  - `branches_address_key` → `ADDRESS_DUPLICATE` (409)
- `23P01` (exclusion_violation) → `MEMBERSHIP_PERIOD_OVERLAP` (409)
- `23514` (check_violation) → `INVALID_*` (400, CHECK 종류로 분기. 일반화 어려우면 `INVALID_INPUT`)
- `23502` (not_null_violation) → 400 (입력 검증 단계에서 잡혀야 함, 여기 도달하면 버그)
- `23503` (foreign_key_violation) → 400 (참조 무결성 위반, 정상 흐름에서 발생 안 해야 함)
- `40001`/`40P01` → retry 헬퍼가 처리. 헬퍼 외부로 빠져나오면 500
- 기타 → 500 `INTERNAL`

### 핸들러 패턴
```go
func (h *MemberHandler) Create(c *gin.Context) {
    var req CreateMemberRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        respondError(c, apperr.New("INVALID_INPUT", err.Error()).WithStatus(400))
        return
    }
    member, err := h.svc.Create(c.Request.Context(), req)
    if err != nil {
        respondError(c, apperr.FromDBError(err))  // 또는 도메인 에러 그대로
        return
    }
    c.JSON(201, member)
}
```

### 미들웨어 책임
- **Auth**: `Authorization` 검증 → 401 `UNAUTHORIZED`. claim 파싱 후 context에 admin 정보 주입.
- **MustChangePasswordGuard**: claim의 `must_change_password=true` → 차단 라우트면 403 `MUST_CHANGE_PASSWORD`.
- **RoleGuard**: `RequireGlobal()`·`RequireBranch(adminBranchID)`. role 불일치면 403 `FORBIDDEN`.
- **RateLimit**: IP 단위 토큰 버킷. 초과 시 429 `RATE_LIMITED`.
- **CORS**: preflight 즉시 응답 + 매 응답에 헤더.
- **RequestID**: 헤더 발급/전파.
- **Recovery**: panic 캡처 → stack 로그 + 500 `INTERNAL`.
- **Logger**: 모든 요청을 slog로 (status·duration·error_code 포함).
- **Audit**: 로그인·관리자/지점 CRUD를 `admin_audit_logs`에 INSERT.

### 에러 응답 통일
모든 에러는 다음 형태로만 응답:
```json
{ "error": { "code": "ERROR_CODE", "message": "사람이 읽는 메시지" } }
```
- 운영(`APP_ENV=prod`)에서는 `message`도 사람이 읽는 일반 문구만(stack·내부 변수명 절대 노출 금지).
- 개발은 디버깅 위해 `message`에 추가 정보 포함 가능, stack은 항상 로그만.

---

## TDD 실행 순서 (Phase 2)

`docs/ROADMAP.md` Phase 2를 진행할 때 권장 순서:

1. 테스트 인프라 (`internal/testutil`, `TEST_DATABASE_URL`, TestMain 헬퍼)
2. `internal/apperr` (DB 에러 매핑 단위 테스트)
3. `internal/auth` (JWT 발급/검증 + bcrypt + 임시 비번 생성기 — 모두 단위 테스트)
4. 미들웨어 (auth/role/CORS/RequestID/Recovery — 핸들러 테스트로 검증)
5. `POST /api/admin/login` (가장 복잡한 검증 흐름 — 잠금·임시 비번 만료·must_change_password 모두)
6. refresh / logout / password (인증 핵심 4종)
7. `GET /api/branches` (가장 단순. 인증 미들웨어 통과 검증용)
8. 회원 CRUD + search
9. 회원권 부여 (멱등성 + EXCLUDE)
10. pause / unpause / cancel-pause
11. 환불 (멱등성)
12. bulk-extend
13. 체크인 + today-count
14. 체크인 조회 (raw / daily)
15. 매출
16. 관리자 CRUD + reset-password
17. 자정 배치 (수동 실행 명령으로 검증)

각 단계마다 본 문서의 카탈로그 해당 섹션을 체크리스트로 사용한다.
