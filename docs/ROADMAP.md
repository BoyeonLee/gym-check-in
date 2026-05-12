# 구현 로드맵

다른 세션에서 작업을 이어받기 위한 진입점. 미완료 항목 중 가장 위의 것부터 진행한다.
스펙은 모두 `docs/PRD.md`, `docs/ARCHITECTURE.md`, `docs/UI_GUIDE.md`와 각 모듈의 `CLAUDE.md`에 있으며, 이 파일은 **순서·산출물·검증**만 다룬다.

## 진행 원칙
- 순서: **DB → Backend → Frontend**. 하위 계층의 계약(스키마·API)이 먼저 고정되어야 상위가 흔들리지 않는다.
- 한 Phase가 "검증 기준"을 모두 통과하기 전에는 다음 Phase로 넘어가지 않는다.
- 커밋은 conventional commits (`feat:`, `fix:`, `docs:`, `refactor:`).
- 각 Phase 시작 시 새 브랜치(`feat/env-bootstrap`, `feat/db-init`, `feat/backend-skeleton`, `feat/frontend-skeleton`)를 끊는다.

---

## Phase 0 — `.env` 부트스트랩
**의존**: 없음. 가장 먼저.
**참조**: `docs/DEV_SETUP.md`, 루트 `.env.example`, `db/CLAUDE.md`

### 산출물
- 루트 `.env` (gitignore 처리, 커밋 금지) — `.env.example`의 모든 키 채움
- 시드용 bcrypt 해시 (`SEED_ADMIN_PASSWORD_HASH`)

### 작업 항목
- [ ] `.env.example`을 복사해 루트 `.env` 생성
- [ ] `DATABASE_URL`, `JWT_ACCESS_SECRET`, `JWT_REFRESH_SECRET`, `CORS_ORIGIN`, `APP_ENV=dev`, `PORT` 채움
- [ ] `SEED_ADMIN_USERNAME`, `SEED_ADMIN_PASSWORD` 채움(개발용 임시값. 운영은 절대 재사용 금지)
- [ ] `SEED_BRANCH_NAME`, `SEED_BRANCH_ADDRESS` 채움(기본값: `PBOY MMA 본점` / `서울 송파구 가락로 142 지하 1층`)
- [ ] `go run ./backend/cmd/hashpw "$SEED_ADMIN_PASSWORD"` 실행 → 출력된 `$2a$12$...` 해시를 `.env`의 `SEED_ADMIN_PASSWORD_HASH`에 저장
- [ ] `JWT_ACCESS_SECRET`/`JWT_REFRESH_SECRET`은 서로 다른 32바이트 이상 랜덤(예: `openssl rand -base64 48`)
- [ ] `.gitignore`에 `.env`가 포함되어 있는지 재확인

### 검증 기준
- `git status`에 `.env`가 보이지 않음(staged 아님)
- `direnv` / `set -a; source .env; set +a` 등으로 셸에 변수 로드 가능
- `echo $DATABASE_URL`이 비어있지 않음
- Phase 1 시작 시 `goose`/`psql` 명령이 별도 인자 없이 `$DATABASE_URL`로 실행됨

---

## Phase 1 — DB 스키마·마이그레이션·시드
**의존**: Phase 0 통과 (`.env`로 DB 연결 가능).
**참조**: `db/CLAUDE.md`, 루트 `docker-compose.yml`, `.env.example`

### 산출물
- 루트 `docker-compose.yml` — PostgreSQL 15-alpine + named volume + healthcheck (이미 작성됨)
- 루트 `.env.example` — DB·JWT·시드 키 목록 (이미 작성됨). 실제 `.env`는 `.gitignore` 처리
- `db/migrations/00001_init.sql` — `branches`, `members`, `memberships`, `membership_events`, `check_ins`, `admins`, `payments`, `idempotency_keys`, `revoked_refresh_tokens`, `admin_audit_logs` 테이블 + 인덱스 + CHECK 제약 + 부분 유니크 인덱스
- `db/migrations/00002_updated_at_trigger.sql` — `set_updated_at()` 함수 + 각 테이블 BEFORE UPDATE 트리거
- `db/seeds/001_admin_and_branch.sql` — 전역 관리자 1명(`must_change_password=true`, `temp_password_expires_at=now()+24h`) + 샘플 지점 1개. bcrypt 해시·지점 이름·지점 주소는 `psql -v` 변수로 주입(평문은 SQL/로그에 남기지 않는다).
- `db/README.md`(선택) — 로컬 실행 + goose 적용 절차 한 페이지

### 작업 항목
- [ ] `docker compose up -d db` 실행 → 컨테이너 healthy 확인 → `psql` 접속 가능한지 확인
- [ ] goose 설치 가이드(macOS/Linux 한 줄) `db/CLAUDE.md`에 보강
- [ ] `00001_init.sql` 작성 (Up/Down 모두):
  - `payments(amount <> 0)` 포함
  - `CREATE EXTENSION IF NOT EXISTS btree_gist`
  - `memberships` EXCLUDE 제약(`memberships_no_period_overlap`, `daterange(start_date, end_date, '[]')` 겹침 차단, `status in ('active','paused')` 한정), `months`/`pause_used` 컬럼, monthly/pass10 CHECK 제약, pause 날짜 CHECK, `status='paused'` 시 `pause_*` NOT NULL CHECK
  - `branches.name CHECK length 1~50`, `branches.address` UNIQUE + 빈 문자열 CHECK, `branches.deleted_at`
  - `members.name CHECK length 1~100`, `members.phone CHECK ^[0-9]{11}$`, `members.birth_date NOT NULL`, `phone_last4` generated column + `(branch_id, phone) WHERE deleted_at IS NULL` 부분 unique + `members.deleted_at` (단독 phone 인덱스는 두지 않음)
  - `admins.deleted_at`, `password_hash` (UNIQUE 없음), `failed_login_count`, `locked_until`, `temp_password_expires_at` 컬럼
  - `check_ins.membership_id NOT NULL`
  - `idempotency_keys` 테이블 (회원권 부여·환불·bulk-extend 공용)
  - `revoked_refresh_tokens` 테이블 (jti PK, admin_id FK, revoked_at)
  - `admin_audit_logs` 테이블 (action, target_type/id, ip, user_agent, metadata jsonb) — 1년 보관
  - `membership_events.action`에 `'unpause'`/`'cancel_pause'` 포함, `actual_pause_end` 컬럼
- [ ] `00002_updated_at_trigger.sql` 작성 (Up/Down 모두)
- [ ] 시드 SQL 작성 (해시·지점 이름·주소는 환경변수에서 `psql -v`로 주입). 백엔드의 `cmd/hashpw`로 해시 생성하는 절차는 `docs/DEV_SETUP.md`에 기재
- [ ] 로컬 컨테이너에서 `goose up` → `goose down` → `goose up` 왕복 통과 확인

### 검증 기준
- 빈 DB에서 `goose -dir db/migrations postgres "$DB_URL" up` 성공
- `goose down` 으로 모든 마이그레이션 롤백 성공
- 동일 회원에 기간 겹치는 active/paused 회원권 INSERT 시 EXCLUDE 제약이 23P01로 거부, 겹치지 않는 미래 회원권 INSERT는 통과
- `check_ins.membership_id NULL` INSERT 시 NOT NULL CHECK 거부
- `admins` role/branch_id CHECK 제약이 잘못된 조합을 거부
- `payments.method` CHECK 제약이 `cash|card` 외 값 거부, `payments.amount = 0` INSERT 거부, `paid_at` 인덱스 존재 확인
- `members.phone`이 11자리 숫자가 아닐 때 INSERT 거부, `phone_last4`가 자동으로 마지막 4자리로 채워지는지 확인, `birth_date IS NULL` 거부, `name` 100자 초과 거부
- 같은 `branch_id` 안에 동일 phone으로 2개 INSERT 시 부분 unique가 거부, 다른 `branch_id`에는 같은 phone INSERT 가능
- `branches.address`에 중복 INSERT 거부(NULL은 여러 개 허용), 빈 문자열·공백만 있는 문자열 CHECK 거부, `branches.name` 50자 초과 거부
- `branches.deleted_at`/`members.deleted_at`/`admins.deleted_at` 모두 NULL/timestamp 허용 + 인덱스 존재
- `memberships`에 `type='monthly'`인데 `months IS NULL`이거나 `type='pass10'`인데 `remaining IS NULL` INSERT 거부
- `memberships`에 `status='paused'`이면서 `pause_start_date IS NULL` 또는 `pause_end_date IS NULL`인 row INSERT 거부
- `idempotency_keys`, `revoked_refresh_tokens`, `admin_audit_logs` 테이블 존재 확인
- `admins.password_hash`에 UNIQUE 제약이 없는지 확인(중복 해시 INSERT가 통과)

---

## Phase 2 — Backend (Go + Gin) 스캐폴드 (TDD)
**의존**: Phase 1 통과 (실제 DB에 마이그레이션 적용 가능 상태).
**참조**: `backend/CLAUDE.md`, `docs/TESTING.md`

**진행 방식**: Red → Green → Refactor. 모든 엔드포인트는 **테스트 먼저** 작성. 정상 케이스 1개당 에러/엣지 케이스 N개를 `docs/TESTING.md` 카탈로그에서 가져와 함께 작성. 테스트 없는 핸들러는 머지 금지.

### 산출물
- `backend/go.mod`, `backend/cmd/server/main.go` — 설정 로드 + DB 풀 + Gin 라우터 기동 + 인-프로세스 cron
- `backend/cmd/hashpw/main.go` — bcrypt 해시 생성 CLI
- `internal/config` — 환경변수 로더 (`DATABASE_URL`, `JWT_SECRET`, `PORT`, `CORS_ORIGIN`)
- `internal/http/router.go` — 라우트 등록 + 미들웨어(logging, recovery, auth) 연결
- `internal/http/middleware/{auth,logging,recovery}.go`
- `internal/auth` — JWT 발급·검증, `must_change_password` 가드
- `internal/batch` — 자정 KST 회원권 상태 전환(만료/정지 복귀)
- `internal/repo/*_repo.go` — 인터페이스 + pgx 구현 스텁 (한 메서드씩 동작)
- `internal/domain/*.go` — 엔티티 + 서비스 인터페이스
- `internal/http/{admins,branches,members,memberships,checkins,sales}.go` — 핸들러 스텁(최소 1개 GET 동작)

### 작업 항목
- [ ] `go mod init` + 핵심 의존성 추가 (`gin`, `pgx/v5`, `bcrypt`, `golang-jwt`, `robfig/cron/v3`, `google/uuid`, `stretchr/testify`, `slog` 표준)
- [ ] **테스트 인프라 먼저** (TDD 의존):
  - `internal/testutil` 패키지: `SetupDB(t)`, `TruncateAll(t, db)`, `CreateAdmin/Branch/Member/Membership(t, db, opts)`, `Login(t, server, ...)`, `AuthRequest(t, ...)`, `FreezeTime(t, instant)`
  - `TEST_DATABASE_URL` 로더 + `TestMain`에서 goose up
  - `Clock`/`UUIDGen` 인터페이스 (시간·랜덤 주입)
  - `//go:build integration` 빌드 태그로 통합 테스트 분리
- [ ] `internal/apperr` 패키지 + 단위 테스트:
  - `AppError{Code, Message, Status, Cause}` 타입
  - `FromDBError(err)` — pgx 에러 코드(`23505`/`23P01`/`23514`/`23502`/`23503`/`40001`/`40P01`)를 `AppError`로 매핑
  - `IsCode(err, code)` 헬퍼
- [ ] `cmd/server/main.go` — DB 풀 + 라우터 + graceful shutdown(SIGTERM → cron → HTTP → DB 풀 순서)
- [ ] 헬스체크 `GET /api/healthz` (DB ping 포함, 핸들러 테스트로 커버)
- [ ] `POST /api/admin/login` (bcrypt 검증 + access/refresh JWT 발급 + `must_change_password` 응답 + `temp_password_expires_at` 만료 시 401 `TEMP_PASSWORD_EXPIRED` + 잠금 카운터/`locked_until` 처리)
- [ ] Auth 미들웨어 — access claim 검증(필수 필드 존재 + 형식) + **매 요청마다 `admins.deleted_at IS NULL` 확인**(soft-deleted admin 즉시 차단)
- [ ] `POST /api/admin/refresh` (refresh JWT 검증 + `revoked_refresh_tokens` 조회 + 새 access 토큰 발급)
- [ ] `POST /api/admin/logout` (refresh JWT의 `jti`를 `revoked_refresh_tokens`에 INSERT)
- [ ] `POST /api/admin/password` — 현재 비밀번호 재입력 검증(`must_change_password=true`인 첫 로그인 포함) + 새 해시 갱신(8자+영숫자 혼합) + 플래그/`temp_password_expires_at` 해제 + 해당 사용자 refresh 토큰 일괄 무효화
- [ ] `POST /api/admins/:id/reset-password` — 전역 전용 + 12자 영숫자 임시 비번 생성(헷갈리는 문자 제외) + `must_change_password=true` + `temp_password_expires_at=now()+24h` + `failed_login_count=0` + `locked_until=NULL` + 응답 1회 plaintext + expires_at 반환
- [ ] `PATCH /api/admins/:id` — 전역 전용. username/role/branch_id 변경. 본인 role/branch_id 변경 시 409. branch_id 변경 시 해당 사용자 refresh 토큰 무효화
- [ ] 로그인 잠금 미들웨어 + IP 단위 rate limit(15분당 60회) + 잠금 해제 후 카운터 리셋 정책(성공 시 0, 실패 시 1부터 누적)
- [ ] CORS 미들웨어 — `Authorization`, `Content-Type`, `Idempotency-Key`, `X-Request-ID` 허용 헤더 + OPTIONS preflight 204 응답 + `Access-Control-Max-Age: 86400`
- [ ] Request ID 미들웨어 — `X-Request-ID` 헤더 발급/전파, 모든 로그·audit metadata에 포함
- [ ] 트랜잭션 retry 헬퍼 — `40001`/`40P01` 시 최대 3회 backoff 재시도
- [ ] panic recovery 미들웨어 — stack trace는 로그만, 응답은 500 `INTERNAL`로 통일
- [ ] HTTP 서버 timeout 설정 (read header 5s, read 10s, write 30s, idle 60s)
- [ ] body size limit (`gin.MaxBytesReader` 1MB)
- [ ] graceful shutdown — SIGTERM 시 cron → HTTP → DB 풀 순서
- [ ] pgxpool 설정 (max 25, min 2, idle 5m, lifetime 1h, 연결 시 `SET TIME ZONE 'UTC'`)
- [ ] trusted proxy 등록 (호스팅 결정 후 OPERATIONS에 CIDR 추가)
- [ ] 감사 로그 미들웨어 — 로그인/로그아웃/비번 변경/리셋/관리자 CRUD/지점 CRUD를 `admin_audit_logs`에 자동 INSERT
- [ ] 응답 timestamp KST(`+09:00`) 직렬화 헬퍼
- [ ] `GET /api/branches` (키오스크 초기화·관리자 공용)
- [ ] `GET /api/members/search` — `mode=name|phone|memberId`. name은 prefix·최소 2자, phone은 4자리 정확, memberId는 정확 일치. **활성 회원권 있는 회원만** 반환, 최근 체크인 순 정렬, limit 20 + truncated 플래그.
- [ ] `POST /api/check-ins` — 5초 LRU 멱등성 캐시 + 활성 회원권 `WHERE status='active' AND start_date <= 오늘 AND end_date >= 오늘 FOR UPDATE`. 잠긴 row 없으면 422 `NO_ACTIVE_MEMBERSHIP`. status는 active이지만 `start_date > 오늘`이면 422 `MEMBERSHIP_NOT_STARTED`. 횟수권은 같은 회원·같은 날짜·같은 지점 첫 row일 때만 `remaining -= 1`.
- [ ] `GET /api/check-ins/today-count` (키오스크 헤더용 — KST 기준 오늘 해당 지점 카운트)
- [ ] 회원 CRUD (`GET/POST/PATCH/DELETE /api/members`) — `DELETE`는 soft delete. `PATCH`는 `name`/`phone`/`birth_date`만 변경 허용(`branch_id` 등 그 외 필드는 무시). `(branch_id, phone) WHERE deleted_at IS NULL` 중복 시 409 `PHONE_DUPLICATE`. `GET` 응답에 `branch_name` 포함. cursor 페이지네이션.
- [ ] `POST /api/members/:id/memberships` — **Idempotency-Key 헤더 필수**(UUIDv4 검증). `monthly` + `months` 또는 `pass10` 입력 → end_date 자동 계산. `start_date >= 오늘` 강제. `payments(amount > 0)` 한 트랜잭션. **`paid_at`은 서버 KST 오늘로 자동 설정**(클라 입력 무시), **`branch_id`는 회원의 branch_id로 자동**(클라 입력 무시). amount<=0이면 400. soft-deleted/다른 지점 회원이면 404. EXCLUDE 위반(`23P01`)은 409 `MEMBERSHIP_PERIOD_OVERLAP`로 변환.
- [ ] `POST /api/memberships/:id/pause` — `pause_used=true`면 409. 시작/종료 검증(`start_date >= 회원권.start_date` 포함). `pause_start_date`가 오늘이면 즉시 paused, 미래면 active 유지. `end_date` 연장 + `pause_used=true`. EXCLUDE 위반은 409 `MEMBERSHIP_PERIOD_OVERLAP`.
- [ ] `POST /api/memberships/:id/unpause` — paused 한정. `actual_pause_end=오늘`로 `end_date` 단축 + `status='active'`.
- [ ] `POST /api/memberships/:id/cancel-pause` — active 상태에서 미래 예약된 정지 취소. `pause_used=false`로 되돌림 + 만료일 복원.
- [ ] `POST /api/memberships/:id/refund` — **Idempotency-Key 헤더 필수**. 호출 가능 status: active/paused/active+미래시작. expired는 409 `MEMBERSHIP_ALREADY_EXPIRED`, refunded는 409. `status='refunded'` + `payments`에 음수 row(`paid_at`은 서버 KST 오늘 자동). (전체 환불만, 부분 환불 미지원)
- [ ] `POST /api/memberships/bulk-extend` — 전역 전용 + `Idempotency-Key` 헤더 필수. `days` 1~90 검증(외 400 `INVALID_EXTEND_DAYS`). 대상은 `status IN ('active','paused')`, paused 및 active+미래 예약 정지(pause_used=true AND pause_start_date>오늘)는 `pause_start_date`/`pause_end_date`도 같이 +days 이동. EXCLUDE 충돌 시 409 `MEMBERSHIP_PERIOD_OVERLAP` + 응답 body에 `first_conflict_membership_id` + 전체 롤백. `idempotency_keys` 같은 키·다른 body는 409 `IDEMPOTENCY_KEY_CONFLICT`.
- [ ] `GET /api/sales/summary` — 전역 전용 + `payments` 일/월·수단별 집계, `gross_total`/`refund_total`/`net_total` 분리.
- [ ] `GET/POST/PATCH/DELETE /api/admins` — 전역 전용. soft delete + 본인 삭제 차단(409). 삭제·branch_id 변경 시 해당 사용자 refresh 토큰 무효화.
- [ ] `DELETE /api/branches/:id` — 사용 중(`deleted_at IS NULL`인 회원/관리자 존재) 시 409, 통과 시 `deleted_at=now()` soft delete
- [ ] `PATCH /api/branches/:id` — 다른 지점 주소와 충돌 시 409 `ADDRESS_DUPLICATE`
- [ ] 횟수권 차감 후 `remaining=0`이면 같은 트랜잭션에서 `status='expired'`
- [ ] `internal/batch` — 자정 KST **00:01** 4 트랜잭션(`active→expired`, `paused→active`, `active→paused` 도래) + 정리 잡(`idempotency_keys` 24h, `revoked_refresh_tokens` 15h, `admin_audit_logs` 1년) 구현 + `./bin/server batch run-expiry` 명령. 모든 SQL은 `(now() AT TIME ZONE 'Asia/Seoul')::date` 사용. cron 표현식 `1 0 * * *` (KST).
- [ ] `cmd/hashpw` — 인자 비밀번호를 bcrypt cost 12로 해싱해 stdout 출력
- [ ] 에러 응답 통일 헬퍼 + 코드 카탈로그(`docs/API.md` 참조)
- [ ] 지점 관리자 `branch_id` 강제 미들웨어/서비스 가드 + 전역 전용 라우트 가드
- [ ] 운영(`APP_ENV=prod`)에서 HSTS 헤더, 개발에선 미적용
- [ ] 위 엔드포인트별 핸들러 테스트 1개씩 (Gin `httptest`)

### 검증 기준
- `go build ./...` 통과
- `go test -race ./...` 통과 (실제 Postgres 사용)
- `go test -short ./...`(단위만) 통과 — CI 빠른 피드백용
- `docs/TESTING.md`의 모든 엔드포인트 카탈로그 항목이 테스트로 존재 (체크리스트 통과)
- 커버리지: 핸들러 ≥80%, 도메인 ≥90% (참고선)
- 시드 관리자로 로그인 → access/refresh 토큰 수신 → `must_change_password=true` 확인
- access 만료 시 refresh로 새 access 수신 → 기존 요청 재시도 성공
- 로그아웃 호출 후 같은 refresh 토큰으로 refresh 시 401 (jti가 `revoked_refresh_tokens`에 존재)
- 비번 변경 후 다른 라우트 접근 가능 + 변경 전 refresh 토큰은 무효
- 현재 비번을 틀리게 5번 입력 → 6번째 시도는 정확한 비번이어도 401 `ACCOUNT_LOCKED`. 15분 후 다시 가능.
- 전역 관리자가 지점 관리자 `reset-password` 호출 → 12자 임시 비번 응답 → 그 비번으로 로그인 → 비번 변경 강제 흐름 재진입
- `reset-password` 24h 후 같은 임시 비번으로 로그인 시 401 `TEMP_PASSWORD_EXPIRED`
- 약한 비번(7자, 영문만, 숫자만)으로 변경 시도 시 400 `WEAK_PASSWORD`
- 전역 관리자가 지점 관리자 PATCH로 branch_id 변경 → 해당 사용자 기존 refresh 토큰 무효
- 본인 PATCH로 role/branch_id 변경 시도 → 409 `CANNOT_MODIFY_SELF_ROLE`
- `role='branch'` 토큰으로 다른 지점 자원 접근 시 403
- `role='branch'` 토큰으로 `/api/sales/summary`, `/api/admins/*`, `/api/memberships/bulk-extend` 접근 시 403
- 로그인 성공/실패·비번 변경·관리자 CRUD·지점 CRUD가 `admin_audit_logs`에 자동 기록되는지 확인
- 회원권 부여 시 `amount <= 0`이면 400, `amount > 0`이면 한 트랜잭션으로 `payments` row 생성
- 환불 후 매출 합계가 음수 row 반영해 자동 보정되는지 확인
- 키오스크 검색은 활성 회원권 없는 회원을 반환하지 않음(`expired`, `paused`, `refunded`, 회원권 없음 모두 제외)
- 활성 회원권이 없는 회원에 `POST /api/check-ins` 시 422 `NO_ACTIVE_MEMBERSHIP`
- 정지 중인 회원권에 체크인 시도 시 422
- 횟수권 회원이 같은 날 두 번 체크인했을 때 `check_ins` row는 2개, `memberships.remaining`은 1만 감소
- 횟수권 회원이 마지막 체크인을 해 `remaining=0`이 된 트랜잭션에서 `status='expired'`로 같이 전환
- 같은 회원·같은 지점에 5초 내 두 번 체크인 요청 → 같은 응답 반환, `check_ins` row는 1개만 생성
- 같은 회원권에 정지 두 번째 시도 시 409 `PAUSE_ALREADY_USED`
- 정지 중(`status='paused'`)인 회원권에 unpause 호출 시 `end_date`가 잔여 정지 일수만큼 단축
- 미래 예약 정지(`status='active'`, `pause_used=true`, `pause_start_date>오늘`)에 cancel-pause 호출 시 `end_date` 복원 + `pause_used=false`. 도래한 정지·active 상태에서는 409 `PAUSE_NOT_SCHEDULED`
- 회원권 부여 시 `start_date`가 어제 → 400 `INVALID_START_DATE`
- 키오스크 search 결과가 20명 초과면 응답에 `truncated: true` 포함
- `aggregate=daily`는 같은 회원이 같은 날 두 번 체크인해도 1 row, `raw`(기본)는 2 row, 잘못된 값은 400. 92일 초과 범위는 400 `RANGE_TOO_LARGE`
- `DELETE /api/branches/:id`가 회원/관리자가 있는 지점에 대해 409, 없는 지점은 `deleted_at` 세팅 후 200
- `PATCH /api/branches/:id`가 다른 지점 주소와 충돌 시 409 `ADDRESS_DUPLICATE`
- `DELETE /api/members/:id`가 soft delete로 처리되고 검색에서 제외됨
- `PATCH /api/members/:id`로 `branch_id` 변경 요청 시 무시됨(다른 필드만 갱신)
- bulk-extend를 같은 `Idempotency-Key`로 같은 body 두 번 호출하면 두 번째는 처리 없이 첫 응답 반환, 만료일은 한 번만 연장. 같은 키·다른 body는 409 `IDEMPOTENCY_KEY_CONFLICT`
- `GET /api/members?cursor=...&limit=20`이 동작, `limit=200`이면 400 `INVALID_LIMIT`. 잘못된 cursor는 400 `INVALID_CURSOR`
- 매출 응답에 `gross_total`/`refund_total`/`net_total` 분리 노출
- 자정 배치 수동 실행(`run-expiry`)으로 만료된 active가 expired, 종료된 paused가 active, 도래한 예약 정지가 paused로 전환되고, 24h 지난 idempotency_keys / 15h 지난 revoked_refresh_tokens / 1년 지난 admin_audit_logs가 정리됨
- 회원권 부여 시 같은 회원에 기간이 겹치는 active/paused 회원권이 있으면 409 `MEMBERSHIP_PERIOD_OVERLAP`. 겹치지 않는 미래 회원권은 통과 → 현재 회원권 만료 후 자동으로 새 회원권이 사용됨
- 회원권 부여·환불 응답의 `paid_at`이 항상 서버 KST 오늘(클라가 다른 값 보내도 무시), `branch_id`는 회원의 branch_id로 자동
- expired 회원권에 환불 시도 → 409 `MEMBERSHIP_ALREADY_EXPIRED`. active/paused/active+미래시작은 환불 가능
- pause 등록 시 `start_date < memberships.start_date`이면 400 `INVALID_PAUSE_RANGE` (미래 시작 회원권의 시작 전 정지 차단)
- pause 등록으로 end_date 연장 결과가 미래 회원권과 겹치면 409 `MEMBERSHIP_PERIOD_OVERLAP` (롤백)
- bulk-extend가 active+미래 예약 정지 회원권에 적용 시 pause_start_date/pause_end_date도 같이 +days 이동
- bulk-extend 충돌 시 응답 body에 `first_conflict_membership_id` 포함
- soft-deleted admin의 access 토큰으로 호출 시 즉시 401 (Auth 미들웨어가 admin row 검증)
- 다른 지점 회원·회원권에 지점 관리자가 접근 시 404 (403 아님)
- soft-deleted 회원에 회원권 부여 시도 → 404
- Idempotency-Key 값이 UUIDv4 아님 → 400 `INVALID_IDEMPOTENCY_KEY`
- access claim 필수 필드 누락된 토큰 → 401
- 미래 시작 회원권에 체크인 시도 시 422 `MEMBERSHIP_NOT_STARTED`
- 회원권 부여·환불에 `Idempotency-Key` 누락 시 400, 같은 키·다른 body 재호출 시 409
- bulk-extend `days=0` 또는 `days=91` → 400 `INVALID_EXTEND_DAYS`. 대상에 paused 회원권이 포함되어 `pause_end_date`도 같이 +days 이동
- 모든 응답 timestamp가 `+09:00` 오프셋, 응답 헤더에 `X-Request-ID` 포함
- panic 트리거 후 응답이 500 `INTERNAL`로 통일되고 stack trace는 응답에 노출되지 않음
- bulk-extend 후 EXCLUDE 위반이 발생하지 않는지 확인(연장이 다른 미래 회원권과 겹쳐도 +days라 자체 모순이 생기지 않는다는 점은 트랜잭션 안에서 별도 검증 필요)
- 동시 체크인을 같은 회원권에 동시에 보내도 `40001`/`40P01`이면 자동 retry로 둘 중 하나만 성공, 다른 하나는 5초 멱등성 캐시로 같은 응답 받음

---

## Phase 3 — Frontend (Vite + React) 스캐폴드 + 화면 구현 + 자동 테스트
**의존**: Phase 2 통과 (백엔드 API가 로컬에서 응답).
**참조**: `frontend/CLAUDE.md`, `docs/UI_GUIDE.md`, `frontend/ui-design/` 시안 (정본), `docs/ADR.md` ADR-017(라이브러리 화이트리스트)

### 산출물
- `frontend/package.json`, `vite.config.ts`, `tailwind.config.ts`, `tsconfig.json` (strict)
- `src/main.tsx`, `src/App.tsx` — React Router 설정
- `src/api/` — fetch 래퍼 (JWT 헤더 자동 + 에러 정규화)
- `src/context/{BranchContext,AuthContext}.tsx`
- `src/hooks/{useBranch,useAuth,useSpeechRecognition}.ts`
- `src/pages/kiosk/*` 빈 컴포넌트 + 플로우 라우팅 (Idle → InputSelect → Voice/Typing → MemberPick → CheckInDone). Idle/InputSelect 헤더에 오늘 체크인 카운터.
- `src/pages/admin/*` 빈 컴포넌트 + 보호 라우트 (Login → PasswordChange 가드 → Members/Memberships/CheckIns/Sales/BulkExtend/Branches). Sales/BulkExtend/Branches는 `role='global'` 가드.
- `src/components/` 공통 UI (Button, Card, Table, NumberPad — UI_GUIDE 토큰 그대로)
- `public/manifest.webmanifest` — `display: fullscreen`, 키오스크 아이콘 + `start_url: /`
- `.env.example` — `VITE_API_URL`
- **화면 구현 완성형**: 키오스크 7화면(BranchSetup·Idle·InputSelect·VoiceSearch·TypingSearch·MemberPick·CheckInDone) + 관리자 전 화면(Login·PasswordChange·Members·Memberships(상세/부여/정지/조기활성화/취소/환불)·CheckIns·Sales·BulkExtend·Admins·Branches). 빈 컴포넌트 X.
- **시안 정합성**: `frontend/ui-design/*.jsx`(kiosk-screens-1/2, admin-shell, admin-members, admin-plan-grant, admin-sales-login)와 픽셀 단위 정합. 허용 오차 — 색상은 토큰 일치, 간격은 ±4px 이하. `frontend/ui-design/styles.css`를 `src/styles/tokens.css`로 복사(직접 수정 금지, Tailwind config가 CSS 변수를 참조).
- **Vitest 단위/컴포넌트 테스트**: `useSpeechRecognition`·`useIdleTimeout`·`useIdempotencyKey` 훅, `api/client` 래퍼(401 자동 refresh 재시도), MemberPick 마스킹(`010-****-1234`/`**-04-15`/`#1234`), 폼 검증(전화 11자리·비번 강도·days 1~90·monthly months·amount > 0). MSW로 API 모킹.
- **Playwright e2e**: 골든 패스 시나리오 12~15개 — 관리자 로그인→비번변경→대시보드, 회원 등록→회원권 부여→체크인→매출 확인, 키오스크 검색→체크인→오늘 카운트 +1, 정지/환불, 잠금 카운트다운, MEMBERSHIP_NOT_STARTED, truncated 배너, 5초 롱프레스 BranchSetup 복귀, idle 10초 타임아웃, 토큰 자동 refresh.
- **PWA 아이콘**: `frontend/ui-design/assets/`에 사용자 제공 PWA 아이콘(192/512/maskable) 연결 → `manifest.webmanifest`에 등록.

### 작업 항목
- [ ] Vite 초기화 + Tailwind + UI_GUIDE 색상/토큰을 `tailwind.config.ts`에 등록
- [ ] React Router 라우트 트리 (`/`, `/kiosk/*`, `/admin/*`)
- [ ] BranchContext: `localStorage.branchId` 로드/저장 + 미설정 시 `/kiosk/setup` 리다이렉트
- [ ] AuthContext: access/refresh JWT 보존 + `must_change_password` 시 `/admin/password` 강제
- [ ] API fetch 래퍼: access 헤더 자동 첨부 + 401 시 자동 refresh + 재시도, refresh도 401이면 강제 로그아웃
- [ ] 로그아웃 액션: `POST /api/admin/logout` 호출 + localStorage 토큰 삭제
- [ ] 키오스크 우상단 5초 롱프레스 → 지점 재설정 진입
- [ ] `useSpeechRecognition` 훅 — Web Speech API 래퍼, 가용성 체크(미지원 시 음성 버튼 숨김), 마이크 권한 거부 시 즉시 타이핑 폴백, 3회 실패 카운터
- [ ] `useIdleTimeout(10000)` 훅 — 키오스크 진행 화면(InputSelect/VoiceSearch/TypingSearch/MemberPick)에서 10초 무입력 시 Idle 복귀
- [ ] `useIdempotencyKey` 훅 — 폼 마운트 시 `crypto.randomUUID()` 발급, 성공 후 새 키 발급
- [ ] `TypingSearch` — 이름 / 전화 뒷자리 4자리 / 회원 번호 3개 탭 분기. 이름은 최소 2자 입력 후 활성화, 4자리는 자동 검색, 회원 번호는 확인 버튼.
- [ ] `MemberPick` — 검색 결과 0건일 때 "활성 회원권이 없거나 등록되지 않은 회원입니다" 안내 후 Idle 복귀. 결과는 서버 정렬(최근 체크인 순) 그대로 렌더.
- [ ] 키오스크 헤더 "오늘 체크인 N명" 카운터 (today-count 쿼리 + 체크인 성공 시 invalidate)
- [ ] 키오스크 풀스크린·`touch-action: manipulation` 적용
- [ ] 회원권 부여 폼 — `monthly`+`months`(1/3/6/12 프리셋), `pass10`(자동 2개월·10회). 결제(금액 양수·수단·결제일) 입력 통합. 만료일 미리보기.
- [ ] 정지 폼 — 시작일·종료일·사유. 이미 정지 이력 있으면(서버 응답 또는 회원권 정보로 판단) 비활성.
- [ ] 정지 조기 활성화(unpause) UI — confirm 모달 + 사유 입력
- [ ] 매출 페이지(`/admin/sales`) — 일/월 토글 + 수단별 분리 + 지점 필터 (전역 전용)
- [ ] 대량 연장(`BulkExtend`) — confirm 모달 + Idempotency-Key 자동 첨부 + 처리 중 버튼 비활성화
- [ ] 관리자 계정 페이지(`/admin/admins`) — 목록·생성·삭제·비번 리셋 (전역 전용)
- [ ] 회원 폼 — 전화번호 11자리 숫자 입력 마스크, 표시 시 `010-1234-5678` 포맷
- [ ] 회원 목록·체크인 이력 페이지 — cursor 페이지네이션(20건씩, "더 보기")
- [ ] 관리자 반응형 레이아웃 (데스크톱 사이드 네비 ↔ 모바일 카드 스택)
- [ ] 로그인 폼 — `ACCOUNT_LOCKED` 응답 시 잠금 해제 시각까지 카운트다운
- [ ] PWA 매니페스트 + `vite-plugin-pwa`(또는 수동 link 태그) 적용 → Lighthouse "설치 가능" 통과
- [ ] 빈 페이지에서 백엔드 헬스체크·로그인이 실제로 통신되는지 확인
- [ ] **관리자 화면 전체 구현** (회원·회원권·체크인·매출·대량 연장·지점·관리자) — 빈 컴포넌트 단계 종료
- [ ] **회원권 부여/정지/조기활성화(unpause)/취소(cancel-pause)/환불 폼** + `useIdempotencyKey` 헤더 첨부
- [ ] **매출 페이지 gross/refund/net 분리 표시** (전역 전용, 카드 3개 + by_method/by_day 분리표)
- [ ] **BulkExtend 폼** (전역 전용, days 1~90 정수 한도, MEMBERSHIP_PERIOD_OVERLAP 시 `first_conflict_membership_id` 표시)
- [ ] **관리자 CRUD + 비번 리셋** (임시비번 1회 표시·복사·`expires_at` 안내, 본인 role/branch_id 비활성)
- [ ] **회원 상세 한 화면** (헤더 + active 회원권 카드 + 회원권 이력 + 결제 이력, `GET /api/members/:id` 단일 호출)
- [ ] **회원권 상세 페이지** (`GET /api/memberships/:id` 결과로 폼 노출 여부 결정, events + payments)
- [ ] **`frontend/ui-design/styles.css` → `src/styles/tokens.css` 복사** + Tailwind config가 CSS 변수 참조
- [ ] **Vitest + MSW 셋업** + 핵심 훅·API 래퍼·마스킹·폼 검증 단위/컴포넌트 테스트
- [ ] **Playwright 셋업** + e2e 골든 패스 시나리오 12~15개
- [ ] **PWA 아이콘** (사용자 제공) manifest 연결 — 192/512/maskable

### 검증 기준
- `pnpm build` 통과 (TS strict 에러 0)
- `pnpm lint` 통과
- 로컬에서 `/` 진입 시 지점 미설정이면 setup 화면, 설정 후 새로고침에도 유지
- `/admin/login` → 시드 관리자 로그인 → 비번 변경 화면 강제 진입 → 변경 후 대시보드 이동
- access 만료(30분) 후 API 호출 시 자동으로 refresh → 새 토큰으로 재시도 성공
- 로그아웃 후 `localStorage`의 토큰 제거 + refresh 시도 401
- Chrome에서 음성 인식 권한 요청 + 인식 결과 콘솔 확인 (브라우저 한정)
- `role='branch'` 계정 로그인 시 사이드 네비에 Sales/BulkExtend/Branches/Admins 메뉴 비표시
- 키오스크 진행 화면에서 10초 동안 터치 없으면 Idle로 자동 복귀
- 마이크 권한 거부 시 자동으로 TypingSearch로 전환되는지 확인
- 키오스크 검색에서 활성 회원권 없는 회원은 결과에 나타나지 않음
- 키오스크 검색을 회원 번호 / 전화 4자리 / 이름 prefix 모두 시도해 동작 확인
- 키오스크 헤더 카운터가 체크인 직후 즉시 +1로 갱신
- 태블릿 Chrome에서 "홈 화면에 추가" 후 아이콘 진입 시 주소창·탭 없이 풀스크린 표시
- BulkExtend 폼에서 같은 세션에 두 번 제출 시 같은 Idempotency-Key가 전송되어 한 번만 적용
- `pnpm build` 통과 (TS strict 에러 0, 번들 산출물 생성)
- `pnpm test` (Vitest) 모두 통과 — 훅·API 래퍼·마스킹·폼 검증 커버
- `pnpm test:e2e` (Playwright) 모두 통과 — 골든 패스 12~15개 시나리오
- 브라우저에서 키오스크/관리자 골든 패스를 수동으로 한 번 클릭(검증 자동화 외 시각 확인)
- `frontend/ui-design/*.jsx` 시안과 픽셀 단위 정합 — 색상 토큰 일치, 간격 ±4px 이하
- PWA 매니페스트의 아이콘(192/512/maskable)이 `frontend/ui-design/assets/`의 사용자 제공 파일과 연결됨

---

## Phase 4 — 배포 환경 결정 (Phase 3 이후)
**의존**: Phase 3 통과.
- [ ] 호스팅 후보 비교 (Fly.io / Railway / Render) — 비용·DB 통합·배포 난이도
- [ ] 결정 사항을 `docs/ADR.md`에 ADR-010으로 추가
- [ ] CI(빌드·테스트) + 배포 파이프라인 정의 (Phase 3에서 셋업한 Vitest·Playwright 빌드 step을 CI에 통합)
- [x] 태블릿 운영 가이드(기종별 "홈 화면 추가" 절차) — `docs/OPERATIONS.md`에 작성 완료

---

## 사용 규칙
- 항목을 끝낼 때마다 체크하고 같은 PR에 포함시킨다.
- 스펙이 바뀌면 먼저 해당 문서(PRD/ARCHITECTURE/CLAUDE.md)를 수정하고, 이 로드맵의 항목을 그에 맞춰 갱신한다.
- 모든 Phase가 끝나면 이 파일은 통째로 삭제하거나, "완료된 마일스톤" 형태로 1페이지로 축약한다.
