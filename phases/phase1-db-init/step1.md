---
agent: backend
---

# Step 1: DB 마이그레이션 (init + updated_at trigger)

## 목표

빈 PostgreSQL에 운영 스키마를 한 번에 적재하는 goose 마이그레이션 2개를 작성한다. **Up/Down 모두 작동해야 한다** (왕복 가능). 이후 step·Phase의 모든 SQL은 이 스키마를 전제로 한다.

## 읽어야 할 파일

먼저 아래 파일을 읽고 스키마·제약·정책을 정확히 반영하라. 누락되면 Phase 2 검증 기준에서 드러난다.

- `CLAUDE.md` (루트) — 공통 CRITICAL 규칙(soft delete, 시크릿 미커밋 등)
- `db/CLAUDE.md` — **테이블 정의의 정본**. 컬럼·CHECK·인덱스·EXCLUDE 제약·트리거·정책이 모두 여기 있다. 그대로 옮긴다.
- `backend/CLAUDE.md` — 어떤 핸들러가 어떤 컬럼·인덱스·제약 이름을 기대하는지(예: `members_branch_phone_unique`, `memberships_no_period_overlap`) 확인
- `docs/ARCHITECTURE.md` — 데이터 흐름·자정 배치 정책
- `docs/ROADMAP.md` (Phase 1 검증 기준) — 이 step의 AC가 명시되어 있다
- `docs/PRD.md`(필요 시), `docs/ADR.md` ADR 항목 중 스키마 결정 관련

## 작업

### 1. `db/migrations/00001_init.sql`

goose 형식. `-- +goose Up` / `-- +goose Down` 두 섹션을 모두 채운다. Down은 Up의 역순으로 모든 객체를 깔끔히 제거(테이블 → 인덱스 → extension 순서 주의).

다음 객체를 모두 만든다(이름·컬럼은 `db/CLAUDE.md`와 정확히 일치):

- `CREATE EXTENSION IF NOT EXISTS btree_gist;` (memberships EXCLUDE 제약 의존)
- 테이블 (모두 `created_at timestamptz not null default now()`, `updated_at timestamptz not null default now()` 보유. `idempotency_keys`/`revoked_refresh_tokens`/`admin_audit_logs`는 `db/CLAUDE.md`에 정의된 형태대로):
  - `branches` (id, name, address unique + 빈/공백 거부 CHECK, deleted_at, name length 1~50 CHECK)
  - `admins` (username unique, password_hash(UNIQUE 없음), must_change_password default true, temp_password_expires_at, role CHECK in ('global','branch'), branch_id FK, last_login_at, password_updated_at, failed_login_count default 0, locked_until, deleted_at, role/branch_id 조합 CHECK)
  - `members` (branch_id FK, name length 1~100 CHECK, phone CHECK `^[0-9]{11}$`, `phone_last4 text generated always as (right(phone, 4)) stored`, birth_date NOT NULL, deleted_at)
  - `memberships` (member_id FK, type CHECK in ('monthly','pass10'), months, start_date, end_date, remaining, status default 'active' CHECK in ('active','paused','refunded','expired'), pause_start_date, pause_end_date, pause_used default false, monthly/pass10 CHECK, pause 날짜 CHECK, status='paused'면 pause_* NOT NULL CHECK)
  - `membership_events` (membership_id FK, action CHECK in ('pause','unpause','cancel_pause','refund','bulk_extend'), pause_start_date, pause_end_date, actual_pause_end, extend_days, reason NOT NULL, performed_by FK→admins)
  - `check_ins` (member_id FK, branch_id FK, **membership_id NOT NULL** FK, checked_in_at default now())
  - `payments` (membership_id FK, branch_id FK, amount integer CHECK `amount <> 0`, method CHECK in ('cash','card'), paid_at date NOT NULL, memo, performed_by FK→admins)
  - `revoked_refresh_tokens` (jti text PK, admin_id FK, revoked_at default now())
  - `admin_audit_logs` (admin_id FK NULL 허용, action text NOT NULL, target_type, target_id, ip inet, user_agent, metadata jsonb, created_at default now())
  - `idempotency_keys` (key text PK, admin_id FK, endpoint text, request_hash text, response_status int, response_body jsonb, created_at default now())
- 인덱스 (`db/CLAUDE.md`에 명시된 모든 인덱스):
  - `branches(deleted_at)`
  - `admins(deleted_at)`, `admins(branch_id)`
  - `members(branch_id)`, `members(phone_last4)`, `members(deleted_at)`
  - **부분 유니크**: `create unique index members_branch_phone_unique on members (branch_id, phone) where deleted_at is null;`
  - `memberships(member_id)`
  - `membership_events(membership_id)`
  - `check_ins(member_id, checked_in_at)`, `check_ins(branch_id, checked_in_at)`
  - `payments(paid_at)`, `payments(branch_id, paid_at)`, `payments(membership_id)`
  - `revoked_refresh_tokens(admin_id)`, `revoked_refresh_tokens(revoked_at)`
  - `admin_audit_logs(admin_id, created_at)`, `admin_audit_logs(action, created_at)`, `admin_audit_logs(created_at)`
  - `idempotency_keys(created_at)`
- **EXCLUDE 제약** (회원당 active/paused 회원권 기간 중첩 차단):
  ```sql
  alter table memberships
    add constraint memberships_no_period_overlap
    exclude using gist (
      member_id with =,
      daterange(start_date, end_date, '[]') with &&
    ) where (status in ('active', 'paused'));
  ```
- 외래키는 인라인 `references ... (id)` 형태(별도 ALTER 없이). FK 이름은 PostgreSQL 기본값으로 두어도 무방. 단 부분 유니크/EXCLUDE/CHECK/제약 이름은 위에 명시된 값과 정확히 일치시킨다.

### 2. `db/migrations/00002_updated_at_trigger.sql`

goose 형식. Up/Down 모두.

- `set_updated_at()` plpgsql 함수를 정의: `NEW.updated_at = now()` 후 `RETURN NEW`.
- 모든 테이블 중 `updated_at` 컬럼을 가진 것들에 BEFORE UPDATE 트리거를 건다 (즉, `idempotency_keys`/`revoked_refresh_tokens`/`admin_audit_logs`를 제외한 7개: branches, admins, members, memberships, membership_events, check_ins, payments).
- 트리거 이름은 `set_updated_at_<table>` 패턴.
- Down에서는 모든 트리거 + 함수까지 제거.

## 핵심 규칙 (반드시 박는다)

- **soft delete 컬럼 + 인덱스**를 `branches`/`admins`/`members`에만 둔다. `memberships`는 status로 종료 상태를 표현하므로 `deleted_at` 두지 않는다.
- `payments.amount`는 양수(부여)·음수(환불) 모두 허용. **0은 CHECK로 거부**.
- `members.phone_last4`는 generated column(STORED). 직접 INSERT 받지 않는다.
- `members.phone`은 **전역 unique 아님**. `(branch_id, phone) where deleted_at is null` 부분 유니크만 둔다.
- `branches.address`는 unique이지만 NULL은 허용(여러 NULL 가능). 빈 문자열·공백 문자열은 CHECK로 거부 (`address is null or length(trim(address)) > 0`).
- `admins.password_hash`에 **UNIQUE 두지 마라** — bcrypt 솔트가 자연 unique이고, 명시 제약은 안전망 효과 없이 UPDATE 비용만 늘린다.
- EXCLUDE 제약은 **`status in ('active','paused')` 한정**. refunded/expired는 기간 중첩 무관.
- DDL만 작성한다. 데이터 INSERT(시드)는 step2의 일이다.
- `(now() AT TIME ZONE 'Asia/Seoul')::date` 같은 KST 변환 SQL은 자정 배치(Phase 2)에서 사용. 마이그레이션 자체는 KST 의존 없음.

## Acceptance Criteria

`.worktrees/backend/`에서 실행한다고 가정한다 (이 worktree 안에서도 루트 `.env`를 `set -a; source ../../.env; set +a`로 로드 가능). 메인 worktree의 `docker-compose.yml`로 띄운 컨테이너를 그대로 쓴다.

```bash
# 사전: 메인에서 docker compose up -d db 가 떠 있어야 한다.
# 그리고 .env에 DATABASE_URL이 채워져 있어야 한다 (Phase 0).

set -a; source ../../.env; set +a

# Up
goose -dir db/migrations postgres "$DATABASE_URL" up
# Down (모든 마이그레이션 롤백)
goose -dir db/migrations postgres "$DATABASE_URL" down
goose -dir db/migrations postgres "$DATABASE_URL" down
# Up 다시 (왕복 검증)
goose -dir db/migrations postgres "$DATABASE_URL" up

# Go 빌드/테스트는 이 step에서 영향 없음 — 코드 미수정. 그래도 backend 워크트리 정합성 차원에서 한 번 돌려둔다.
cd backend && go build ./... && go test ./... ; cd ..
```

추가로 ad-hoc psql 스모크 테스트를 직접 실행해 다음을 모두 통과시킨다(실패 시 마이그레이션 수정):

1. **EXCLUDE**: 같은 member_id에 기간 겹치는 active 회원권 2건 INSERT → 두 번째가 `23P01`로 거부. 겹치지 않는 미래 회원권 INSERT는 통과.
2. **체크인 NOT NULL**: `check_ins(membership_id)`에 NULL INSERT 거부.
3. **admins CHECK**: `role='global' AND branch_id IS NOT NULL` 또는 `role='branch' AND branch_id IS NULL` 거부.
4. **payments CHECK**: `method='paypal'` 거부, `amount=0` 거부, `paid_at` 인덱스 존재(`\d payments`).
5. **members**: phone 11자리 미만/문자 포함 INSERT 거부, phone_last4가 자동 채워짐, birth_date NULL 거부, name 100자 초과 거부.
6. **부분 유니크**: 같은 branch_id에 같은 phone 2건 INSERT 시 두 번째 거부, 다른 branch_id에는 같은 phone 통과.
7. **branches**: 같은 address 두 번 거부, NULL address 두 개 통과, 공백만 있는 address 거부, name 50자 초과 거부.
8. **memberships CHECK**: `type='monthly'`인데 `months IS NULL` 거부, `type='pass10'`인데 `remaining IS NULL` 거부, `status='paused'`인데 `pause_*` NULL 거부.
9. **admins.password_hash**에 UNIQUE 없는지 확인 (`\d admins`로 인덱스 목록 보고 hash 단독 unique 인덱스 없는지).
10. **테이블 존재**: `\dt`로 10개 테이블이 모두 보이는지.
11. **updated_at 트리거**: 임의 테이블(`branches`)에서 `UPDATE branches SET name = name` 후 `updated_at`이 갱신되었는지 확인.

## 검증 절차

1. 위 AC 명령과 ad-hoc psql 검증을 직접 실행한다.
2. **`code-reviewer` 서브에이전트를 Task tool로 호출해 변경 사항을 검증받는다.** 입력: step 이름(`phase1-db-init/step1`), `git diff HEAD --stat`. PASS 응답이 나와야만 다음 단계로.
3. 결과에 따라 `phases/phase1-db-init/index.json`의 step1 status 업데이트:
   - PASS → `"status": "completed"` + `"summary": "00001_init.sql + 00002_updated_at_trigger.sql 추가; 10개 테이블/인덱스/EXCLUDE/CHECK/부분 유니크/트리거 적재. goose up→down→up 왕복 통과."`
   - 3회 재시도 후에도 실패 → `"status": "error"` + `"error_message"`
   - 사용자 개입 필요(goose 미설치, docker 컨테이너 미기동, .env 미설정 등) → `"status": "blocked"` + `"blocked_reason"` 후 즉시 중단

## 금지사항

- `frontend/` 변경 금지(이 step은 backend worktree).
- 공유 파일(`docs/`, 루트 `.env.example`, `docker-compose.yml`, 루트 `CLAUDE.md`, `.gitignore`, `scripts/`, `.claude/`) 변경 금지 — shared step에서만 처리한다.
- 시드 INSERT를 마이그레이션에 넣지 마라. 시드는 step2에서 별도 SQL로 처리한다(평문 시크릿 방어 + 환경 분리).
- 평문 비밀번호·해시·JWT 시크릿을 SQL/주석/로그에 남기지 마라.
- `goose down` 1회로 모든 마이그레이션이 롤백되어야 한다 — Up에서 만든 객체를 Down이 빼먹지 않도록 점검.
- `members.phone`에 단독 unique·단독 인덱스 추가 금지(부분 유니크가 phone_last4 검색까지 커버하지 않으므로 `phone_last4` 인덱스만 따로 둔다).
- ADR 외 PostgreSQL extension 추가 금지 — `btree_gist` 외에 추가가 정말 필요하면 `blocked` 처리 후 사용자와 ADR 갱신.
- 마이그레이션 파일 안에 호스트·자격증명·환경변수 하드코딩 금지.
