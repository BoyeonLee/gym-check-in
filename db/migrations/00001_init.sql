-- +goose Up
-- +goose StatementBegin

-- memberships의 EXCLUDE 제약(daterange WITH &&)은 btree_gist extension에 의존한다.
create extension if not exists btree_gist;

-- ============================================================
-- branches
-- ============================================================
create table branches (
  id          bigserial primary key,
  name        text not null check (length(name) between 1 and 50),
  address     text unique check (address is null or length(trim(address)) > 0),
  deleted_at  timestamptz,
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now()
);
create index branches_deleted_at_idx on branches (deleted_at);

-- ============================================================
-- admins
-- ============================================================
create table admins (
  id                       bigserial primary key,
  username                 text not null unique,
  password_hash            text not null,
  must_change_password     boolean not null default true,
  temp_password_expires_at timestamptz,
  role                     text not null check (role in ('global', 'branch')),
  branch_id                bigint references branches(id),
  last_login_at            timestamptz,
  password_updated_at      timestamptz,
  failed_login_count       int not null default 0,
  locked_until             timestamptz,
  deleted_at               timestamptz,
  created_at               timestamptz not null default now(),
  updated_at               timestamptz not null default now(),
  check (
    (role = 'global' and branch_id is null) or
    (role = 'branch' and branch_id is not null)
  )
);
create index admins_deleted_at_idx on admins (deleted_at);
create index admins_branch_id_idx  on admins (branch_id);

-- ============================================================
-- members
-- ============================================================
create table members (
  id           bigserial primary key,
  branch_id    bigint not null references branches(id),
  name         text not null check (length(name) between 1 and 100),
  phone        text not null check (phone ~ '^[0-9]{11}$'),
  phone_last4  text generated always as (right(phone, 4)) stored,
  birth_date   date not null,
  deleted_at   timestamptz,
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now()
);
create index members_branch_id_idx   on members (branch_id);
create index members_phone_last4_idx on members (phone_last4);
create index members_deleted_at_idx  on members (deleted_at);
-- 같은 지점 내 같은 번호 중복 차단 (soft-deleted 제외).
create unique index members_branch_phone_unique
  on members (branch_id, phone) where deleted_at is null;

-- ============================================================
-- memberships
-- ============================================================
create table memberships (
  id                bigserial primary key,
  member_id         bigint not null references members(id),
  type              text not null check (type in ('monthly', 'pass10')),
  months            int,
  start_date        date not null,
  end_date          date not null,
  remaining         int,
  status            text not null default 'active'
                     check (status in ('active', 'paused', 'refunded', 'expired')),
  pause_start_date  date,
  pause_end_date    date,
  pause_used        boolean not null default false,
  created_at        timestamptz not null default now(),
  updated_at        timestamptz not null default now(),
  check (
    (type = 'monthly' and months is not null and months > 0 and remaining is null) or
    (type = 'pass10'  and months is null     and remaining is not null and remaining >= 0)
  ),
  check (
    (pause_start_date is null and pause_end_date is null) or
    (pause_start_date is not null and pause_end_date is not null and pause_start_date <= pause_end_date)
  ),
  check (
    status <> 'paused' or (pause_start_date is not null and pause_end_date is not null)
  )
);
create index memberships_member_id_idx on memberships (member_id);
-- 회원당 active/paused 회원권 기간 중첩 차단(미리 등록 허용 — 기간만 안 겹치면 OK).
alter table memberships
  add constraint memberships_no_period_overlap
  exclude using gist (
    member_id with =,
    daterange(start_date, end_date, '[]') with &&
  ) where (status in ('active', 'paused'));

-- ============================================================
-- check_ins
-- ============================================================
create table check_ins (
  id              bigserial primary key,
  member_id       bigint not null references members(id),
  branch_id       bigint not null references branches(id),
  membership_id   bigint not null references memberships(id),
  checked_in_at   timestamptz not null default now(),
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now()
);
create index check_ins_member_checked_in_at_idx on check_ins (member_id, checked_in_at);
create index check_ins_branch_checked_in_at_idx on check_ins (branch_id, checked_in_at);

-- ============================================================
-- payments
-- ============================================================
create table payments (
  id            bigserial primary key,
  membership_id bigint not null references memberships(id),
  branch_id     bigint not null references branches(id),
  amount        integer not null check (amount <> 0),
  method        text not null check (method in ('cash', 'card')),
  paid_at       date not null,
  memo          text,
  performed_by  bigint not null references admins(id),
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now()
);
create index payments_paid_at_idx           on payments (paid_at);
create index payments_branch_paid_at_idx    on payments (branch_id, paid_at);
create index payments_membership_id_idx     on payments (membership_id);

-- ============================================================
-- membership_events
-- ============================================================
create table membership_events (
  id                bigserial primary key,
  membership_id     bigint not null references memberships(id),
  action            text not null check (action in ('pause', 'unpause', 'cancel_pause', 'refund', 'bulk_extend')),
  pause_start_date  date,
  pause_end_date    date,
  actual_pause_end  date,
  extend_days       int,
  reason            text not null,
  performed_by      bigint not null references admins(id),
  created_at        timestamptz not null default now(),
  updated_at        timestamptz not null default now()
);
create index membership_events_membership_id_idx on membership_events (membership_id);

-- ============================================================
-- revoked_refresh_tokens
-- ============================================================
create table revoked_refresh_tokens (
  jti         text primary key,
  admin_id    bigint not null references admins(id),
  revoked_at  timestamptz not null default now()
);
create index revoked_refresh_tokens_admin_id_idx   on revoked_refresh_tokens (admin_id);
create index revoked_refresh_tokens_revoked_at_idx on revoked_refresh_tokens (revoked_at);

-- ============================================================
-- admin_audit_logs
-- ============================================================
create table admin_audit_logs (
  id          bigserial primary key,
  admin_id    bigint references admins(id),
  action      text not null,
  target_type text,
  target_id   bigint,
  ip          inet,
  user_agent  text,
  metadata    jsonb,
  created_at  timestamptz not null default now()
);
create index admin_audit_logs_admin_id_created_at_idx on admin_audit_logs (admin_id, created_at);
create index admin_audit_logs_action_created_at_idx   on admin_audit_logs (action, created_at);
create index admin_audit_logs_created_at_idx          on admin_audit_logs (created_at);

-- ============================================================
-- idempotency_keys
-- ============================================================
create table idempotency_keys (
  key             text primary key,
  admin_id        bigint not null references admins(id),
  endpoint        text not null,
  request_hash    text not null,
  response_status int not null,
  response_body   jsonb not null,
  created_at      timestamptz not null default now()
);
create index idempotency_keys_created_at_idx on idempotency_keys (created_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- 생성 역순으로 제거. FK 의존성 때문에 child → parent 순서.
drop table if exists idempotency_keys;
drop table if exists admin_audit_logs;
drop table if exists revoked_refresh_tokens;
drop table if exists membership_events;
drop table if exists payments;
drop table if exists check_ins;
drop table if exists memberships;
drop table if exists members;
drop table if exists admins;
drop table if exists branches;

drop extension if exists btree_gist;

-- +goose StatementEnd
