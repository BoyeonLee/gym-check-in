-- +goose Up

-- +goose StatementBegin
create or replace function set_updated_at()
returns trigger as $$
begin
  new.updated_at = now();
  return new;
end;
$$ language plpgsql;
-- +goose StatementEnd

-- updated_at 컬럼을 가진 7개 테이블에 BEFORE UPDATE 트리거 설치.
-- (idempotency_keys / revoked_refresh_tokens / admin_audit_logs는 created_at만 가진 이력성 테이블이라 제외.)
create trigger set_updated_at_branches
  before update on branches
  for each row execute function set_updated_at();

create trigger set_updated_at_admins
  before update on admins
  for each row execute function set_updated_at();

create trigger set_updated_at_members
  before update on members
  for each row execute function set_updated_at();

create trigger set_updated_at_memberships
  before update on memberships
  for each row execute function set_updated_at();

create trigger set_updated_at_membership_events
  before update on membership_events
  for each row execute function set_updated_at();

create trigger set_updated_at_check_ins
  before update on check_ins
  for each row execute function set_updated_at();

create trigger set_updated_at_payments
  before update on payments
  for each row execute function set_updated_at();

-- +goose Down

drop trigger if exists set_updated_at_payments          on payments;
drop trigger if exists set_updated_at_check_ins         on check_ins;
drop trigger if exists set_updated_at_membership_events on membership_events;
drop trigger if exists set_updated_at_memberships       on memberships;
drop trigger if exists set_updated_at_members           on members;
drop trigger if exists set_updated_at_admins            on admins;
drop trigger if exists set_updated_at_branches          on branches;

drop function if exists set_updated_at();
