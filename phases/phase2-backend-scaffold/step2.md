---
agent: backend
depends_on: [scaffold]
---

# Step 2: 미들웨어 묶음 + 트랜잭션 retry 헬퍼

## 목표

step1에서 만든 스캐폴드 위에 **횡단 관심사**(횟수에 관계없이 모든 라우트에 적용되는 공통 처리)를 한 번에 깐다. 이 step이 끝나면 다음 step의 auth 핸들러가 별도 미들웨어 작업 없이 핵심 비즈니스 로직만 작성하면 된다.

산출물:
- `internal/http/middleware/{requestid,logger,recovery,cors,bodylimit,ratelimit}.go`
- `internal/repo/tx.go` — 트랜잭션 retry 헬퍼 (`WithTx`)
- `internal/audit/audit.go` — audit INSERT 헬퍼 (호출은 step3·4에서 박는다, **여기선 함수만**)
- HTTP 서버 timeout/body limit/CORS 설정이 cmd/server에 통합

## 읽어야 할 파일

- `CLAUDE.md` (루트)
- `backend/CLAUDE.md` — 미들웨어 책임 분리 섹션, slog 필드, HTTP timeout, body size, trusted proxies, CORS, 트랜잭션 retry 정책
- `db/CLAUDE.md` — `admin_audit_logs` 테이블(action enum, metadata jsonb, ip inet)
- `docs/API.md` — 에러 코드 카탈로그(429 `RATE_LIMITED`, 400 `BODY_TOO_LARGE`, 500 `INTERNAL`)
- `docs/ARCHITECTURE.md` — 단일 인스턴스 가정(rate limit·LRU 메모리)
- step1 산출물: `internal/config`, `internal/apperr`, `internal/testutil`, `cmd/server/main.go`

## 작업

### 1. `internal/http/middleware/requestid.go`

- `X-Request-ID` 요청 헤더가 있으면 그대로, 없으면 `uuid.NewV4()`로 발급.
- 응답 헤더에 같은 값 echo.
- gin context에 key `"request_id"`로 저장. 이후 모든 미들웨어/핸들러는 `c.GetString("request_id")`로 접근.
- 단위 테스트: 헤더 없을 때 발급, 있을 때 통과(단, **외부에서 들어온 값을 검증 — UUIDv4 형식이 아니면 새로 발급**, 보안: 임의 ID 주입 방지).

### 2. `internal/http/middleware/logger.go`

slog 액세스 로그. 요청 종료 시 한 줄.

필드: `request_id`, `admin_id`(있으면 — auth 미들웨어가 context에 박은 값), `ip`(`c.ClientIP()`), `method`, `path`, `status`, `duration_ms`, `error_code`(응답 body에서 추출 가능 시).

**금지 필드**: 비밀번호, JWT, refresh token, `phone`, `birth_date`, 평문 임시 비번.

단위 테스트: slog 핸들러를 in-memory로 갈아끼워 호출 후 record가 위 필드를 포함하는지 검증, PII 키워드(`phone`/`password`/`birth_date`)가 포함되지 않는지 검증.

### 3. `internal/http/middleware/recovery.go`

- panic 발생 시 stack trace를 slog로(필드: `error`, `stack`, `request_id`).
- 응답은 항상 500 + `{"error":{"code":"INTERNAL","message":"internal server error"}}`.
- `cfg.AppEnv == "dev"`일 때만 응답 message에 panic value의 짧은 string 포함(stack은 항상 응답 미노출).
- gin 기본 Recovery 대신 직접 작성(slog 필드 + apperr 형식 유지).

단위 테스트: panic을 일으키는 가짜 핸들러로 status 500·body 형식·stack 미노출 검증.

### 4. `internal/http/middleware/cors.go`

```go
func CORS(allowOrigin string) gin.HandlerFunc
```

응답 헤더:
- `Access-Control-Allow-Origin: <allowOrigin>` (와일드카드 금지, 빈 문자열이면 헤더 미설정)
- `Access-Control-Allow-Methods: GET, POST, PATCH, DELETE, OPTIONS`
- `Access-Control-Allow-Headers: Authorization, Content-Type, Idempotency-Key, X-Request-ID`
- `Access-Control-Allow-Credentials: false`
- `Access-Control-Max-Age: 86400`

OPTIONS 요청은 위 헤더만 세팅하고 **204 No Content** 즉시 반환(다음 핸들러 호출 안 함).

단위 테스트: OPTIONS preflight → 204 + 모든 헤더, GET 일반 요청 → 헤더 부착 + 핸들러 통과, 와일드카드 origin 입력 거부(또는 무시).

### 5. `internal/http/middleware/bodylimit.go`

- `gin.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)` 적용.
- 1MB 초과 → 400 `BODY_TOO_LARGE` (apperr 형식). MaxBytesReader는 핸들러가 read 시점에 에러를 내므로, recovery 미들웨어 또는 별도 wrapper에서 잡아 변환.

단위 테스트: 1MB 미만 정상, 초과 400.

### 6. `internal/http/middleware/ratelimit.go`

IP 단위 토큰 버킷(15분당 60회). 인-프로세스 메모리(단일 인스턴스 가정 — 이 사실을 코드 주석에 명시).

```go
type Limiter struct {
    // sync.Mutex로 보호되는 map[ip]bucket. bucket = (tokens float64, lastRefill time.Time)
}
func NewLimiter(window time.Duration, max int) *Limiter
func (l *Limiter) Allow(ip string) bool       // false면 429
func (l *Limiter) Middleware() gin.HandlerFunc
```

- 초과 시 429 + `{"error":{"code":"RATE_LIMITED","message":"..."}}` + `Retry-After: <초>` 헤더.
- 인증 라우트(`POST /api/admin/login`, `POST /api/admin/refresh`)에 우선 적용. healthz는 면제(라우터에서 미들웨어 그룹 분리).
- map 누수 방지: `Allow` 호출 시 마지막 사용으로부터 15분 지난 엔트리는 lazy 정리.

단위 테스트: 60회까지 통과, 61회째 429, 윈도 경과 후 다시 통과. `time.Now`는 `Clock` 인터페이스로 주입 가능하게 — testutil의 `FreezeTime` 활용.

### 7. `internal/repo/tx.go` — 트랜잭션 retry 헬퍼

```go
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error
```

- `pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})` 시작 → `fn(tx)` 호출 → 성공 시 commit, 실패 시 rollback.
- `fn`이 PostgreSQL `40001`(serialization_failure) 또는 `40P01`(deadlock_detected)을 반환하면 **최대 3회 재시도**, backoff `50ms` → `100ms` → `200ms`.
- 재시도 시 매번 새 tx 시작.
- 그 외 에러는 즉시 반환(rollback 후).
- ctx 취소는 즉시 반환 + rollback.

검출은 `errors.As(err, *pgconn.PgError)` 후 `pgErr.Code == "40001" || "40P01"`.

단위 테스트:
- 정상 commit
- 첫 호출에서 40001 반환 → 두 번째 시도에서 성공 (호출 횟수 2)
- 3회 모두 40001 → 마지막 에러 반환 (호출 횟수 3)
- 40001 외 에러는 즉시 1회 반환 후 종료

### 8. `internal/audit/audit.go` — audit INSERT 헬퍼만

```go
type Action string
const (
    LoginSuccess  Action = "login_success"
    LoginFailure  Action = "login_failure"
    Logout        Action = "logout"
    PasswordChange Action = "password_change"
    PasswordReset  Action = "password_reset"
    AdminCreate   Action = "admin_create"
    AdminUpdate   Action = "admin_update"
    AdminDelete   Action = "admin_delete"
    BranchCreate  Action = "branch_create"
    BranchUpdate  Action = "branch_update"
    BranchDelete  Action = "branch_delete"
)

type Entry struct {
    AdminID    *int64       // 로그인 실패는 NULL 가능
    Action     Action
    TargetType *string      // "admin" | "branch" | nil
    TargetID   *int64
    IP         string       // c.ClientIP() — inet으로 INSERT
    UserAgent  string
    Metadata   map[string]any  // request_id 포함
}

func Log(ctx context.Context, pool *pgxpool.Pool, e Entry) error
```

- INSERT는 동기. 실패 시 slog로만 남기고 caller에는 nil 반환(audit 실패가 핵심 흐름을 막지 않게) — **단, INSERT 에러 자체는 반드시 로그로 남긴다**.
- `metadata`는 jsonb로 직렬화. `request_id`는 caller가 채워서 전달.
- ip는 `inet` 타입이므로 빈 문자열은 NULL로 변환.

이 step에선 함수 정의 + 단위 테스트(가짜 pool에 INSERT 동작)만. **호출은 step3·4의 핸들러에서 박는다** — 이 step에서 미리 박지 마라.

### 9. `cmd/server/main.go` 통합

기존 step1의 main에 다음을 끼워넣는다:

```go
// 라우트 그룹 분리:
r := gin.New()
r.Use(middleware.RequestID())
r.Use(middleware.Logger(...))
r.Use(middleware.Recovery(cfg.AppEnv))
r.Use(middleware.CORS(cfg.CORSOrigin))
r.Use(middleware.BodyLimit())

// 면제 그룹 (healthz)
r.GET("/api/healthz", healthHandler)

// 인증 그룹 (rate limit 적용 — 로그인/refresh)
authGroup := r.Group("/api/admin")
authGroup.Use(rateLimiter.Middleware())
// 로그인 라우트는 step3에서 등록. 여기선 그룹 자리만 만들어둔다.
```

trusted proxies는 `cfg.AppEnv == "prod"`일 때 `gin.SetTrustedProxies([]string{...})` 호출 — 호스팅 결정 전이므로 빈 슬라이스 또는 `nil`로 두고 TODO 주석.

HSTS: `cfg.AppEnv == "prod"`일 때 응답에 `Strict-Transport-Security: max-age=31536000; includeSubDomains` 헤더 추가(미들웨어로). dev에서는 미적용.

### 10. healthz 미들웨어 면제 검증

기존 healthz 테스트가 인증·rate limit 없이 200을 받는지 재확인. 미들웨어 추가로 깨진다면 라우터 등록 순서 점검.

## 핵심 규칙 (반드시 박는다)

- **미들웨어 책임 분리**: requestid → logger → recovery → cors → bodylimit → (ratelimit은 라우트 그룹별). 위 순서를 바꾸면 logger가 request_id를 못 주워 담는 등 부작용. 통합 테스트로 순서 검증.
- **PII/시크릿 미로깅**: logger 미들웨어는 query string/body를 로그에 남기지 않는다(검색 q 파라미터의 phone 노출 가능성). path만.
- **응답 일관성**: 모든 에러 응답은 apperr 형식. recovery는 panic을 500 INTERNAL로, body limit은 400 BODY_TOO_LARGE로, rate limit은 429 RATE_LIMITED로.
- **단일 인스턴스 가정**: rate limit이 인-프로세스 메모리임을 코드 주석으로 명시 + 다중 인스턴스 시 Redis 마이그레이션 필요(루트 CLAUDE.md CRITICAL).
- **트랜잭션 retry는 idempotent해야 안전**: `WithTx`에 넘기는 `fn`은 외부 부수효과(파일 쓰기·HTTP 호출 등)를 가지면 안 된다. DB 작업만 — 주석으로 명시.
- **CORS 와일드카드 금지**: `Access-Control-Allow-Origin: *` 절대 출력하지 마라(보안 + credentials 호환).
- **healthz는 면제**: rate limit + auth 모두 면제. 모니터링 시스템이 헬스체크 폭주를 일으키면 안 됨.
- **audit 호출은 step3·4의 일**: 이 step에서 audit INSERT 호출을 하드코딩하지 마라(헬퍼만).

## Acceptance Criteria

```bash
set -a; source ../../.env; set +a
export TEST_DATABASE_URL="${TEST_DATABASE_URL:-$DATABASE_URL}"

cd backend
go vet ./...
go build ./...
go test -short -race ./...
go test -race -tags=integration ./...

# 서버 기동 + 미들웨어 동작 확인
go build -o bin/server ./cmd/server
PORT=18080 DATABASE_URL="$DATABASE_URL" CORS_ORIGIN="https://example.test" APP_ENV=dev ./bin/server &
SERVER_PID=$!
sleep 1

# Request ID echo
curl -fsS -i http://localhost:18080/api/healthz | grep -i '^x-request-id:' >/dev/null

# Request ID 외부 입력 echo
RID=$(uuidgen)
curl -fsS -i -H "X-Request-ID: $RID" http://localhost:18080/api/healthz | grep -i "^x-request-id: $RID" >/dev/null

# CORS preflight
curl -fsS -i -X OPTIONS \
  -H "Origin: https://example.test" \
  -H "Access-Control-Request-Method: POST" \
  -H "Access-Control-Request-Headers: Authorization, Content-Type, Idempotency-Key" \
  http://localhost:18080/api/admin/login | grep -E '^HTTP/.* 204' >/dev/null

# CORS 응답 헤더 6종
curl -fsS -i -X OPTIONS -H "Origin: https://example.test" http://localhost:18080/api/admin/login \
  | grep -i 'access-control-' | wc -l | grep -q '5'   # Origin/Methods/Headers/Credentials/Max-Age (5종)

# Body size limit (2MB → 400)
dd if=/dev/zero bs=1M count=2 2>/dev/null | base64 \
  | curl -fsS -o /tmp/body_resp -w '%{http_code}' -H "Content-Type: application/json" --data-binary @- \
    http://localhost:18080/api/admin/login | grep -q '400'

# panic recovery — 가짜 panic 라우트가 있다면(테스트용) status 500 + INTERNAL.
# 없으면 단위 테스트에서 검증. 운영 라우트는 step3 이후라 여기선 단위만으로 충분.

kill $SERVER_PID
wait $SERVER_PID 2>/dev/null || true
```

추가 단위 테스트 자가 점검:
- WithTx 정상/40001 1회/40001 3회/외 에러 즉시
- ratelimit 60/61/윈도 경과
- recovery panic → 500 + body 형식
- audit.Log INSERT가 admin_audit_logs에 정확히 한 row 적재 (target_type/id/metadata)

## 검증 절차

1. 위 AC 명령을 직접 실행한다.
2. `code-reviewer` 서브에이전트를 Task tool로 호출해 변경 사항을 검증받는다. 입력: step 이름(`phase2-backend-scaffold/middleware`), `git diff HEAD --stat`. PASS 응답이 나와야 다음 단계로.
3. 결과에 따라 `phases/phase2-backend-scaffold/index.json`의 step2 status 업데이트:
   - PASS → `"status": "completed"` + `"summary": "middleware(requestid/logger/recovery/cors/bodylimit/ratelimit) + WithTx retry(40001/40P01 3회) + audit INSERT 헬퍼 추가; cmd/server에 미들웨어 체인·HSTS·trusted proxies 통합."`
   - 3회 재시도 후에도 실패 → `"status": "error"` + `"error_message"`
   - 사용자 개입 필요 → `"status": "blocked"` + `"blocked_reason"`

## 금지사항

- `frontend/` 변경 금지. 공유 파일 변경 금지.
- 이 step에서 auth 핸들러(login/refresh/logout/password) 작성 금지 — step3.
- audit INSERT 호출을 핸들러에 박지 마라 — 헬퍼 정의만.
- `Access-Control-Allow-Origin: *` 출력 금지.
- gin 기본 `Recovery()`/`Logger()` 사용 금지(직접 작성 — slog 필드 + apperr 형식).
- rate limit·5초 LRU의 인-프로세스 메모리 가정을 변경하지 마라(다중 인스턴스 가정은 ADR-010 결정 후).
- ADR 외 라이브러리 추가 금지.
- recovery 미들웨어가 panic value를 응답에 그대로 노출 금지(prod). 짧은 message만 dev에서 허용.
- logger가 request body/query string을 로그에 남기지 마라(PII 노출 위험).
