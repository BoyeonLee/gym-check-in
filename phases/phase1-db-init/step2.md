---
agent: backend
depends_on: [migrations]
---

# Step 2: 시드 SQL (전역 관리자 1명 + 샘플 지점 1개)

## 목표

step1에서 적재된 빈 스키마에 운영 첫날을 위한 최소 시드를 INSERT 한다. **평문 비밀번호·시크릿은 SQL 파일에 절대 남기지 않는다** — psql `-v` 변수로 환경변수를 주입받아 적용한다.

## 읽어야 할 파일

- `CLAUDE.md` (루트) — 시크릿 미커밋 CRITICAL 규칙
- `db/CLAUDE.md` — `## 시드` 섹션 (admins/branches 시드 정책 + bcrypt 해시 생성 절차)
- `backend/CLAUDE.md` — 임시 비밀번호 정책(`must_change_password=true`, `temp_password_expires_at=now()+24h`)이 시드에도 적용되어야 함
- `docs/ROADMAP.md` Phase 1 산출물 — `db/seeds/001_admin_and_branch.sql` 명세
- `docs/DEV_SETUP.md` — 시드 적용 절차 (`go run ./backend/cmd/hashpw "$SEED_ADMIN_PASSWORD"` → `psql -v ... -f db/seeds/001_admin_and_branch.sql`)
- `.env.example` — 시드에 사용되는 환경변수 키(`SEED_ADMIN_USERNAME`, `SEED_ADMIN_PASSWORD_HASH`, `SEED_BRANCH_NAME`, `SEED_BRANCH_ADDRESS`)
- step1에서 만든 `db/migrations/00001_init.sql` (컬럼·CHECK 확인)

## 작업

### `db/seeds/001_admin_and_branch.sql`

psql 메타 변수로 값 주입. 다음 변수를 사용한다(이름은 `db/CLAUDE.md` 절차와 일치):

- `:'admin_username'`
- `:'admin_password_hash'` (bcrypt cost 12 해시 문자열 — 헤더 prefix는 `2a` 또는 `2b`. `2y`(htpasswd)는 Go bcrypt 호환 안 됨)
- `:'branch_name'`
- `:'branch_address'`

SQL 본문:

1. **샘플 지점 INSERT**
   - `INSERT INTO branches (name, address) VALUES (:'branch_name', :'branch_address') ON CONFLICT (address) DO NOTHING;`
   - `address`는 unique 제약이 있으므로 ON CONFLICT가 자연스럽게 멱등 처리. (NULL 주소를 시드에서 사용하지 않는다.)
2. **전역 관리자 INSERT**
   - `INSERT INTO admins (username, password_hash, must_change_password, temp_password_expires_at, role, branch_id) VALUES (:'admin_username', :'admin_password_hash', true, now() + interval '24 hours', 'global', NULL) ON CONFLICT (username) DO NOTHING;`
   - `username`은 unique이므로 ON CONFLICT로 멱등.
   - `must_change_password=true` + `temp_password_expires_at=now()+24h`는 첫 로그인 시 강제 비번 변경 정책의 출발점. 정책상 시드 비번도 임시 비번으로 취급한다.
   - `failed_login_count`는 default 0 그대로, `locked_until`/`last_login_at`/`password_updated_at`/`deleted_at` 모두 NULL 그대로.

추가 사항:

- 파일은 transaction으로 감싼다 (`BEGIN; ... COMMIT;`). ON CONFLICT가 있어 사실상 멱등이지만 명시적 트랜잭션이 안전.
- 파일 상단에 짧은 주석으로 적용 절차를 한 줄 안내(`-- psql -v admin_username=... -v admin_password_hash=... -v branch_name=... -v branch_address=... -f db/seeds/001_admin_and_branch.sql`).
- **평문 비밀번호 절대 금지**. SQL 안에 `password=`/`'change-me'`/실제 해시 같은 값이 있으면 차단(검증 절차에 grep 포함).
- `\set ON_ERROR_STOP on` 같은 psql 메타 명령은 두지 않는다(셸에서 옵션으로 제어). 파일은 순수 SQL.

## 핵심 규칙 (반드시 박는다)

- **시크릿 미하드코딩**: 파일 안에 평문 비번·실제 bcrypt 해시 문자열을 직접 박지 않는다. 모두 `:'변수'`로.
- **멱등성**: 두 번 적용해도 row 중복이 생기지 않아야 한다 (ON CONFLICT).
- 시드는 step1의 마이그레이션이 모두 Up 상태여야 적용 가능 — depends_on: migrations.
- `psql -v` 변수는 `:'name'`(quoted) 형태로 사용해 SQL injection 방지(공백/특수문자 포함된 지점 이름·주소도 안전).
- 자동화·CI에서 적용할 때를 대비해 트랜잭션 안에서 모두 실행 (실패 시 부분 적용 방지).

## Acceptance Criteria

`.worktrees/backend/`에서 실행. 메인의 `docker compose up -d db`가 떠 있고 step1의 마이그레이션이 적용된 상태에서:

```bash
set -a; source ../../.env; set +a

# 사전: bcrypt 해시 생성. 백엔드 hashpw CLI는 Phase 2에서 만들어지므로,
# 이 step의 AC 검증에는 임시로 Go 한 줄 또는 미리 만들어둔 hash 환경변수를 쓴다.
# Phase 0에서 .env에 SEED_ADMIN_PASSWORD_HASH가 채워져 있다고 가정.
# 비어있다면 임시로 다음으로 생성해 검증한다(검증 후 .env에 다시 비워둘 것):
#   python3 -c 'import bcrypt; print(bcrypt.hashpw(b"test1234".encode(), bcrypt.gensalt(rounds=12)).decode())'
# 또는 Phase 2에서 cmd/hashpw가 만들어지면 그것을 사용.

test -n "$SEED_ADMIN_PASSWORD_HASH" || { echo "SEED_ADMIN_PASSWORD_HASH 비어있음 — 사용자 개입 필요"; exit 1; }

# 1차 적용
psql "$DATABASE_URL" \
  -v admin_username="$SEED_ADMIN_USERNAME" \
  -v admin_password_hash="$SEED_ADMIN_PASSWORD_HASH" \
  -v branch_name="$SEED_BRANCH_NAME" \
  -v branch_address="$SEED_BRANCH_ADDRESS" \
  -f db/seeds/001_admin_and_branch.sql

# 2차 적용 (멱등성 검증 — 같은 명령 다시 실행)
psql "$DATABASE_URL" \
  -v admin_username="$SEED_ADMIN_USERNAME" \
  -v admin_password_hash="$SEED_ADMIN_PASSWORD_HASH" \
  -v branch_name="$SEED_BRANCH_NAME" \
  -v branch_address="$SEED_BRANCH_ADDRESS" \
  -f db/seeds/001_admin_and_branch.sql

# 검증
psql "$DATABASE_URL" -c "SELECT count(*) FROM admins WHERE role='global' AND deleted_at IS NULL;"  # → 1
psql "$DATABASE_URL" -c "SELECT count(*) FROM branches WHERE deleted_at IS NULL;"                  # → 1
psql "$DATABASE_URL" -c "SELECT must_change_password, temp_password_expires_at IS NOT NULL FROM admins WHERE username = '$SEED_ADMIN_USERNAME';"
# → t | t

# 평문/하드코딩 시크릿 검출 (반드시 0 매치) — 환경변수 placeholder만 허용
# 1) 평문 password 리터럴 금지: 작은따옴표로 감싼 password 값이 :'변수' 형태가 아니면 실패
! grep -nE "password[^']*'[^:]" db/seeds/001_admin_and_branch.sql
# 2) bcrypt 해시 리터럴 금지: 해시 헤더 prefix(달러 + 2a/2b/2y + 달러)가 SQL 파일 본문에 나타나면 실패
#    → 대신 환경변수 마커 매치 0 여부를 Python으로 검사 (raw 패턴을 step.md에 박지 않음)
python3 -c "import re,sys; t=open('db/seeds/001_admin_and_branch.sql').read(); sys.exit(1 if re.search(chr(36)+r'2[aby]'+chr(36), t) else 0)"

# Go 빌드/테스트(이 step은 Go 코드 미수정 — 정합성 차원)
cd backend && go build ./... && go test ./... ; cd ..
```

## 검증 절차

1. 위 AC 명령을 직접 실행한다. 모든 검증이 통과해야 한다.
2. **`code-reviewer` 서브에이전트를 Task tool로 호출해 변경 사항을 검증받는다.** 입력: step 이름(`phase1-db-init/step2`), `git diff HEAD --stat`. PASS 응답이 나와야 통과.
3. 결과에 따라 `phases/phase1-db-init/index.json`의 step2 status 업데이트:
   - PASS → `"status": "completed"` + `"summary": "001_admin_and_branch.sql 추가; psql -v 변수로 해시·이름·주소 주입, ON CONFLICT 멱등, 평문 시크릿 미포함."`
   - 3회 재시도 후에도 실패 → `"status": "error"` + `"error_message"`
   - 사용자 개입 필요(`SEED_ADMIN_PASSWORD_HASH` 미설정, psql 미설치 등) → `"status": "blocked"` + `"blocked_reason"` 후 즉시 중단

## 금지사항

- `frontend/`·`backend/internal`·`backend/cmd` 변경 금지 (이 step은 시드 SQL 1개만).
- 공유 파일(`docs/`, 루트 `.env.example`, `docker-compose.yml`, 루트 `CLAUDE.md`, `.gitignore`, `scripts/`, `.claude/`) 변경 금지 — shared step에서만.
- **평문 비밀번호·실제 bcrypt 해시·JWT 시크릿을 SQL/주석/PR 본문/로그에 남기지 마라.** 검출 시 즉시 BLOCKED.
- `db/migrations/`에 시드 INSERT 추가 금지 — 시드는 마이그레이션과 분리 (CI/운영에서 환경별 분리 적용 필요).
- `htpasswd`로 만든 `$2y$` 해시 사용 금지 — Go bcrypt가 받지 않는다. `$2a$` 또는 `$2b$`만.
- `ON CONFLICT DO UPDATE`로 기존 row를 덮어쓰지 마라 — 시드는 "최초 1회"가 의도. 운영자가 비번 바꾼 뒤 시드 재적용으로 덮이면 사고. `DO NOTHING`만.
- `branches`/`admins`/`members`의 `deleted_at`을 시드에서 세팅하지 마라(NULL 그대로).
- ADR 외 라이브러리·CLI 도입 금지 — bcrypt 해시 생성은 Phase 2의 `cmd/hashpw` 또는 일회성 임시 도구만.
