# Backend — 체육관 체크인 API

## 기술 스택
- Go 1.22+
- Gin (HTTP 라우팅·미들웨어)
- pgx v5 (PostgreSQL 드라이버·풀)
- bcrypt (관리자 비밀번호 해시)
- JWT (관리자 세션 토큰)
- goose (마이그레이션 — `db/` 폴더에서 관리)

## 디렉토리 구조
```
backend/
├── cmd/
│   ├── server/
│   │   └── main.go         # 엔트리포인트 (설정 로드, 라우터·DB 풀 초기화 + 인-프로세스 cron)
│   └── hashpw/
│       └── main.go         # 시드/리셋용 bcrypt 해시 생성 CLI (`go run ./cmd/hashpw <pw>`)
├── internal/
│   ├── http/               # Gin 핸들러 + 라우팅
│   │   ├── router.go
│   │   ├── middleware/     # auth, logging, recovery
│   │   ├── members.go
│   │   ├── memberships.go
│   │   ├── checkins.go
│   │   ├── branches.go
│   │   ├── sales.go        # 매출 조회 (전역 관리자 전용)
│   │   └── admins.go
│   ├── domain/             # 엔티티·도메인 서비스·권한 체크
│   │   ├── member.go
│   │   ├── membership.go   # 부여·정지·환불·대량 연장 로직
│   │   ├── payment.go      # 결제 기록·매출 집계
│   │   ├── checkin.go
│   │   ├── branch.go
│   │   └── admin.go
│   ├── repo/               # pgx 리포지토리 (SQL은 여기에만)
│   │   ├── members_repo.go
│   │   ├── memberships_repo.go
│   │   ├── payments_repo.go
│   │   ├── checkins_repo.go
│   │   ├── branches_repo.go
│   │   └── admins_repo.go
│   ├── auth/               # JWT 발급·검증, must_change_password 가드
│   ├── batch/              # 자정 KST 회원권 상태 전환(만료/정지 복귀)
│   └── config/             # 환경변수 로더
├── go.mod
└── go.sum
```

## 주요 엔드포인트 (초안)
세부 명세(요청·응답·에러 코드)는 `docs/API.md` 참고. 아래는 라우트 목록 요약.
```
POST   /api/admin/login              # 관리자 로그인 → access JWT + refresh JWT + must_change_password
POST   /api/admin/refresh            # refresh JWT → 새 access JWT 발급
POST   /api/admin/logout             # 클라이언트 토큰 폐기 + 서버측 refresh JWT 무효화
POST   /api/admin/password           # 본인 비밀번호 변경 — 현재 비번 재입력 검증

GET    /api/admins                   # 전역 전용 — 관리자 목록 (deleted_at IS NULL)
POST   /api/admins                   # 전역 전용 — 지점 관리자 생성. must_change_password=true 자동
PATCH  /api/admins/:id               # 전역 전용 — 지점 관리자 정보(username, role, branch_id) 변경
DELETE /api/admins/:id               # 전역 전용 — soft delete. 본인 계정은 삭제 불가
POST   /api/admins/:id/reset-password # 전역 전용 — 임시 비밀번호 발급 + must_change_password=true 자동 세팅

GET    /api/branches                 # 지점 목록 (키오스크 초기화·관리자 공용, deleted_at IS NULL)
POST   /api/branches                 # 전역 관리자만
PATCH  /api/branches/:id             # 전역 관리자만
DELETE /api/branches/:id             # 전역 전용 soft delete — 해당 지점에 회원/관리자가 있으면 409 차단

GET    /api/members/search           # q=값, mode=name|phone|memberId, branchId 필수 (키오스크 체크인. 활성 회원권이 있는 회원만 반환)
GET    /api/members                  # 관리자 목록/필터 (지점 관리자=자기 지점, 전역=전체). cursor 페이지네이션
GET    /api/members/:id              # 관리자 회원 단건 + active 회원권 + 회원권 이력(최근 20개) + 결제 이력
POST   /api/members                  # 관리자: 이름·전화·생년월일·지점 (지점 관리자는 branch_id를 자기 지점으로 강제)
PATCH  /api/members/:id              # 지점 관리자는 자기 지점 회원만
DELETE /api/members/:id              # soft delete. 지점 관리자는 자기 지점 회원만

GET    /api/memberships/:id                 # 관리자 회원권 단건 + 결제 이력 + membership_events 이력
POST   /api/members/:id/memberships         # Idempotency-Key 헤더 필수. 부여(monthly+months | pass10) + 결제(amount>0, method, paid_at) 단일 트랜잭션
POST   /api/memberships/:id/pause           # { start_date, end_date, reason } — 한 회원권당 1회만 가능
POST   /api/memberships/:id/unpause         # { reason } — 정지 도달(status='paused') 후 조기 활성화. end_date를 잔여 정지 일수만큼 단축
POST   /api/memberships/:id/cancel-pause    # { reason } — 미래 예약된 정지를 도달 전(status='active') 취소. 만료일/pause_used를 등록 전 상태로 복원
POST   /api/memberships/:id/refund          # Idempotency-Key 헤더 필수. { reason } — payments에 음수 row 자동 추가
POST   /api/memberships/bulk-extend         # 전역 전용. Idempotency-Key 헤더 필수. body: { branch_id?, type?, days, reason } (days 양수 1~90)

POST   /api/check-ins                # 체크인 기록 (active 회원권 한정, 횟수권은 1일 1회만 차감)
GET    /api/check-ins                # 관리자 조회: 지점 관리자=자기 지점만, 전역=전체. ?from&to&branchId?&aggregate=raw|daily, cursor 페이지네이션
GET    /api/check-ins/today-count    # 키오스크용 { branchId } → { count } (오늘 해당 지점 체크인 수)

GET    /api/sales/summary            # 전역 전용 ?from&to&branchId? → { total, by_method, by_day }
```

## 명령어
```
go mod tidy
go run ./cmd/server                           # 개발 실행 (인-프로세스 cron 포함)
go run ./cmd/hashpw "$SEED_ADMIN_PASSWORD"    # 시드/리셋용 bcrypt 해시 출력
go test ./...                                 # 전체 테스트
go build -o bin/server ./cmd/server           # 빌드
DATABASE_URL=... JWT_ACCESS_SECRET=... JWT_REFRESH_SECRET=... ./bin/server  # 실행
./bin/server batch run-expiry                 # 자정 배치를 1회 수동 실행 (외부 스케줄러용)
```

환경변수: `DATABASE_URL`, `JWT_ACCESS_SECRET`, `JWT_REFRESH_SECRET`, `PORT`(기본 8080), `CORS_ORIGIN`, `APP_ENV`(`dev`|`prod`).

## 규칙
### 일반
- **CRITICAL**: SQL은 오직 `internal/repo`에만 존재한다. 핸들러·도메인 서비스는 리포지토리 인터페이스를 통해서만 DB에 접근.
- **CRITICAL**: 비밀번호·JWT·개인정보는 로그·에러 메시지·응답에 포함하지 않는다.
- **CRITICAL**: 지점 관리자(`role='branch'`)의 모든 읽기/쓰기는 서비스 계층에서 `branch_id` 필터를 강제한다. 전역 관리자만 `branch_id` 미지정 허용.
- **CRITICAL**: 다른 지점의 리소스(회원·회원권·체크인)에 지점 관리자가 접근 시 **404로 통일**한다(403이 아닌 404 — 존재 자체를 노출하지 않는 보안 모범). 마찬가지로 존재하지 않는 `:id`도 404. 본인 지점이지만 soft-deleted된 리소스도 404.
- **CRITICAL**: 모든 삭제는 soft delete(`deleted_at = now()`). 모든 조회는 `deleted_at IS NULL` 필터를 강제한다.
- **CRITICAL**: MVP는 **단일 백엔드 인스턴스**를 가정한다. 다음 기능이 인스턴스 내 메모리·인-프로세스 상태에 의존한다 — (1) 체크인 5초 LRU 멱등성 캐시, (2) IP rate limit 토큰 버킷, (3) 인-프로세스 cron 자정 배치. 다중 인스턴스 시 (1)(2)는 Redis로, (3)은 외부 스케줄러 또는 `pg_advisory_lock`으로 마이그레이션 필요. 호스팅 결정(ADR-010) 시 함께 검토.
- 요청 검증: Gin 바인딩 태그 + `validator` 태그 + 명시적 비즈니스 규칙 확인(회원권 날짜 범위·지점 일치 등).
- **CRITICAL**: 모든 SQL은 pgx parameterized query(`$1`, `$2` 등)로만 작성한다. 문자열 연결로 SQL을 만들지 않는다(SQL injection 방어).
- 에러는 구조화 로그(`slog`)로 남기고, 응답은 `{ "error": { "code": "...", "message": "..." } }` 형태로 통일. 코드 카탈로그는 `docs/API.md`.
- panic recovery 미들웨어는 stack trace를 로그에만 남기고, 운영(`APP_ENV=prod`) 응답은 항상 500 `{ "error": { "code": "INTERNAL", "message": "internal server error" } }`로 고정한다(stack trace·내부 에러 메시지 노출 금지). 개발에선 디버깅을 위해 응답에 message만 포함 가능, stack은 로그에만.
- 운영(`APP_ENV=prod`)에서는 모든 응답을 HTTPS로만 서빙(리버스 프록시 + `Strict-Transport-Security: max-age=31536000; includeSubDomains` HSTS 헤더). 개발(`APP_ENV=dev`)은 HTTP 허용 + HSTS 미적용.
- **Request ID**: 미들웨어가 모든 요청에 `X-Request-ID` 헤더를 발급(클라이언트가 보냈으면 그 값 사용, 없으면 UUIDv4 생성). 응답 헤더에도 동일 값 포함. slog 로그·`admin_audit_logs.metadata`에 `request_id` 필드로 기록해 추적성 확보.
- **HTTP server timeouts**: `ReadHeaderTimeout=5s`, `ReadTimeout=10s`, `WriteTimeout=30s`, `IdleTimeout=60s`. Go 기본값(무한)은 슬로우로리스 등 공격에 취약하므로 명시 필수.
- **Request body size limit**: 1MB. `gin.MaxBytesReader`로 라우터 단에서 강제. 초과 시 400.
- **DB connection pool** (pgxpool): `pool_max_conns=25`, `pool_min_conns=2`, `pool_max_conn_idle_time=5m`, `pool_max_conn_lifetime=1h`. 연결 시 `SET TIME ZONE 'UTC'` 적용(세션 타임존 일관).
- **Trusted proxies / X-Forwarded-For**: 호스팅 플랫폼이 프록시면 `gin.SetTrustedProxies([...])`에 플랫폼 내부 CIDR 등록. 클라이언트 IP는 `c.ClientIP()`로 추출(rate limit·`admin_audit_logs.ip`에 사용). 운영 도메인 결정(ADR-010) 시 trusted proxy 목록 OPERATIONS.md에 추가.
- **CORS**: 미들웨어가 모든 응답에 다음을 포함한다. preflight `OPTIONS` 요청에는 본문 없이 200 + 아래 헤더만 응답.
  - `Access-Control-Allow-Origin: $CORS_ORIGIN` (와일드카드 금지, 정확한 도메인 1개)
  - `Access-Control-Allow-Methods: GET, POST, PATCH, DELETE, OPTIONS`
  - `Access-Control-Allow-Headers: Authorization, Content-Type, Idempotency-Key, X-Request-ID`
  - `Access-Control-Allow-Credentials: false` (쿠키 미사용. JWT는 `Authorization` 헤더로만)
  - `Access-Control-Max-Age: 86400` (preflight 캐시 24시간)
  - 운영에서 프론트와 API가 동일 도메인이면 CORS 자체가 불필요하지만, 미들웨어는 동일하게 둔다(다른 도메인 클라가 호출하면 차단).
- **트랜잭션 retry**: pgx 트랜잭션 헬퍼는 PostgreSQL 에러 코드 `40001`(serialization failure)·`40P01`(deadlock)을 만나면 최대 3회 자동 재시도(50ms·100ms·200ms backoff). 그래도 실패하면 500 응답. 사용자가 보지 못하는 정도의 짧은 충돌은 자동 복구되도록 한다.
- **Graceful shutdown**: SIGTERM 수신 시 (1) 인-프로세스 cron 정지 → (2) HTTP 서버 신규 연결 차단 + 진행 중 요청 30초 대기 → (3) DB 풀 close 순서. 30초 안에 끝나지 않은 요청은 강제 종료.
- **slog 구조화 로그 필드**: 모든 액세스 로그는 `request_id`, `admin_id`(있으면), `ip`, `method`, `path`, `status`, `duration_ms`, `error_code`(있으면)를 포함한다. 비밀번호·JWT·토큰·평문 임시 비번·회원 PII(전화·생년월일)는 어떤 필드에도 넣지 않는다.
- **응답 timestamp**: 모든 `timestamptz` 응답은 KST 오프셋(`+09:00`) 형식으로 직렬화한다(예: `2026-04-27T18:23:00+09:00`). pgx 결과를 `time.Time`으로 받은 뒤 핸들러에서 `Asia/Seoul` Location으로 변환 후 ISO8601 마샬링. UTC `Z` 표기 금지(클라가 한국 운영, 디버깅·로그 직관성).
- **CORS**: 미들웨어가 모든 응답에 다음을 포함한다. preflight `OPTIONS` 요청에는 본문 없이 200 + 아래 헤더만 응답.
  - `Access-Control-Allow-Origin: $CORS_ORIGIN` (와일드카드 금지, 정확한 도메인 1개)
  - `Access-Control-Allow-Methods: GET, POST, PATCH, DELETE, OPTIONS`
  - `Access-Control-Allow-Headers: Authorization, Content-Type, Idempotency-Key`
  - `Access-Control-Allow-Credentials: false` (쿠키 미사용. JWT는 `Authorization` 헤더로만)
  - 운영에서 프론트와 API가 동일 도메인이면 CORS 자체가 불필요하지만, 미들웨어는 동일하게 둔다(다른 도메인 클라가 호출하면 차단).
- **트랜잭션 retry**: pgx 트랜잭션 헬퍼는 PostgreSQL 에러 코드 `40001`(serialization failure)·`40P01`(deadlock)을 만나면 최대 3회 자동 재시도(50ms·100ms·200ms backoff). 그래도 실패하면 500 응답. 사용자가 보지 못하는 정도의 짧은 충돌은 자동 복구되도록 한다.

### 인증·세션
- 토큰은 두 종류 (서명 알고리즘 HS256, 비밀키는 환경변수):
  - **access token**: 만료 30분, 모든 API 요청 헤더 `Authorization: Bearer ...`. 비밀키 `JWT_ACCESS_SECRET`.
    - claim: `{ sub: <admin_id>, username, role, branch_id, must_change_password, iat, exp }`
    - 매 요청마다 DB 조회 없이 권한 판정이 가능하도록 라우트 가드에 필요한 정보만 담는다. `temp_password_expires_at`은 access claim에 넣지 않는다(만료 검증은 로그인 시점에서만).
    - **claim 무결성 검증**: 미들웨어가 토큰 파싱 후 모든 필수 필드(`sub, role, iat, exp`) 존재를 확인한다. 누락·타입 오류는 401 `UNAUTHORIZED`(위조·구버전 토큰 차단).
    - **admin DELETE / 비번 변경 시 access 즉시 무효화**: Auth 미들웨어는 access claim 검증 후 `SELECT password_updated_at FROM admins WHERE id=? AND deleted_at IS NULL`로 admin row를 매 요청 조회한다.
      - row가 없으면 401 `UNAUTHORIZED`(soft-deleted admin의 access 즉시 차단)
      - `password_updated_at IS NOT NULL` 이고 access claim의 `iat < password_updated_at`이면 401 `UNAUTHORIZED`(비번 변경 직후 다른 디바이스 stale access 즉시 차단)
      - DB 1쿼리 추가 비용은 관리자 트래픽 규모(분당 수십 회) 기준 무시 가능. 두 검증을 한 쿼리에 합쳐 round-trip은 1회 유지.
  - **refresh token**: 만료 15시간, `POST /api/admin/refresh`에서만 사용. 비밀키 `JWT_REFRESH_SECRET`.
    - claim: `{ sub: <admin_id>, jti, iat, exp }` (UUIDv4 jti)
    - jti는 `revoked_refresh_tokens` 무효화 목록 키. claim에는 권한 정보를 넣지 않는다(refresh는 새 access 발급용일 뿐, DB에서 admin row를 다시 읽어 새 access claim을 채운다 → 비번 변경·role 변경이 새 access에 즉시 반영됨).
- **`Idempotency-Key` 형식 검증**: 헤더 값이 UUIDv4 형식이어야 한다(정규식 검증). 위반 시 400 `INVALID_IDEMPOTENCY_KEY`. 빈 문자열·임의 string으로 인한 키 공간 오염 방지.
- `POST /api/admin/login`: 두 토큰 모두 발급. 응답 body로 access·refresh 둘 다 반환(refresh는 `localStorage`에 저장).
- `POST /api/admin/refresh`: refresh 토큰을 검증 → 새 access 토큰 발급. `revoked_refresh_tokens` 테이블에서 `jti` 조회되면 401.
- `POST /api/admin/logout`: refresh 토큰의 `jti`를 `revoked_refresh_tokens`에 INSERT. 클라이언트는 access·refresh 모두 폐기.
- `must_change_password=true`인 관리자 JWT는 `/api/admin/password`, `/api/admin/logout`, `/api/admin/refresh` 외 모든 라우트에서 403.
- `POST /api/admin/password`는 항상 **현재 비밀번호 재입력**을 검증한다. bcrypt 비교 실패 시 401, 성공 시 새 해시로 갱신 + `must_change_password=false` + `password_updated_at=now()` + `temp_password_expires_at=NULL` 세팅 + 해당 사용자의 기존 refresh 토큰을 모두 무효화한다.
- **비번 변경 후 stale access 즉시 차단**: 비번 변경 응답 후 다른 디바이스에 살아있는 access(만료까지 최대 30분)는 Auth 미들웨어의 `iat < password_updated_at` 검증으로 다음 요청에서 401 처리된다(refresh 무효화 + access 무효화로 모든 디바이스 강제 재로그인). 호출한 본인 디바이스는 응답 처리 후 새 토큰을 받기 위해 재로그인하거나, 클라이언트가 비번 변경 폼에서 자동 로그아웃 → 재로그인 유도.
- **새 비밀번호 강도 정책**: 최소 8자, 영문 1자 이상, 숫자 1자 이상. 위반 시 400 `WEAK_PASSWORD`. 특수문자는 강제하지 않는다(태블릿/모바일 입력 부담).
- **로그인 방어**: `failed_login_count` 컬럼을 사용. 비번 5회 연속 실패 시 `locked_until = now() + 15분` 세팅 → 잠금 동안 401 반환(에러 코드 `ACCOUNT_LOCKED`). 카운터 리셋 규칙:
  - 정상 로그인 성공 시 `failed_login_count=0`.
  - `locked_until` 경과 후 다음 시도가 정확하면 정상 로그인 처리(0으로 리셋), 틀리면 카운터 1부터 다시 누적(잠금 해제 시점에 자동 0으로 리셋되지는 않는다).
  - 추가로 IP 단위 rate limit(15분당 60회)을 미들웨어에서 적용.
- **임시 비밀번호 생성(`reset-password`)**: 길이 12, charset = `ABCDEFGHJKLMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789` (헷갈리는 `0/O/I/l/1`·소문자 `o/i` 제외). `crypto/rand`로 생성. 해시(bcrypt cost 12) 갱신 + `must_change_password=true` + `temp_password_expires_at=now()+24h` + `failed_login_count=0` + `locked_until=NULL` 세팅. 응답에 plaintext 1회 반환, 로그·DB 평문 저장 금지.
- **임시 비밀번호 만료(24시간)**: 로그인 시점에 `must_change_password=true` 이고 `temp_password_expires_at < now()` 이면 401 `TEMP_PASSWORD_EXPIRED` 반환. 전역 관리자가 다시 `reset-password`를 호출해 새 임시 비번을 발급해야 한다.
- **감사 로그(`admin_audit_logs`) 자동 기록**: 미들웨어가 다음 액션을 자동 INSERT 한다 — `login_success`, `login_failure`, `logout`, `password_change`, `password_reset`, `admin_create`, `admin_update`, `admin_delete`, `branch_create`, `branch_update`, `branch_delete`. 컬럼: `admin_id`(실패는 NULL 가능), `action`, `target_type`, `target_id`, `ip`, `user_agent`, `metadata`(jsonb). 회원·회원권 변경은 `membership_events`/`payments.performed_by`가 추적하므로 여기에 기록하지 않는다.

### 페이지네이션
- 목록 API(`/api/members`, `/api/check-ins?aggregate=raw`)는 cursor 기반.
- 쿼리: `?cursor=<opaque>&limit=<int>` (기본 20, 최대 100, 초과 시 400 `INVALID_LIMIT`).
- cursor는 JSON `{"t": "<RFC3339 timestamp>", "id": <bigint>}`을 base64로 인코딩한 opaque 문자열. 디코딩 실패·필드 누락·타입 오류는 400 `INVALID_CURSOR`.
- 정렬은 `created_at DESC, id DESC`(또는 `checked_in_at DESC, id DESC`)로 고정. WHERE 절은 `(timestamp, id) < (cursor.t, cursor.id)` 형태(키셋 페이지네이션).
- 응답에 `next_cursor`를 포함한다. 다음 페이지가 없으면 `null`.
- `GET /api/check-ins?aggregate=daily`는 페이지네이션을 적용하지 않는다(짧은 기간 집계 용도). `from`~`to` 간격은 최대 92일로 제한, 초과 시 400 `RANGE_TOO_LARGE`.

### 회원·검색
- 회원 전화번호는 `^[0-9]{11}$` (예: `01012345678`)만 허용. 핸들러에서 입력 검증 + DB CHECK 제약. 동일인이 여러 지점에 가입할 수 있으므로 phone은 전역 unique가 아니지만, **같은 지점 내 중복은 차단**(`(branch_id, phone)` 부분 유니크 인덱스). 같은 지점에 같은 번호 등록 시 409 `PHONE_DUPLICATE`.
- 회원 이름은 1~100자, `birth_date`는 NOT NULL.
- `PATCH /api/members/:id`에서 변경 가능한 필드는 **`name`, `phone`, `birth_date`만**이다. `branch_id`는 요청 body에 와도 무시(이전 불가). 다른 필드(예: `created_at`)도 무시.
- `GET /api/members` 응답은 `branch_name`을 포함해 전역 관리자가 지점을 식별할 수 있게 한다.
- `GET /api/members/search`(키오스크용):
  - `mode=name`: **prefix 일치**(`name LIKE 'q%'`), 입력 최소 2글자(미만은 400 `QUERY_TOO_SHORT`).
  - `mode=phone`: 입력 4자리 정확 일치(`phone_last4 = q`). 4자리 아니면 400.
  - `mode=memberId`: `id = q::bigint` 정확 일치. 숫자가 아니면 400.
  - 결과는 **활성 회원권(`status='active'`)이 있는 회원만 반환**한다. 활성 회원권이 없는 회원(없음/expired/refunded/paused)은 검색 결과에서 제외.
  - 결과 정렬: 해당 회원의 가장 최근 `check_ins.checked_in_at` DESC, NULL은 마지막. 회원권 상태별 분기는 키오스크에서 처리하지 않는다.
  - 결과 limit 20. 더 많은 동명이인이 있으면 응답에 `truncated: true`를 포함하고, 키오스크는 "결과가 너무 많습니다. 회원 번호 또는 전화 4자리로 검색해주세요" 안내를 띄운다.
  - 응답 한 row: `{ id, name, phone_masked, birth_md, member_id_display }`. 식별 보조정보는 마스킹된 형태(`010-****-1234`, `**-04-15`)로만 노출. `member_id_display`는 `#1234` 형태로 서버에서 포맷.

### 체크인
- 체크인은 같은 날 중복 허용 — `check_ins` 삽입에 유니크 제약 없음.
- **활성 회원권이 있는 회원만 체크인 가능**. 핸들러는 트랜잭션 내에서 `memberships WHERE member_id=? AND branch_id=? AND status='active' AND start_date <= (now() AT TIME ZONE 'Asia/Seoul')::date AND end_date >= (now() AT TIME ZONE 'Asia/Seoul')::date FOR UPDATE`로 잠근다.
  - 잠긴 row가 없는 원인을 구분: status가 active가 아니면 422 `NO_ACTIVE_MEMBERSHIP`, status는 active인데 `start_date > 오늘`이면 422 `MEMBERSHIP_NOT_STARTED`, `end_date < 오늘`이면 자정 배치가 expired로 전환할 예정이므로 `NO_ACTIVE_MEMBERSHIP`로 통일.
  - `check_ins.membership_id`는 NOT NULL이므로 잠긴 row의 id를 그대로 INSERT.
- 횟수권 차감은 **같은 회원·같은 날짜·같은 지점에 기존 `check_ins` row가 없을 때만** 동일 트랜잭션에서 `memberships.remaining -= 1`. 두 번째부터는 row만 추가, 잔여 변동 없음.
- 횟수권 차감 후 `remaining=0`이면 같은 트랜잭션에서 `status='expired'`로 전환.
- 동시 체크인 이중 차감 방지를 위해 위 `SELECT ... FOR UPDATE` 사용(또는 트랜잭션 격리 `SERIALIZABLE`).
- **이중 클릭 방지(짧은 멱등성)**: `(member_id, branch_id)` 기준으로 직전 5초 안에 성공한 체크인이 있으면, 새 row를 만들지 않고 기존 체크인 응답을 그대로 반환(같은 클릭의 중복 처리). 메모리 LRU 캐시(키 `member_id:branch_id` → `(check_in_id, checked_in_at)`, TTL 5초)로 충분 — 키오스크 디바운스가 1차 방어, 서버 캐시가 2차.
- `GET /api/check-ins`의 `aggregate` 파라미터는 `raw|daily` enum. 잘못된 값은 400 `INVALID_AGGREGATE`. `aggregate=daily`는 페이지네이션을 두지 않으며 `(from, to)` 간격 최대 92일로 제한(초과 시 400 `RANGE_TOO_LARGE`).
- `GET /api/check-ins/today-count?branchId=...`는 KST 기준 오늘 카운트: `WHERE branch_id = ? AND (checked_in_at AT TIME ZONE 'Asia/Seoul')::date = (now() AT TIME ZONE 'Asia/Seoul')::date`.
- 영업시간 외 체크인은 차단하지 않는다(MVP 범위 밖).

### 회원권
- 회원권 부여(`POST /api/members/:id/memberships`): **`Idempotency-Key` 헤더 필수**(클라이언트 발급 UUID). 트랜잭션:
  - `memberships` insert (`type`, `months`/`remaining`, `start_date`, `end_date`)
  - `payments` insert: **`paid_at`은 서버가 KST 오늘로 자동 설정**(클라가 보내도 무시 — 회원권 등록일 = 결제일). `branch_id`는 회원의 `branch_id`로 자동 채움(클라 입력 무시). `amount > 0`은 클라 검증, `method`는 `cash|card`.
  - `amount <= 0`은 400 `INVALID_AMOUNT`. 0원/무료 결제 미지원.
- 회원의 `deleted_at IS NOT NULL`이면 404 (모든 회원 단건 라우트 공통).
- `start_date`는 **오늘 또는 미래만 허용**한다. 과거 날짜로 부여 불가(400 `INVALID_START_DATE`).
- **다음 회원권 미리 등록 허용**: 한 회원에 active/paused 회원권이 있어도 **기간이 겹치지 않으면** 새 회원권을 등록할 수 있다(예: 5/30 만료 active 보유 + 6/1~ 시작 회원권 미리 등록). DB의 EXCLUDE 제약(`memberships_no_period_overlap`, `daterange(start_date, end_date, '[]')`)이 강제. 위반 시 PostgreSQL `23P01` 에러를 핸들러가 잡아 409 `MEMBERSHIP_PERIOD_OVERLAP`로 변환.
- 동시 부여 트랜잭션 race는 EXCLUDE가 막으므로 추가 잠금 불필요(보조적으로 회원의 현재 active/paused 회원권을 `FOR SHARE`로 잠그는 건 선택).
- 기간 계산:
  - `monthly`: body의 `months`(int, 1 이상) 받아 `end_date = start_date + months month`.
  - `pass10`: `remaining=10`, `end_date = start_date + 2 month` 자동 계산(body로 받지 않음).
- 회원권 정지(`POST /api/memberships/:id/pause`):
  - 한 회원권당 1회만 허용 — `pause_used=true`면 409 `PAUSE_ALREADY_USED`.
  - body: `{ start_date, end_date, reason }`. 검증: `start_date <= end_date`, `start_date >= 오늘`, **`start_date >= memberships.start_date`**(미래 시작 회원권의 시작 전 정지 차단), `end_date <= 회원권 end_date`. 위반 시 400 `INVALID_PAUSE_RANGE`.
  - 트랜잭션: `pause_start_date/pause_end_date` 세팅 + `end_date += (pause_end_date - pause_start_date)` + `pause_used=true` + `membership_events`(action='pause') 기록.
  - `pause_start_date <= 오늘`이면 같은 트랜잭션에서 `status='paused'`로 전환. 미래면 `status='active'` 유지(자정 배치가 도래일에 paused로 전환).
  - **EXCLUDE 충돌 처리**: `end_date` 연장 결과가 같은 회원의 미래 등록된 회원권과 기간이 겹치면 PostgreSQL `23P01` → 핸들러가 409 `MEMBERSHIP_PERIOD_OVERLAP`로 변환(롤백). 운영자는 미래 회원권 `start_date` 조정 또는 정지 기간 단축으로 대응.
- 회원권 조기 활성화(`POST /api/memberships/:id/unpause`):
  - 현재 `status='paused'`인 회원권에서만 호출 가능. 아니면 409 `NOT_PAUSED`.
  - 트랜잭션: `actual_pause_end = 오늘`. `잔여_정지일 = pause_end_date - actual_pause_end` (양수). `end_date -= 잔여_정지일`. `pause_start_date/pause_end_date = NULL`. `status='active'`. `membership_events`(action='unpause', actual_pause_end) 기록.
  - 예: 4/1~4/7 정지 + 원 만료 5/30. 4/6에 unpause 호출 → 잔여 정지 1일 → 만료 5/29.
- 미래 예약 정지 취소(`POST /api/memberships/:id/cancel-pause`):
  - 호출 가능 조건: `status='active'` 이면서 `pause_used=true` 이고 `pause_start_date > 오늘`(즉, 등록은 했지만 아직 도래하지 않은 정지). 그 외에는 409 `PAUSE_NOT_SCHEDULED`.
  - 트랜잭션: `end_date -= (pause_end_date - pause_start_date)`로 정지 등록 시 늘렸던 만큼 되돌림 + `pause_start_date/pause_end_date = NULL` + `pause_used=false`(다시 정지 등록 가능) + `membership_events`(action='cancel_pause') 기록.
- 환불(`POST /api/memberships/:id/refund`): **`Idempotency-Key` 헤더 필수**. 호출 가능 status:
  - `active` (사용 중)
  - `paused` (정지 중)
  - `active` + `start_date > 오늘` (미래 시작, 사전 결제 후 마음 바꿈)
  - `expired` → 409 `MEMBERSHIP_ALREADY_EXPIRED` (만료 후 환불 불가)
  - `refunded` → 409 (재환불 차단. 단 같은 Idempotency-Key 재호출이면 첫 응답)
  
  트랜잭션: `memberships.status='refunded'` + `payments`에 음수 row 추가. 환불 row 필드는 모두 **서버가 자동 채운다**(클라는 `reason`만 보냄):
  - `paid_at` = 서버 시각의 KST 오늘
  - `method` = 원본 결제 row의 `method`와 동일(원본 cash → 환불 cash row, card → card row). 매출 집계의 `by_method` 분리가 자연스럽게 유지됨.
  - `amount` = 원본 결제 row의 `amount`의 부호 반전(원본 150000 → -150000). MVP는 전체 환불만 지원이라 단순 반전으로 충분(부분 환불은 Phase 5+).
  - `branch_id` = 원본 결제 row의 `branch_id`와 동일(매출 집계 일관).
  - `performed_by` = 호출한 관리자 admin_id.
  - `memo` = NULL(reason은 `membership_events`에 기록).
  
  같은 회원권에 부여 결제 row가 여러 개일 가능성은 MVP에 없음(부여 1회 = 결제 1 row). 안전망으로 `WHERE membership_id=? AND amount>0 ORDER BY paid_at ASC LIMIT 1`로 원본을 잡는다(여러 row가 생겨도 최초 부여 결제 기준).
- 대량 연장(`POST /api/memberships/bulk-extend`):
  - 전역 토큰 + **`Idempotency-Key` 헤더 필수**(클라이언트 발급 UUID). 헤더 없으면 400 `IDEMPOTENCY_KEY_REQUIRED`.
  - 같은 키 재호출 시 `(admin_id, endpoint, request_hash)`를 비교해 일치하면 첫 응답 그대로 재반환(연장은 한 번만). 같은 키인데 body가 다르면 409 `IDEMPOTENCY_KEY_CONFLICT`. 24시간이 지난 키는 무효 처리.
  - body의 `days`는 **양수 1~90 한도**(음수·0·91 이상은 400 `INVALID_EXTEND_DAYS`). 단축은 미지원.
  - 대상 범위: 필터 조건(`branch_id?`, `type?`)으로 뽑은 **`status IN ('active','paused')` memberships row들 모두**. paused 회원권도 동일하게 +days(연휴 보상은 정지 중 회원에게도 적용되어야 자연스러움).
  - 트랜잭션 처리(모든 대상 row):
    - `end_date += days` (모든 대상 공통)
    - status='paused' 또는 status='active' + 미래 예약 정지(`pause_used=true` AND `pause_start_date > 오늘`)이면 `pause_start_date += days`, `pause_end_date += days` (정지 일정도 같이 이동)
    - 그 외(미래 정지 예약 없는 active)는 `end_date`만 연장
    - `membership_events`(action='bulk_extend', extend_days) 기록
  - **EXCLUDE 충돌 처리**: 연장 결과가 같은 회원의 미래 등록된 회원권과 기간이 겹치게 되면 PostgreSQL이 `23P01`로 거부한다. 핸들러는 트랜잭션을 롤백하고 409 `MEMBERSHIP_PERIOD_OVERLAP`로 응답하며, 응답 body에 `first_conflict_membership_id`(첫 충돌 회원권 ID)를 포함해 운영자가 즉시 디버깅 가능하게 한다. `extended_count`는 0(전체 롤백).
- 매출 합계 조회는 `payments`만 본다(`memberships`/`check_ins`로 매출을 역산하지 않는다). `paid_at`(date) 기준으로 일/월 그룹.
- `GET /api/sales/summary` 응답은 환불을 분리해서 노출한다 — `gross_total`(양수 합), `refund_total`(음수 합, 절대값), `net_total = gross - refund`. `by_method`/`by_day`도 같은 분리 적용.
- `GET /api/sales/summary`는 전역 관리자 토큰만 통과(미들웨어 차단).

### 자정 KST 배치(`internal/batch`)
**매일 KST 00:01에 실행**(00:00 자정 경계 데이터 일관성 안전 margin). SQL은 `(now() AT TIME ZONE 'Asia/Seoul')::date`로 KST 기준 날짜 계산. 순서대로 트랜잭션 실행:
1. `UPDATE memberships SET status='expired' WHERE status='active' AND end_date < (now() AT TIME ZONE 'Asia/Seoul')::date`
2. `UPDATE memberships SET status='active', pause_start_date=NULL, pause_end_date=NULL WHERE status='paused' AND pause_end_date < (now() AT TIME ZONE 'Asia/Seoul')::date`
3. `UPDATE memberships SET status='paused' WHERE status='active' AND pause_start_date = (now() AT TIME ZONE 'Asia/Seoul')::date` (예약된 정지 도래)
4. 정리 잡:
   - `DELETE FROM idempotency_keys WHERE created_at < now() - interval '24 hours'`
   - `DELETE FROM revoked_refresh_tokens WHERE revoked_at < now() - interval '15 hours'` (refresh JWT 만료 길이만큼만 보관)
   - `DELETE FROM admin_audit_logs WHERE created_at < now() - interval '1 year'` (1년 보관)

MVP 구현은 인-프로세스 cron(`robfig/cron/v3`, KST 스케줄 `1 0 * * *`). `./bin/server batch run-expiry` 명령으로 1회 실행도 가능해야 한다.

### 관리자·지점
- `POST /api/admins`는 전역 토큰만 통과. body 검증: `username` 유니크(soft-deleted 포함하지 않음, `deleted_at IS NULL`만), `password` 강도(8자 이상 + 영문/숫자 혼합), `role`이 `'branch'`면 `branch_id` 필수·`'global'`이면 `branch_id`는 NULL. 생성 row의 `must_change_password=true` + `temp_password_expires_at=now()+24h` 강제. 응답에 비밀번호·해시 노출 금지.
- `PATCH /api/admins/:id`는 전역 토큰만 통과. 변경 가능 필드: `username`, `role`, `branch_id`. 검증: role/branch_id 조합(`global`은 branch_id NULL 강제, `branch`는 branch_id 필수), username 유니크. `password_hash`·`must_change_password`·`failed_login_count`·`locked_until` 등은 PATCH로 변경 불가(별도 엔드포인트 사용). `branch_id` 변경 시 해당 사용자의 refresh 토큰 모두 무효화(권한 변동을 즉시 반영).
- `DELETE /api/admins/:id`는 전역 토큰만 통과 + 호출자 본인 계정은 삭제 불가(409 `CANNOT_DELETE_SELF`). soft delete 후 해당 사용자의 refresh 토큰 모두 무효화.
- `DELETE /api/branches/:id`는 동일 트랜잭션 안에서 (1) `members WHERE branch_id=:id AND deleted_at IS NULL`이 0인지, (2) `admins WHERE branch_id=:id AND deleted_at IS NULL`이 0인지 확인 후 0이 아니면 409 `BRANCH_IN_USE`. 통과하면 `deleted_at = now()`로 soft delete.
- `PATCH /api/branches/:id`는 `name`/`address` 변경. 다른 지점 주소와 충돌 시 409 `ADDRESS_DUPLICATE`.
- `DELETE /api/members/:id`는 soft delete. 지점 관리자는 자기 지점 회원만. 활성 회원권이 있어도 삭제는 가능(이후 검색·체크인에서 제외). 매출/체크인 이력은 그대로 보존.

## 테스트 (TDD)

### 워크플로우
- 모든 핸들러·도메인 함수는 **테스트 먼저** 작성한다(Red → Green → Refactor).
- 정상 케이스 1개당 에러/엣지 케이스 N개를 함께 작성. 카탈로그는 `docs/TESTING.md` 참조.
- 테스트 없는 PR은 머지 금지. 변경된 라우트는 그 변경에 해당하는 테스트가 같이 들어와야 한다.
- 테스트는 결정적(deterministic): 시간·UUID·랜덤은 인터페이스로 주입(`Clock`/`UUIDGen`)해서 고정 가능하게.

### 계층
- **단위 테스트**(`internal/domain`, `internal/auth`, `internal/util`): 외부 의존성 없는 순수 함수. 만료일 계산·비번 강도·cursor 인코딩·KST 날짜·임시 비번 생성기·JWT claim 등.
- **리포지토리 테스트**(`internal/repo`): 실제 Postgres(`TEST_DATABASE_URL`, goose 적용). 모킹 금지. DB 제약 위반(unique·CHECK·EXCLUDE·FK) 테스트도 포함.
- **핸들러 테스트**(`internal/http`): Gin `httptest`로 router → middleware → repo까지 실제로 통과하는 e2e. 응답 status·body·헤더(`X-Request-ID`, CORS) 검증.

### 인프라
- 테스트 DB: `gym_test` (운영/개발 격리). `TEST_DATABASE_URL` 환경변수.
- `internal/testutil` 헬퍼: `SetupDB(t)`, `TruncateAll(t, db)`, `CreateAdmin/Branch/Member/Membership(t, db, opts)`, `Login(t, server, ...)`, `AuthRequest(t, ...)`, `FreezeTime(t, instant)`.
- 격리: 매 테스트 전 `TRUNCATE TABLE ... RESTART IDENTITY CASCADE`(시드 admin/branch는 별도 보존 또는 매번 재시드).
- CI: `go test -race ./...`. 통합 테스트는 `//go:build integration` 빌드 태그로 분리해 `go test -short`로 단위만 빠르게 돌릴 수 있게.
- 커버리지 기준선: 핸들러 80%, 도메인 90% (참고용, 절대 기준 아님).

### 에러 핸들링 (`internal/apperr`)
- 모든 도메인 에러는 `apperr.AppError`(`Code`, `Message`, `Status`, `Cause`)로 통일.
- DB 에러 매핑(`apperr.FromDBError`):
  - `23505 unique_violation` → 컨스트레인트 이름으로 분기: `members_branch_phone_unique` → `PHONE_DUPLICATE`, `admins_username_key` → `USERNAME_DUPLICATE`, `branches_address_key` → `ADDRESS_DUPLICATE` (모두 409).
  - `23P01 exclusion_violation` → `MEMBERSHIP_PERIOD_OVERLAP` (409).
  - `23514 check_violation` → 400 `INVALID_INPUT` (혹은 컨텍스트별 INVALID_*).
  - `23502 not_null_violation` / `23503 foreign_key_violation` → 정상 흐름에선 발생 안 해야 함. 발생 시 500.
  - `40001`/`40P01` → 트랜잭션 retry 헬퍼가 흡수. 외부로 빠지면 500.
  - 기타 → 500 `INTERNAL`.
- 에러 응답은 항상 `{ "error": { "code": "...", "message": "..." } }`. 운영에서 `message`는 일반 문구만(stack·내부 변수명 노출 금지).

### 미들웨어 책임 분리
- **Auth**: `Authorization` 검증 → 401. claim을 context에 주입.
- **MustChangePasswordGuard**: 차단 라우트면 403 `MUST_CHANGE_PASSWORD`.
- **RoleGuard**: `RequireGlobal()`/`RequireBranch()`로 라우트별 적용. 불일치 403.
- **RateLimit**: IP 토큰 버킷. 초과 시 429.
- **CORS**: preflight + 모든 응답 헤더.
- **RequestID**: `X-Request-ID` 발급/전파.
- **Recovery**: panic → 500 `INTERNAL`, stack은 로그만.
- **Logger**: slog 액세스 로그(필드: request_id, admin_id, ip, method, path, status, duration_ms, error_code).
- **Audit**: 로그인·관리자/지점 CRUD → `admin_audit_logs`.

### 에러·엣지 케이스 카탈로그
모든 핸들러 테스트 작성 시 `docs/TESTING.md`의 해당 섹션을 체크리스트로 사용한다. 예: 회원권 부여 핸들러 테스트는 "회원권 부여" 섹션의 모든 항목 + 공통(인증·권한·멱등성·페이지네이션 해당 시)을 빠짐없이 커버.
