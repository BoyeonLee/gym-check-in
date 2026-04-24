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
  name         text not null,
  address      text,
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now()
);

members (
  id           bigserial primary key,
  branch_id    bigint not null references branches(id),
  name         text not null,
  phone        text not null,
  birth_date   date,
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now()
);
create index on members (branch_id);
create index on members (phone);

memberships (
  id           bigserial primary key,
  member_id    bigint not null references members(id),
  type         text not null check (type in ('monthly','pass10')),
  start_date   date not null,
  end_date     date not null,
  remaining    int,           -- type='pass10'일 때 사용, 초기 10
  status       text not null default 'active'
                check (status in ('active','refunded','expired')),
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now()
);
create index on memberships (member_id);
-- 한 회원당 활성 회원권 1개 보장(애플리케이션 규칙 보강용 부분 유니크)
create unique index memberships_one_active_per_member
  on memberships (member_id) where status = 'active';

membership_events (
  id                bigserial primary key,
  membership_id     bigint not null references memberships(id),
  action            text not null check (action in ('pause','refund','bulk_extend')),
  pause_start_date  date,          -- action='pause'
  pause_end_date    date,          -- action='pause'
  extend_days       int,           -- action='bulk_extend'
  reason            text not null,
  performed_by      bigint not null references admins(id),
  created_at        timestamptz not null default now(),
  updated_at        timestamptz not null default now()
);
create index on membership_events (membership_id);

check_ins (
  id              bigserial primary key,
  member_id       bigint not null references members(id),
  branch_id       bigint not null references branches(id),
  membership_id   bigint references memberships(id),
  checked_in_at   timestamptz not null default now(),
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now()
);
create index on check_ins (member_id, checked_in_at);
create index on check_ins (branch_id, checked_in_at);
-- 같은 날 중복 체크인 허용 → 유니크 제약 없음

admins (
  id                    bigserial primary key,
  username              text not null unique,
  password_hash         text not null,
  must_change_password  boolean not null default true,
  role                  text not null check (role in ('global','branch')),
  branch_id             bigint references branches(id),
  last_login_at         timestamptz,
  password_updated_at   timestamptz,
  created_at            timestamptz not null default now(),
  updated_at            timestamptz not null default now(),
  check (
    (role = 'global' and branch_id is null) or
    (role = 'branch' and branch_id is not null)
  )
);
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
- `admins`: 전역 관리자 1명 (`username='owner'`, 초기 비번 해시 삽입, `must_change_password=true`, `role='global'`).
- `branches`: 샘플 지점 1개.

## 명령어
```
# 환경변수
export DB_URL="postgres://user:pass@localhost:5432/gym?sslmode=disable"

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
- 모든 테이블에 `created_at`/`updated_at`을 둔다.
- 참조 무결성은 FK로, 비즈니스 규칙은 CHECK 제약으로 보강한다(예: `admins` role/branch_id 조건).
- 부분 유니크 인덱스(`where status='active'`)로 "회원당 활성 회원권 1개"를 보장.
- 시크릿(DB 비밀번호, 시드 초기 관리자 비번)은 리포지토리에 커밋하지 않는다.
