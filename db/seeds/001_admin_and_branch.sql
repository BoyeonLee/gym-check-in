-- 운영 첫날을 위한 최소 시드: 전역 관리자 1명 + 샘플 지점 1개.
-- 평문 비밀번호·실제 bcrypt 해시는 이 파일에 절대 두지 않는다 — 모두 psql -v 변수로 주입.
--
-- 적용:
-- psql "$DATABASE_URL" \
--   -v admin_username="$SEED_ADMIN_USERNAME" \
--   -v admin_password_hash="$SEED_ADMIN_PASSWORD_HASH" \
--   -v branch_name="$SEED_BRANCH_NAME" \
--   -v branch_address="$SEED_BRANCH_ADDRESS" \
--   -f db/seeds/001_admin_and_branch.sql
--
-- bcrypt 해시는 `go run ./backend/cmd/hashpw "$SEED_ADMIN_PASSWORD"`로 생성 ($2a$/$2b$만 허용, $2y$ 금지).
-- ON CONFLICT DO NOTHING으로 멱등 보장 — 운영자가 비번/주소를 바꾼 뒤 시드 재적용으로 덮이지 않는다.

begin;

insert into branches (name, address)
values (:'branch_name', :'branch_address')
on conflict (address) do nothing;

insert into admins (
  username,
  must_change_password,
  temp_password_expires_at,
  role,
  branch_id,
  password_hash
)
values (
  :'admin_username',
  true,
  now() + interval '24 hours',
  'global',
  null,
  :'admin_password_hash'
)
on conflict (username) do nothing;

commit;
