# DB — PostgreSQL 스키마·마이그레이션·시드

## 기술 스택
- PostgreSQL 15+
- 마이그레이션 도구: [goose](https://github.com/pressly/goose) (Go 생태계 표준, 단일 바이너리)

## 디렉토리 구조
```
db/
├── migrations/     # goose SQL 마이그레이션 (파일명: 00001_<name>.sql ...)
├── seeds/          # 초기 시드 SQL (관리자 1인, 샘플 지점)
└── CLAUDE.md
```

## 테이블 (초안)
모든 테이블은 `created_at timestamptz not null default now()`, `updated_at timestamptz not null default now()`를 갖는다. `updated_at`은 공통 트리거로 자동 갱신.

```sql
branches (
  id           bigserial primary key,
  name         text not null check (length(name) between 1 and 50),
  address      text unique check (address is null or length(trim(address)) > 0),  -- NULL 허용(여러 NULL 가능), 비-NULL 값은 unique. 빈/공백 문자열은 거부.
  deleted_at   timestamptz,                       -- soft delete (NULL = 활성)
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now()
);
create index on branches (deleted_at);
-- 모든 조회는 `WHERE deleted_at IS NULL` 필터를 강제. 삭제 시 해당 지점 소속 members/admins(deleted_at IS NULL)가 1개라도 있으면 차단.

members (
  id           bigserial primary key,
  branch_id    bigint not null references branches(id),
  name         text not null check (length(name) between 1 and 100),
  phone        text not null check (phone ~ '^[0-9]{11}$'),  -- 숫자 11자리. 동일인이 여러 지점 가입 가능(전역 unique 아님).
  phone_last4  text generated always as (right(phone, 4)) stored,
  birth_date   date not null,                     -- 동명이인 식별·키오스크 마스킹 표시에 필수
  deleted_at   timestamptz,                       -- soft delete (NULL = 활성)
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now()
);
create index on members (branch_id);
create index on members (phone_last4);
create index on members (deleted_at);
-- 같은 지점 내 같은 번호 중복 가입 차단 (soft-deleted 제외)
create unique index members_branch_phone_unique
  on members (branch_id, phone) where deleted_at is null;
-- 키오스크 "전화 뒷자리 4자리" 검색은 phone_last4 인덱스를 활용. 검색은 항상 branch_id로 필터링.
-- (branch_id, phone) 부분 unique가 phone 검색을 커버하므로 phone 단독 인덱스는 두지 않는다.
-- 모든 조회는 `WHERE deleted_at IS NULL` 필터 강제. 회원 hard delete 금지(payments/check_ins FK 무결성 보호).
-- 회원의 branch_id는 변경하지 않는다(다른 지점 이전은 새 row INSERT). 동일인이 여러 지점 가입 가능.

memberships (
  id                bigserial primary key,
  member_id         bigint not null references members(id),
  type              text not null check (type in ('monthly','pass10')),
  months            int,           -- type='monthly'일 때 개월 수(1,3,6,12 등). end_date = start_date + months month
  start_date        date not null,
  end_date          date not null,
  remaining         int,           -- type='pass10'일 때 사용, 초기 10
  status            text not null default 'active'
                     check (status in ('active','paused','refunded','expired')),
  pause_start_date  date,          -- 정지 시작일(미래 예약 가능, 도래 전엔 status='active' 유지)
  pause_end_date    date,          -- 정지 종료일
  pause_used        boolean not null default false,  -- 한 회원권당 정지 1회 제한
  created_at        timestamptz not null default now(),
  updated_at        timestamptz not null default now(),
  check (
    (type = 'monthly' and months is not null and months > 0 and remaining is null) or
    (type = 'pass10' and months is null and remaining is not null and remaining >= 0)
  ),
  check (
    (pause_start_date is null and pause_end_date is null) or
    (pause_start_date is not null and pause_end_date is not null and pause_start_date <= pause_end_date)
  ),
  -- status='paused'일 때는 pause_*가 반드시 세팅되어 있어야 한다
  check (
    status <> 'paused' or (pause_start_date is not null and pause_end_date is not null)
  )
);
create index on memberships (member_id);
-- 회원권 기간 계산 규칙:
--   monthly: end_date = start_date + months month (예: months=3이면 4/1 시작 → 7/1 만료)
--   pass10:  end_date = start_date + 2 month  (이 기간 내 10회 사용)
-- 회원당 active/paused 회원권은 기간이 겹치지 않아야 한다 — 미리 등록(다음 회원권 선결제) 허용.
-- daterange의 '[]'은 양 끝 포함. WITH &&는 겹침 연산자. btree_gist extension 필요.
create extension if not exists btree_gist;
alter table memberships
  add constraint memberships_no_period_overlap
  exclude using gist (
    member_id with =,
    daterange(start_date, end_date, '[]') with &&
  ) where (status in ('active', 'paused'));
-- 위반 시 PostgreSQL 에러 코드 23P01(exclusion_violation) → 핸들러가 409 MEMBERSHIP_PERIOD_OVERLAP로 변환.

-- 상태 전환 정책:
--   1. 자정 KST 배치: status='active' AND end_date < CURRENT_DATE → 'expired'
--   2. 자정 KST 배치: status='paused' AND pause_end_date < CURRENT_DATE → 'active'
--   3. 자정 KST 배치: status='active' AND pause_start_date = CURRENT_DATE → 'paused' (예약된 정지가 도래)
--   4. 체크인 트랜잭션: 횟수권 차감 후 remaining=0 → 같은 트랜잭션에서 'expired'
--   5. 관리자 정지 처리: pause_start_date/pause_end_date 세팅 + end_date += (pause_end_date - pause_start_date) + pause_used=true.
--      pause_start_date <= 오늘이면 즉시 'paused'로, 미래면 'active' 유지(배치 #3이 처리).
--   6. 관리자 조기 활성화(unpause): status='paused' 상태에서 호출. 오늘 날짜를 actual_pause_end로 사용,
--      end_date -= (pause_end_date - actual_pause_end)로 단축, status='active'로 복귀.
--      예: 4/1~4/7 정지인데 4/6에 활성화 → end_date를 1일 앞당김(원래 5/30이면 5/29).
--
-- 정지는 회원권당 1회만 허용 (pause_used=true가 되면 재정지 불가). 애플리케이션 단에서 검증.

membership_events (
  id                bigserial primary key,
  membership_id     bigint not null references memberships(id),
  action            text not null check (action in ('pause','unpause','cancel_pause','refund','bulk_extend')),
  pause_start_date  date,          -- action='pause'
  pause_end_date    date,          -- action='pause'
  actual_pause_end  date,          -- action='unpause' (정지 도달 후 조기 활성화 실제 종료일)
  extend_days       int,           -- action='bulk_extend' (양수=연장, 음수=조기 활성화로 단축)
  reason            text not null,
  performed_by      bigint not null references admins(id),
  created_at        timestamptz not null default now(),
  updated_at        timestamptz not null default now()
);
create index on membership_events (membership_id);
-- action 종류:
--   'pause'        — 정지 등록(즉시 또는 미래 예약)
--   'unpause'      — 정지 도달(status='paused') 후 조기 활성화
--   'cancel_pause' — 미래 예약된 정지를 도달 전(status='active') 취소
--   'refund'       — 환불
--   'bulk_extend'  — 대량 연장

check_ins (
  id              bigserial primary key,
  member_id       bigint not null references members(id),
  branch_id       bigint not null references branches(id),
  membership_id   bigint not null references memberships(id),  -- 활성 회원권 있을 때만 체크인 가능 → 항상 매핑
  checked_in_at   timestamptz not null default now(),
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now()
);
create index on check_ins (member_id, checked_in_at);
create index on check_ins (branch_id, checked_in_at);
-- 같은 날 중복 체크인 허용 → 유니크 제약 없음
-- 단, 횟수권(memberships.type='pass10') 잔여 차감은 같은 회원·같은 날짜·같은 지점의 첫 row일 때만 1회. 두 번째부터는 row만 추가, 잔여 변동 없음.
-- 키오스크 "오늘 체크인 수"는 (branch_id, checked_in_at::date) 카운트로 조회.

payments (
  id            bigserial primary key,
  membership_id bigint not null references memberships(id),
  branch_id     bigint not null references branches(id),  -- 매출 집계용 비정규화
  amount        integer not null check (amount <> 0),     -- 원 단위, 부여=양수(>0), 환불=음수(<0). 0원 결제 금지.
  method        text not null check (method in ('cash','card')),
  paid_at       date not null,                             -- 매출 귀속일
  memo          text,
  performed_by  bigint not null references admins(id),
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now()
);
create index on payments (paid_at);
create index on payments (branch_id, paid_at);
create index on payments (membership_id);
-- 매출 합계: SUM(amount) by (paid_at, method, branch_id)
-- 무료/0원 결제는 MVP에서 지원하지 않는다(부여 시 amount > 0 강제).

admins (
  id                    bigserial primary key,
  username              text not null unique,
  password_hash         text not null,                   -- bcrypt 해시. (UNIQUE는 두지 않는다 — 솔트로 자연 unique이고 안전망 효과 없음.)
  must_change_password  boolean not null default true,
  temp_password_expires_at timestamptz,                  -- 임시 비밀번호 만료 시각(발급 시 +24h 세팅, 일반 변경 시 NULL).
  role                  text not null check (role in ('global','branch')),
  branch_id             bigint references branches(id),
  last_login_at         timestamptz,
  password_updated_at   timestamptz,
  failed_login_count    int not null default 0,          -- 연속 실패 카운터
  locked_until          timestamptz,                     -- 잠금 해제 시각(NULL = 미잠금)
  deleted_at            timestamptz,                     -- soft delete (NULL = 활성)
  created_at            timestamptz not null default now(),
  updated_at            timestamptz not null default now(),
  check (
    (role = 'global' and branch_id is null) or
    (role = 'branch' and branch_id is not null)
  )
);
create index on admins (deleted_at);
create index on admins (branch_id);

revoked_refresh_tokens (
  -- 로그아웃·비번 변경·계정 soft delete 시 무효화된 refresh JWT 목록.
  -- refresh JWT 만료(15h)보다 오래 보관할 필요 없음 → 자정 배치가 정리.
  jti          text primary key,                          -- refresh JWT의 고유 식별자
  admin_id     bigint not null references admins(id),
  revoked_at   timestamptz not null default now()
);
create index on revoked_refresh_tokens (admin_id);
create index on revoked_refresh_tokens (revoked_at);

admin_audit_logs (
  -- 보안·운영 추적용. 자동 미들웨어가 기록.
  -- 기록 대상: 로그인 성공/실패, 로그아웃, 비번 변경, 비번 리셋, 관리자 CRUD, 지점 CRUD.
  -- 회원·회원권 관련 변경은 membership_events / payments(performed_by)로 이미 추적되므로 여기에 기록하지 않는다.
  id           bigserial primary key,
  admin_id     bigint references admins(id),              -- NULL 허용(로그인 실패 시 username만 알고 admin_id 매칭이 안 될 수 있음)
  action       text not null,                             -- 'login_success' | 'login_failure' | 'logout' | 'password_change' | 'password_reset' | 'admin_create' | 'admin_update' | 'admin_delete' | 'branch_create' | 'branch_update' | 'branch_delete'
  target_type  text,                                      -- 'admin' | 'branch' | NULL
  target_id    bigint,
  ip           inet,
  user_agent   text,
  metadata     jsonb,                                     -- 자유 추가 필드(예: 로그인 실패 사유)
  created_at   timestamptz not null default now()
);
create index on admin_audit_logs (admin_id, created_at);
create index on admin_audit_logs (action, created_at);
create index on admin_audit_logs (created_at);
-- 보존 기간 1년 — 자정 배치가 `created_at < now() - interval '1 year'`인 row를 정리한다.

idempotency_keys (
  -- bulk-extend 등 위험 작업의 멱등성 보장
  key             text primary key,                      -- 클라이언트 발급 UUID
  admin_id        bigint not null references admins(id),
  endpoint        text not null,                          -- 예: 'POST /api/memberships/bulk-extend'
  request_hash    text not null,                          -- 요청 body의 SHA-256
  response_status int not null,
  response_body   jsonb not null,
  created_at      timestamptz not null default now()
);
create index on idempotency_keys (created_at);
-- 24시간 지난 row는 별도 cleanup 잡으로 삭제(또는 자정 배치에 포함).
```

## `updated_at` 자동 갱신 트리거
```sql
create or replace function set_updated_at()
returns trigger as $$
begin
  new.updated_at = now();
  return new;
end;
$$ language plpgsql;

-- 각 테이블에 before update 트리거로 연결
```

## 시드
- `admins`: 전역 관리자 1명 (`username=$SEED_ADMIN_USERNAME`, 초기 비번 해시 삽입, `must_change_password=true`, `role='global'`).
- `branches`: 샘플 지점 1개 (`name=$SEED_BRANCH_NAME`, `address=$SEED_BRANCH_ADDRESS`). 기본값은 각각 `PBOY MMA 본점` / `서울 송파구 가락로 142 지하 1층`이며 `.env`로 주입.

### 시드용 bcrypt 해시 생성
시드 SQL에는 평문 비밀번호를 절대 두지 않는다. 해시는 백엔드에 포함된 CLI로 생성한다.
```
# 1) .env에 SEED_ADMIN_PASSWORD 설정 후
go run ./backend/cmd/hashpw "$SEED_ADMIN_PASSWORD"
# → $2a$12$... 같은 문자열을 출력. 이를 SEED_ADMIN_PASSWORD_HASH로 .env에 넣는다.

# 2) 시드 적용 (psql 변수 주입)
psql "$DATABASE_URL" \
  -v admin_username="$SEED_ADMIN_USERNAME" \
  -v admin_password_hash="$SEED_ADMIN_PASSWORD_HASH" \
  -v branch_name="$SEED_BRANCH_NAME" \
  -v branch_address="$SEED_BRANCH_ADDRESS" \
  -f db/seeds/001_admin_and_branch.sql
```
- bcrypt 비용은 12를 권장(2025년 기준). 백엔드 인증 시 사용하는 cost와 일치시킨다.
- htpasswd로 만든 `$2y$` 해시는 Go bcrypt가 받지 않으므로 사용 금지.

## 로컬 실행 (Docker Compose)
프로젝트 루트의 `docker-compose.yml`로 PostgreSQL 컨테이너를 띄운다. 자격증명은 루트 `.env`에서 읽는다.
```
docker compose up -d db          # postgres 컨테이너 기동 (volume에 데이터 영속)
docker compose down              # 종료 (볼륨 유지)
docker compose down -v           # 종료 + 볼륨 삭제(초기화)
docker compose logs -f db        # 로그
```
연결 URL은 루트 `.env`의 `DATABASE_URL`을 그대로 사용한다 (예: `postgres://gym:changeme@localhost:5432/gym?sslmode=disable`).

## 명령어
```
# 환경변수 (루트 .env가 로드된 셸에서)
export DB_URL="$DATABASE_URL"

# 상태 확인
goose -dir db/migrations postgres "$DB_URL" status

# 적용 / 롤백
goose -dir db/migrations postgres "$DB_URL" up
goose -dir db/migrations postgres "$DB_URL" down

# 새 마이그레이션 생성
goose -dir db/migrations create <name> sql

# 시드 적용
psql "$DB_URL" -f db/seeds/001_admin_and_branch.sql
```

## 규칙
- **CRITICAL**: 모든 스키마 변경은 goose 마이그레이션으로만. 서버가 스키마를 자동으로 바꾸지 않는다.
- **CRITICAL**: 마이그레이션은 "Up/Down 모두 작동"해야 한다. Down이 불가능한 파괴적 변경은 PR에서 명시적으로 논의.
- **CRITICAL**: **모든 삭제는 soft delete**(`deleted_at` 세팅)로 처리한다. 대상: `branches`, `members`, `admins`. `memberships`는 `status` 컬럼으로 종료 상태(`refunded`/`expired`)를 관리하므로 `deleted_at` 불필요. `check_ins`/`payments`/`membership_events`는 이력성으로 삭제 자체가 없다.
- **CRITICAL**: 자정 KST 배치 SQL은 `(now() AT TIME ZONE 'Asia/Seoul')::date`로 KST 기준 날짜를 계산한다. `CURRENT_DATE`는 DB 세션 타임존 의존이라 사용 금지(또는 세션 초기화 시 `SET TIME ZONE 'Asia/Seoul'`을 강제할 것).
- 모든 테이블에 `created_at`/`updated_at`을 둔다(`idempotency_keys`는 created_at만으로 충분).
- 참조 무결성은 FK로, 비즈니스 규칙은 CHECK 제약으로 보강한다(예: `admins` role/branch_id 조건, `payments.amount <> 0`).
- 부분 유니크 인덱스(`where status in ('active','paused')`)로 "회원당 활성/정지 회원권 1개"를 보장.
- `members.phone`은 같은 지점 내에서만 unique (`(branch_id, phone)` 부분 유니크 인덱스, soft-deleted 제외). 동일인이 여러 지점에 가입 가능하므로 전역 unique는 두지 않는다. `branches.address`는 unique(NULL/공백 거부, 비-NULL만 unique). `admins.password_hash`는 unique를 두지 않는다(bcrypt 솔트로 자연 unique이고 명시적 제약은 안전망 효과가 없으면서 UPDATE 비용만 늘림).
- 시크릿(DB 비밀번호, JWT 비밀키, 시드 초기 관리자 비번 등)은 리포지토리의 `.env`에 저장하고 `.env`는 `.gitignore`에 둔다. 키 목록만 `.env.example`로 커밋. 시드 SQL은 `psql -v admin_password_hash="$SEED_ADMIN_PASSWORD_HASH"` 처럼 환경변수에서 해시를 받아 적용한다(평문은 SQL 파일에 절대 남기지 않는다).
- 결제는 회원권 부여 트랜잭션 안에서 `payments` row를 함께 INSERT 한다(`amount > 0` 강제). 환불 시에는 `memberships.status='refunded'`와 함께 `payments`에 음수 금액 row를 추가해 매출 합계가 자동 보정되게 한다.
- 자정 KST 배치는 회원권 상태 전환 외에 만료 데이터 정리 잡도 함께 실행한다:
  - `DELETE FROM idempotency_keys WHERE created_at < now() - interval '24 hours'`
  - `DELETE FROM revoked_refresh_tokens WHERE revoked_at < now() - interval '15 hours'` (refresh JWT 만료 길이만큼만 보관)
  - `DELETE FROM admin_audit_logs WHERE created_at < now() - interval '1 year'` (1년 보관)
- 자정 배치는 KST **00:01**에 실행한다(00:00 자정 경계 데이터 일관성 안전 margin).
- DB 세션 timezone은 **UTC** 강제. 풀 연결 시 `SET TIME ZONE 'UTC'` 적용. KST 변환은 명시적 `(... AT TIME ZONE 'Asia/Seoul')`로만(타임존 의존 SQL 방지).
