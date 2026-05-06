# 개발 환경 부트스트랩

새 개발자(또는 새 Claude 세션)가 로컬에서 처음 띄울 때 따라가는 한 페이지. 각 단계는 위에서부터 순서대로 실행한다.

## 0. 사전 준비
- Docker Desktop 또는 Docker Engine + Compose
- Go 1.22+
- Node 20+ + pnpm 9+
- `psql` (PostgreSQL 클라이언트, 시드 적용용)
- `goose` (마이그레이션 도구)
  - macOS: `brew install goose`
  - Linux: `go install github.com/pressly/goose/v3/cmd/goose@latest`

## 1. `.env` 부트스트랩
```bash
cp .env.example .env
```

`.env`를 열어 다음을 채운다.

| 키 | 값 |
|----|-----|
| `POSTGRES_PASSWORD` | 임의 문자열 (개발용) |
| `DATABASE_URL` | 위 비밀번호 반영 (`postgres://gym:<password>@localhost:5432/gym?sslmode=disable`) |
| `JWT_ACCESS_SECRET` | `openssl rand -base64 48` 결과 |
| `JWT_REFRESH_SECRET` | `openssl rand -base64 48` 결과(access와 다른 값) |
| `APP_ENV` | `dev` |
| `CORS_ORIGIN` | `http://localhost:5173` |
| `SEED_ADMIN_USERNAME` | `owner` (또는 임의) |
| `SEED_ADMIN_PASSWORD` | 임의(첫 로그인 후 즉시 변경됨) |
| `SEED_ADMIN_PASSWORD_HASH` | (다음 단계 4에서 채움) |
| `SEED_BRANCH_NAME` | `PBOY MMA 본점` |
| `SEED_BRANCH_ADDRESS` | `서울 송파구 가락로 142 지하 1층` |
| `TEST_DATABASE_URL` | `postgres://gym:<password>@localhost:5432/gym_test?sslmode=disable` |
| `VITE_API_URL` | `http://localhost:8080` |

**중요**: `.env`는 `.gitignore`에 등록되어 있다. `git status`에 절대 보이면 안 된다.

## 2. PostgreSQL 컨테이너 기동
```bash
docker compose up -d db
docker compose logs -f db   # "database system is ready to accept connections" 확인 후 Ctrl+C
```

`psql "$DATABASE_URL" -c '\l'`로 접속 가능한지 확인.

## 3. 마이그레이션 적용
```bash
goose -dir db/migrations postgres "$DATABASE_URL" up
goose -dir db/migrations postgres "$DATABASE_URL" status
```

### 테스트 DB 준비 (TDD)
TDD로 백엔드를 만들기 때문에 테스트 전용 DB가 필요하다. 같은 Postgres 컨테이너에 별도 데이터베이스로 둔다.
```bash
psql "$DATABASE_URL" -c "CREATE DATABASE gym_test;"
goose -dir db/migrations postgres "$TEST_DATABASE_URL" up
```
이후 `go test ./...`는 자동으로 `TEST_DATABASE_URL`을 읽어 사용한다. 테스트는 매 케이스 전에 도메인 테이블을 truncate하므로 이 DB의 데이터는 일회성으로 봐도 된다.

## 4. 시드용 bcrypt 해시 생성
```bash
go run ./backend/cmd/hashpw "$SEED_ADMIN_PASSWORD"
# → $2a$12$... 출력
```

출력된 해시를 `.env`의 `SEED_ADMIN_PASSWORD_HASH`에 붙여넣고 셸 환경변수도 다시 export.

```bash
set -a; source .env; set +a
```

## 5. 시드 적용
```bash
psql "$DATABASE_URL" \
  -v admin_username="$SEED_ADMIN_USERNAME" \
  -v admin_password_hash="$SEED_ADMIN_PASSWORD_HASH" \
  -v branch_name="$SEED_BRANCH_NAME" \
  -v branch_address="$SEED_BRANCH_ADDRESS" \
  -f db/seeds/001_admin_and_branch.sql
```

검증:
```bash
psql "$DATABASE_URL" -c "select id, username, role, must_change_password, temp_password_expires_at from admins;"
psql "$DATABASE_URL" -c "select id, name, address from branches;"
```
시드 직후 `must_change_password=true`, `temp_password_expires_at`은 NULL(시드 비번은 만료가 별도로 걸리지 않음 — 첫 로그인 후 본인이 변경). 만약 임시 비번이 발급된 계정이면 `temp_password_expires_at`이 발급+24h로 채워져 있어야 함.

## 6. 백엔드 실행
```bash
cd backend
go mod tidy
go run ./cmd/server
# 8080에서 listen 확인. 인-프로세스 cron 시작 로그도 같이 출력.
```

다른 터미널에서 헬스체크:
```bash
curl http://localhost:8080/api/healthz
# {"status":"ok"} 비슷한 응답
```

## 7. 프론트엔드 실행
```bash
cd frontend
pnpm install
pnpm dev
# http://localhost:5173 에서 Vite dev server
```

## 8. 첫 사용 시나리오
1. 브라우저에서 `http://localhost:5173/admin/login` → 시드 관리자(`owner` + 위에서 정한 비번) 로그인
2. 비밀번호 변경 강제 화면이 뜸 → 새 비번 설정
3. `/admin/branches`에서 지점이 보임 (시드된 1개)
4. `/`로 이동 → "지점 미설정" → 지점 선택 → 키오스크 대기 화면
5. `/admin/members`에서 회원 등록 → 회원권 부여(결제 입력 포함) → 키오스크에서 검색·체크인 흐름 검증

## 자주 빠뜨리는 것
- `.env`를 채우지 않고 `go run ./cmd/server` 실행 → 환경변수 누락 에러. `set -a; source .env; set +a`로 셸에 로드 후 실행.
- bcrypt 해시 생성 후 `.env`에 붙여넣기만 하고 셸은 재로드 안 해서 시드 SQL이 빈 해시로 들어감 → 로그인 실패. `set -a; source .env; set +a` 다시 실행.
- `JWT_ACCESS_SECRET`과 `JWT_REFRESH_SECRET`을 같은 값으로 설정 → 토큰 분리 의미 사라짐. 반드시 다른 값.
- macOS에서 `psql`이 없을 때: `brew install libpq && brew link --force libpq`.
- 컨테이너 볼륨 초기화: `docker compose down -v` (개발 데이터 전부 삭제). 다시 처음부터 진행.

## 참고 문서
- `docs/PRD.md` — 무엇을 만드는지
- `docs/ARCHITECTURE.md` — 어떻게 연결되는지
- `docs/API.md` — 엔드포인트 명세
- `docs/ROADMAP.md` — 어디까지 만들었고 다음에 뭘 하는지
- `docs/TESTING.md` — TDD 워크플로우·에러/엣지 케이스 카탈로그
- `docs/OPERATIONS.md` — 배포/운영 절차
