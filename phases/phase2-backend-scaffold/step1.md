---
agent: backend
---

# Step 1: 백엔드 스캐폴드 + 테스트 인프라 + apperr

## 목표

`backend/` 모듈을 초기화하고, 이후 모든 step이 의존하는 **테스트 인프라**(`internal/testutil`)와 **에러 통일 래퍼**(`internal/apperr`)를 먼저 만든다. 핸들러는 `GET /api/healthz` 단 하나만 — 다음 step부터 차근차근 추가한다.

이 step이 끝나면:
- `cd backend && go build ./... && go test -race ./...`이 통과한다.
- 이후 step의 핸들러 테스트가 사용할 `SetupDB`, `TruncateAll`, `CreateBranch/Admin/Member/Membership`, `AuthRequest`, `FreezeTime`이 모두 동작한다.
- pgx 위반 코드(`23505`/`23P01`/`23514`/`40001`/`40P01`)가 `apperr.AppError`로 자동 매핑된다.
- `cmd/hashpw "비번"`이 bcrypt cost 12 해시를 stdout으로 출력한다.

## 읽어야 할 파일

- `CLAUDE.md` (루트) — 공통 CRITICAL 규칙
- `backend/CLAUDE.md` — 디렉토리 구조, pgxpool 설정(25/2/5m/1h, `SET TIME ZONE 'UTC'`), HTTP 서버 timeout, graceful shutdown, slog 필드, 트랜잭션 retry 정책, 응답 timestamp KST(`+09:00`), `Idempotency-Key` UUIDv4 검증 규칙
- `db/CLAUDE.md` — 테이블/제약/인덱스(특히 constraint 이름: `members_branch_phone_unique`, `admins_username_key`, `branches_address_key`, `memberships_no_period_overlap`)
- `docs/API.md` — 에러 코드 카탈로그 (apperr가 매핑할 코드 목록)
- `docs/ARCHITECTURE.md` — 데이터 흐름·페이지네이션·KST 시각화 정책
- `docs/TESTING.md` — testutil 시그니처·계층·결정성 규칙(`Clock`/`UUIDGen` 주입)
- `docs/ROADMAP.md` Phase 2 산출물·검증 기준
- `db/migrations/00001_init.sql`, `db/migrations/00002_updated_at_trigger.sql` (스키마 정본)

## 작업

### 1. 모듈 초기화 + 의존성

`.worktrees/backend/backend/`에서:

```
go mod init github.com/lboyeon1223/gym-check-in/backend
```

직접 추가할 의존성(이번 step에서 실제 import하는 것만 — 나머지는 다음 step에서):

- `github.com/gin-gonic/gin`
- `github.com/jackc/pgx/v5`, `github.com/jackc/pgx/v5/pgxpool`
- `golang.org/x/crypto/bcrypt`
- `github.com/google/uuid`
- `github.com/stretchr/testify`
- `github.com/pressly/goose/v3` (testutil의 `SetupDB`가 마이그레이션을 실행)

`go get` 호출은 hook이 ADR 외 라이브러리를 차단할 수 있으므로, 위 패키지들이 ADR-008(스택 결정)에 명시되어 있는지 확인 후 추가한다. 명시되지 않은 라이브러리가 보이면 즉시 `blocked` 처리하고 사용자 개입을 요청한다.

### 2. `backend/internal/config`

환경변수 로더 (struct + `Load()` 함수 + 단위 테스트).

```go
type Config struct {
    DatabaseURL       string
    JWTAccessSecret   string  // 이 step에선 비어있어도 OK (auth는 step3)
    JWTRefreshSecret  string
    Port              string  // default "8080"
    CORSOrigin        string
    AppEnv            string  // "dev" | "prod"
}

func Load() (*Config, error)
```

- 누락 필드(`DATABASE_URL`)는 에러로 반환. 비밀키는 step1에선 옵셔널(빈 문자열 허용), step3에서 필수로 격상.
- 단위 테스트: 환경변수 set/unset 케이스, default 값.

### 3. `backend/internal/apperr`

```go
type AppError struct {
    Code    string  // "PHONE_DUPLICATE", "MEMBERSHIP_PERIOD_OVERLAP", ...
    Message string
    Status  int     // 400, 401, 403, 404, 409, 422, 500
    Cause   error
}

func (e *AppError) Error() string
func (e *AppError) Unwrap() error
func New(status int, code, message string) *AppError
func Wrap(status int, code, message string, cause error) *AppError
func IsCode(err error, code string) bool

// pgx 에러 → AppError. 호출 컨텍스트별 코드 매핑은 caller가 추가 분기.
func FromDBError(err error) *AppError
```

`FromDBError` 매핑 (PostgreSQL SQLSTATE 기준):

- `23505` unique_violation: constraint 이름으로 분기
  - `members_branch_phone_unique` → 409 `PHONE_DUPLICATE`
  - `admins_username_key` → 409 `USERNAME_DUPLICATE`
  - `branches_address_key` → 409 `ADDRESS_DUPLICATE`
  - 그 외 → 409 `CONFLICT` (기본)
- `23P01` exclusion_violation → 409 `MEMBERSHIP_PERIOD_OVERLAP`
- `23514` check_violation → 400 `INVALID_INPUT`
- `23502` not_null / `23503` foreign_key → 500 `INTERNAL` (정상 흐름에서 발생하면 안 됨)
- `40001` / `40P01` → caller가 retry 헬퍼로 흡수해야 하므로 `AppError`로 감싸지 말고 원본 에러 그대로 통과(또는 `Status: 500` + `Code: "TRANSIENT"`로 감싸 retry 헬퍼가 식별).
- 나머지 → 500 `INTERNAL`

단위 테스트: 가짜 `*pgconn.PgError`를 만들어 코드별 매핑을 검증.

### 4. `backend/internal/testutil` — **이 step의 핵심**

다음 헬퍼를 모두 동작하게 만든다(시그니처는 정확히 이 형태). 각 헬퍼는 `t *testing.T`를 받아 실패 시 `t.Fatalf`로 즉시 중단한다.

```go
// pkg testutil

// 환경변수 TEST_DATABASE_URL 사용. 없으면 t.Skip("TEST_DATABASE_URL 미설정").
// goose up으로 db/migrations 적용. process 단위 캐시(sync.Once)로 한 번만 적용.
// 매 테스트 호출 시 TruncateAll로 격리.
func SetupDB(t *testing.T) *pgxpool.Pool

// 모든 테이블을 RESTART IDENTITY CASCADE로 비운다. (시드 admin/branch도 같이 비움)
func TruncateAll(t *testing.T, pool *pgxpool.Pool)

// 옵션 패턴. nil 가능.
type BranchOpts struct { Name, Address string }
type AdminOpts struct {
    Username string
    Role     string  // "global" | "branch"
    BranchID *int64  // role=branch면 필수
    Password string  // 미지정 시 "test1234A". bcrypt cost 4(테스트는 빠르게)로 해시.
    MustChangePassword bool
}
type MemberOpts struct { BranchID int64; Name, Phone, BirthDate string }
type MembershipOpts struct {
    MemberID  int64
    Type      string  // "monthly" | "pass10"
    Months    *int    // monthly일 때
    Remaining *int    // pass10일 때
    StartDate string  // "2026-05-07"
    EndDate   string
    Status    string  // default "active"
}

func CreateBranch(t *testing.T, pool *pgxpool.Pool, o *BranchOpts) (id int64)
func CreateAdmin(t *testing.T, pool *pgxpool.Pool, o *AdminOpts) (id int64, plainPassword string)
func CreateMember(t *testing.T, pool *pgxpool.Pool, o *MemberOpts) (id int64)
func CreateMembership(t *testing.T, pool *pgxpool.Pool, o *MembershipOpts) (id int64)

// HTTP e2e 헬퍼. step3 이후에 의미가 생기지만 시그니처는 이 step에서 확정.
func Login(t *testing.T, server http.Handler, username, password string) (accessToken, refreshToken string)
func AuthRequest(t *testing.T, server http.Handler, method, path, accessToken string, body any) *httptest.ResponseRecorder

// Clock/UUIDGen 인터페이스 (시간·랜덤 결정성). 기본 구현 + 테스트용 fake 둘 다 제공.
type Clock interface { Now() time.Time }
type UUIDGen interface { NewV4() string }

// FreezeTime은 t.Cleanup에서 해제. 핸들러 테스트가 결정적 timestamp를 받게 한다.
func FreezeTime(t *testing.T, instant time.Time) (restore func())
```

- `SetupDB`는 goose 마이그레이션을 코드에서 직접 실행한다(예: `goose.RunContext(ctx, "up", db, "../../db/migrations")`). 외부 `goose` 바이너리에 의존하지 않는다(CI에서 바이너리 누락 회피).
- `Login`/`AuthRequest`는 step3에서 actual auth 라우트가 생기기 전까지는 `t.Skip` 또는 503 응답을 가정해도 OK. 시그니처와 호출 가능성만 보장.
- 헬퍼 자체에 단위 테스트는 두지 않아도 되지만, `SetupDB`/`TruncateAll`는 짧은 smoke 테스트(`internal/testutil/testutil_test.go`)로 한 번 검증 — `//go:build integration`.

### 5. `backend/internal/repo` (씨앗만)

```go
// internal/repo/db.go
package repo

func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error)
```

- pgxpool 설정: `pool_max_conns=25`, `pool_min_conns=2`, `pool_max_conn_idle_time=5m`, `pool_max_conn_lifetime=1h`, `BeforeAcquire`(또는 `AfterConnect`)에서 `SET TIME ZONE 'UTC'` 실행.
- 단위 테스트는 아직 없어도 됨(다음 step의 repo 테스트가 `NewPool`을 사용).

### 6. `backend/cmd/server/main.go`

엔트리포인트. 다음만 수행:

1. `config.Load()` → 실패 시 fatal.
2. `repo.NewPool(ctx, cfg.DatabaseURL)` → 실패 시 fatal.
3. Gin 라우터 생성 (미들웨어는 step2에서 추가). `GET /api/healthz` 1개만 등록 — `pool.Ping(ctx)` 후 `{"status":"ok"}`.
4. `&http.Server{ReadHeaderTimeout: 5*time.Second, ReadTimeout: 10*time.Second, WriteTimeout: 30*time.Second, IdleTimeout: 60*time.Second, Handler: r}`
5. **graceful shutdown**: SIGTERM/SIGINT 수신 시 (a) HTTP 서버 신규 연결 차단 + 30초 동안 진행 중 요청 대기 → (b) DB 풀 close. cron은 step8에서 추가될 때 1순위로 정지하도록 자리만 비워둔다.
6. `body size limit`: 임시로 라우터에 `c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)` 미들웨어를 끼워넣지 말고 step2의 `bodylimit` 미들웨어에서 통합. 이 step에선 healthz만 GET이라 영향 없음.

### 7. `backend/cmd/hashpw/main.go`

```
go run ./cmd/hashpw "test1234A"   # → bcrypt 표준 prefix(2a/2b · cost 12)로 시작하는 60자 해시 한 줄 출력
```

- `os.Args[1]`을 받아 `bcrypt.GenerateFromPassword([]byte(arg), 12)` → stdout으로 해시 1줄 출력.
- 인자 없으면 stderr에 사용법 + exit 1. 빈 문자열도 거부.
- 비번을 stdout/stderr에 echo하지 않는다(해시만 출력, 평문 노출 금지).
- 단위 테스트: 출력이 bcrypt 표준 prefix(major version 2a 또는 2b, cost 12)로 시작하고 `bcrypt.CompareHashAndPassword`가 통과하는지. (정규식 작성 시 dollar 기호는 `\\x24` 또는 `chr(36)`로 표기해 hook의 평문 해시 검출에 걸리지 않게.)

### 8. 핸들러 테스트 (`GET /api/healthz`)

`internal/http/health_test.go` (또는 적절한 패키지에) httptest로 다음을 검증:
- 200 응답 + body `{"status":"ok"}`
- DB 풀 down 상태(임의로 close 후) → 503 응답 + `{"error":{"code":"INTERNAL","message":"..."}}`

이 테스트는 `internal/testutil.SetupDB`를 호출 가능한지를 e2e로 가장 먼저 검증하는 의미가 있다.

## 핵심 규칙 (반드시 박는다)

- **TDD**: `internal/{http,domain,repo,auth,batch}` 파일 변경에 대응하는 `_test.go`가 같이 추가되어야 한다(hook이 차단). 이 step에선 healthz 핸들러 + apperr 매핑 + config 로드 + cmd/hashpw 출력에 대한 테스트가 같이 들어와야 한다.
- **시크릿/PII 비노출**: `cmd/hashpw`가 평문 비번을 출력하지 않게. config 로더가 비밀키 값을 에러 메시지에 포함하지 않게(키 이름만).
- **시간/랜덤은 인터페이스로 주입**: `time.Now()`/`uuid.New()`를 도메인·핸들러 코드에서 직접 부르지 말고 `Clock`/`UUIDGen` 인터페이스를 통해 호출. 이 step에선 healthz가 시간을 안 쓰지만, 향후 step에서 강제될 수 있도록 인터페이스 정의를 먼저 둔다.
- **pgxpool `SET TIME ZONE 'UTC'`**: 누락 시 KST 변환 SQL이 환경 종속이 됨. `repo.NewPool` 단위에서 강제.
- **goose 마이그레이션은 testutil이 코드로 실행**: 테스트 환경에 외부 바이너리 의존 X.
- **healthz는 인증·rate limit 면제**. step2에서 미들웨어 도입 시 healthz는 면제 라우트로 등록.
- **에러 응답 형식**: 모든 응답은 `{"error":{"code":"...","message":"..."}}`. healthz의 503도 같은 형식.
- **응답 timestamp**: 이 step에선 timestamp 응답이 거의 없지만, 향후 step의 마샬링 헬퍼 자리(`internal/util/jsontime.go` 등)를 미리 만들어 두면 좋다(필수는 아님).

## Acceptance Criteria

`.worktrees/backend/`에서 실행. `docker compose up -d db`가 떠 있고 step1(phase1)의 마이그레이션이 적용되어 있어야 한다.

```bash
set -a; source ../../.env; set +a
export TEST_DATABASE_URL="${TEST_DATABASE_URL:-$DATABASE_URL}"   # 임시: 별도 test DB 권장이나 미설정 시 fallback

cd backend

go mod tidy
go vet ./...
go build ./...

# 단위만 빠르게
go test -short -race ./...

# 통합 포함 (실 DB)
go test -race -tags=integration ./...

# hashpw 동작 확인 (bcrypt 표준 prefix 검사 — dollar 기호는 셸 변수로 우회해 step.md 평문 매치 회피)
HASH_OUT=$(go run ./cmd/hashpw "test1234A")
python3 -c "import re,sys,os; sys.exit(0 if re.match(chr(36)+r'2[ab]'+chr(36)+r'12'+chr(36), os.environ['HASH_OUT']) else 1)" HASH_OUT="$HASH_OUT"

# server 기동 + healthz 응답 (백그라운드 기동 → curl → 종료)
go build -o bin/server ./cmd/server
PORT=18080 DATABASE_URL="$DATABASE_URL" ./bin/server &
SERVER_PID=$!
sleep 1
curl -fsS http://localhost:18080/api/healthz | grep -q '"status":"ok"'
kill $SERVER_PID
wait $SERVER_PID 2>/dev/null || true
```

추가 ad-hoc 검증:

1. `apperr.FromDBError`가 `members_branch_phone_unique` 위반 → 409 `PHONE_DUPLICATE`로 매핑 (단위 테스트로).
2. `testutil.SetupDB`/`TruncateAll`/`CreateBranch`/`CreateAdmin`/`CreateMember` 호출 후 DB에 행이 적재됨 (smoke 통합 테스트).
3. `cmd/hashpw`가 빈 인자 → exit 1, 평문 비번을 출력하지 않음.
4. `bin/server` 종료 시 30초 미만으로 깔끔히 끝남(SIGTERM 핸들러 동작).

## 검증 절차

1. 위 AC 명령을 직접 실행한다.
2. **`code-reviewer` 서브에이전트를 Task tool로 호출해 변경 사항을 검증받는다.** 입력: step 이름(`phase2-backend-scaffold/scaffold`), `git diff HEAD --stat`. PASS 응답이 나와야 다음 단계로.
3. 결과에 따라 `phases/phase2-backend-scaffold/index.json`의 step1 status 업데이트:
   - PASS → `"status": "completed"` + `"summary": "go.mod 초기화; internal/config·apperr(pgx 23xxx/40xxx 매핑)·testutil(SetupDB/TruncateAll/Create*/AuthRequest/FreezeTime/Clock·UUIDGen) 추가; cmd/server graceful shutdown + GET /api/healthz; cmd/hashpw bcrypt 12; pgxpool 25/2/5m/1h + SET TIME ZONE UTC."`
   - 3회 재시도 후에도 실패 → `"status": "error"` + `"error_message"`
   - 사용자 개입 필요(`TEST_DATABASE_URL`/`DATABASE_URL` 미설정, docker 컨테이너 미기동, ADR 외 라이브러리 추가 필요 등) → `"status": "blocked"` + `"blocked_reason"` 후 즉시 중단

## 금지사항

- `frontend/` 변경 금지. 공유 파일(`docs/`, 루트 `.env.example`, `docker-compose.yml`, 루트 `CLAUDE.md`, `.gitignore`, `scripts/`, `.claude/`) 변경 금지.
- 이 step에서 핸들러 라우트를 `healthz` 외에 추가하지 마라. auth/members/memberships 등은 다음 step.
- ADR-008에 없는 라이브러리 추가 금지. 추가가 필요하면 `blocked` 처리 후 사용자와 ADR 갱신.
- `time.Now()`·`uuid.New()`를 도메인 코드(`internal/domain`)에서 직접 호출 금지. 인터페이스를 통해서만.
- `SET TIME ZONE 'UTC'`를 누락하지 마라(누락 시 step8의 KST 배치가 환경 종속이 됨).
- **JWT 비밀키·DB 비밀번호·평문 비번을 로그·에러 메시지·응답에 포함 금지.**
- `goose` 외부 바이너리에 의존하는 testutil 작성 금지(코드로 마이그레이션 실행).
- `migrations/`·`seeds/`(phase1 산출물) 변경 금지 — 이 step은 backend 코드만.
