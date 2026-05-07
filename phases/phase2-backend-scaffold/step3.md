---
agent: backend
depends_on: [middleware]
---

# Step 3: 인증·세션 (login/refresh/logout/password) + Auth 미들웨어 + 로그인 잠금

## 목표

관리자 인증 흐름 전체와 그 가드를 한 step에 끝낸다. 다음 step부터는 모든 보호 라우트가 이 미들웨어 위에서 동작한다.

산출물:
- `internal/auth/jwt.go` — access/refresh JWT 발급·검증 (HS256)
- `internal/auth/password.go` — bcrypt 해시·검증, 비번 강도 검증, 임시 비번 생성기
- `internal/repo/admins_repo.go` — 인증에 필요한 메서드(`FindByUsername`, `RecordLoginSuccess`, `RecordLoginFailure`, `UpdatePassword`, `RevokeAllRefreshTokens`)
- `internal/repo/refresh_tokens_repo.go` — `IsRevoked(jti)`, `Revoke(jti, adminID)`
- `internal/http/middleware/auth.go` — claim 검증 + admin row 검증 (1쿼리: `deleted_at IS NULL` + `password_updated_at`)
- `internal/http/middleware/guards.go` — `MustChangePasswordGuard`, `RequireGlobal`, `RequireBranch`
- `internal/http/admins_auth.go` — `POST /api/admin/{login,refresh,logout,password}`
- audit 자동 기록(login_success/failure/logout/password_change) 호출 박기

## 읽어야 할 파일

- `CLAUDE.md`, `backend/CLAUDE.md` (인증·세션 섹션 통째로)
- `db/CLAUDE.md` (admins/revoked_refresh_tokens/admin_audit_logs)
- `docs/API.md` — 로그인/refresh/logout/password 라우트 명세, 에러 코드(`ACCOUNT_LOCKED`, `TEMP_PASSWORD_EXPIRED`, `WEAK_PASSWORD`, `MUST_CHANGE_PASSWORD`, `UNAUTHORIZED`)
- `docs/TESTING.md` — 인증 테스트 카탈로그
- step1·2 산출물: `internal/{config,apperr,testutil,audit}`, middleware 묶음, `WithTx`

## 작업

### 1. `internal/auth/jwt.go`

```go
type AccessClaims struct {
    Sub                 int64  `json:"sub"`
    Username            string `json:"username"`
    Role                string `json:"role"`               // "global" | "branch"
    BranchID            *int64 `json:"branch_id,omitempty"`
    MustChangePassword  bool   `json:"must_change_password"`
    Iat                 int64  `json:"iat"`
    Exp                 int64  `json:"exp"`
}

type RefreshClaims struct {
    Sub int64  `json:"sub"`
    Jti string `json:"jti"`   // UUIDv4
    Iat int64  `json:"iat"`
    Exp int64  `json:"exp"`
}

type Issuer struct {
    AccessSecret  []byte
    RefreshSecret []byte
    Clock         testutil.Clock
    UUIDGen       testutil.UUIDGen
}

func (i *Issuer) IssueAccess(c AccessClaims) (token string, err error)   // exp = now + 30m
func (i *Issuer) IssueRefresh(adminID int64) (token string, jti string, err error)  // exp = now + 15h
func (i *Issuer) ParseAccess(token string) (*AccessClaims, error)
func (i *Issuer) ParseRefresh(token string) (*RefreshClaims, error)
```

- HS256만 지원. `alg=none`/RS256 거부(공격 방어).
- 파싱 후 **claim 무결성 검증**: `Sub`/`Role`/`Iat`/`Exp` 필수. 누락·타입 오류 → `apperr.New(401, "UNAUTHORIZED", ...)`.
- 시간/UUID는 인터페이스로 주입.

단위 테스트: 발급/파싱 라운드트립, 만료 토큰 거부, 시그니처 변조 거부, 필수 claim 누락 거부, alg=none 거부.

### 2. `internal/auth/password.go`

```go
func HashPassword(plain string) (string, error)            // bcrypt cost 12
func VerifyPassword(hash, plain string) error              // nil = 일치
func ValidateStrength(plain string) error                  // 8자+영문+숫자, 미달 시 apperr 400 WEAK_PASSWORD
func GenerateTempPassword(rng io.Reader) (string, error)   // 12자, charset 헷갈리는 문자 제외
```

`GenerateTempPassword` charset(헷갈리는 `0/O/I/l/1`·소문자 `o/i` 제외):
`ABCDEFGHJKLMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789`

- `crypto/rand.Reader`를 기본으로 받되, 테스트용 fake `io.Reader` 주입 가능하게.
- 단위 테스트: 길이 12, charset 외 문자 없음, 분포 smoke(엔트로피 검증은 과함, 1만 회 생성 후 charset 외 0건만 확인).

### 3. `internal/repo/admins_repo.go` (이 step에 필요한 메서드만)

```go
type AdminRow struct {
    ID                       int64
    Username                 string
    PasswordHash             string
    MustChangePassword       bool
    TempPasswordExpiresAt    *time.Time
    Role                     string
    BranchID                 *int64
    PasswordUpdatedAt        *time.Time
    FailedLoginCount         int
    LockedUntil              *time.Time
    DeletedAt                *time.Time   // 미들웨어가 IS NULL 강제, 응답에는 노출 안 함
}

// 모두 deleted_at IS NULL 강제. soft-deleted는 not found(*AppError 401 또는 404 — caller 결정).
func FindByUsername(ctx, tx Querier, username string) (*AdminRow, error)
func FindByID(ctx, tx Querier, id int64) (*AdminRow, error)

// 단일 SELECT로 (deleted_at IS NULL) + password_updated_at 동시 조회. Auth 미들웨어용.
type AdminAccessCheck struct {
    PasswordUpdatedAt *time.Time   // NULL이면 첫 발급 후 비번 미변경
    Exists            bool          // false면 soft-deleted 또는 없음
}
func GetForAccessCheck(ctx, tx Querier, id int64) (AdminAccessCheck, error)

// 로그인 성공/실패 기록.
func RecordLoginSuccess(ctx, tx Querier, id int64, now time.Time) error
// 비번 틀림 시 failed_login_count += 1, 5에 도달하면 locked_until = now + 15m.
func RecordLoginFailure(ctx, tx Querier, id int64, now time.Time) error

// 비번 변경: hash 갱신 + must_change_password=false + temp_password_expires_at=NULL +
//          password_updated_at=now + failed_login_count=0 + locked_until=NULL.
func UpdatePassword(ctx, tx Querier, id int64, newHash string, now time.Time) error
```

### 4. `internal/repo/refresh_tokens_repo.go`

```go
func IsRevoked(ctx, tx Querier, jti string) (bool, error)
func Revoke(ctx, tx Querier, jti string, adminID int64, now time.Time) error
func RevokeAllForAdmin(ctx, tx Querier, adminID int64, now time.Time) error
```

- jti 충돌(같은 jti 두 번 revoke)은 INSERT … ON CONFLICT DO NOTHING로 흡수.
- `RevokeAllForAdmin`은 **그 사용자가 발급받은 모든 미만료 refresh 토큰을 한꺼번에 무효화** — 우리는 jti 목록을 미리 알 수 없으므로 DB에 발급 이력 테이블이 따로 없는 한 불가능. **대신 `admins.password_updated_at`을 갱신해 access 무효화 + refresh의 경우 별도 테이블 `revoked_refresh_tokens`에 미리 저장된 jti를 무효화하는 단순 모델**로 가되, 비번 변경/계정 삭제/branch_id 변경 시에는 모든 미사용 refresh 토큰을 거부해야 한다.
  - 구현 방식: **issued_refresh_tokens** 테이블이 없으므로, `admins.password_updated_at`을 refresh 검증 시에도 비교한다 — refresh claim의 `iat < password_updated_at`이면 거부. (access와 같은 무효화 모델). branch_id 변경 시점도 마찬가지로 별도 컬럼(예: `tokens_invalidated_at`)을 둘 수 있으나, MVP는 `password_updated_at` 한 컬럼으로 통합 사용. 이 결정을 **이 step에서 확정하고 코드 주석에 명시**한다. 추후 별도 컬럼이 필요해지면 마이그레이션을 추가한다(이 step 범위 밖).
  - 즉 **`RevokeAllForAdmin`의 실제 구현은 `password_updated_at = now()`로 갱신** + 명시적 logout으로 전달된 jti는 `revoked_refresh_tokens`에 INSERT.

단위 테스트: `Revoke` 후 `IsRevoked` true, 다른 jti는 false. 동일 jti 두 번 Revoke 무에러(멱등).

### 5. `internal/http/middleware/auth.go`

```go
func RequireAuth(issuer *auth.Issuer, pool *pgxpool.Pool) gin.HandlerFunc
```

흐름:
1. `Authorization: Bearer <token>` 추출. 없으면 401 `UNAUTHORIZED`.
2. `issuer.ParseAccess(token)` → 실패 시 401 (만료·시그 변조·필드 누락 모두 같은 코드).
3. `repo.GetForAccessCheck(ctx, pool, claims.Sub)` 호출(단일 SELECT):
   - `Exists=false` → 401 (soft-deleted)
   - `claims.Iat < PasswordUpdatedAt` → 401 (비번 변경 후 stale access)
4. context에 `admin_id`, `role`, `branch_id`, `must_change_password` 박기 + logger 미들웨어가 admin_id를 읽도록.

단위 테스트: 정상 통과, 토큰 없음 401, 만료 401, soft-deleted admin 401, password 변경 후 stale access 401, 필드 누락 401.

### 6. `internal/http/middleware/guards.go`

```go
func MustChangePasswordGuard() gin.HandlerFunc
// must_change_password=true인 토큰은 /api/admin/{password,logout,refresh} 외 모든 라우트에서 403 MUST_CHANGE_PASSWORD.

func RequireGlobal() gin.HandlerFunc
// role != "global"이면 403 FORBIDDEN.

func RequireBranch() gin.HandlerFunc
// role != "branch"이면 403 FORBIDDEN. (지점 전용 라우트는 사실상 없으나, 스펙상 정의)
```

### 7. `internal/http/admins_auth.go` — 4개 핸들러

#### `POST /api/admin/login`

요청: `{ "username", "password" }`

흐름 (단일 트랜잭션 — `WithTx`):
1. `FindByUsername(username)` — 없거나 soft-deleted면 **401 `UNAUTHORIZED`** (username 노출 회피, 일관 응답).
2. `locked_until > now`면 401 `ACCOUNT_LOCKED` + body에 `locked_until`(KST `+09:00`) 포함.
3. `must_change_password && temp_password_expires_at < now` → 401 `TEMP_PASSWORD_EXPIRED`.
4. bcrypt 비교:
   - **불일치**: `RecordLoginFailure` (counter += 1, 5 도달 시 `locked_until = now + 15m`) → audit `login_failure` (admin_id, metadata: 사유). 401 `UNAUTHORIZED`.
   - **일치**: `RecordLoginSuccess` (counter = 0, last_login_at = now). audit `login_success`.
5. access(30m) + refresh(15h) 발급. 응답: `{ access_token, refresh_token, must_change_password, role, branch_id, username }`.

#### `POST /api/admin/refresh`

요청: `{ "refresh_token" }`

흐름:
1. `ParseRefresh` → 실패 시 401.
2. `IsRevoked(jti)` → true면 401.
3. `FindByID(sub)` → 없거나 soft-deleted면 401.
4. `claims.Iat < admin.PasswordUpdatedAt` → 401 (비번 변경 후 stale refresh).
5. 새 access 발급(refresh 자체는 재발급 안 함 — 만료까지 사용). 응답: `{ access_token, must_change_password, role, branch_id, username }`.

#### `POST /api/admin/logout`

요청: `{ "refresh_token" }`. 인증은 access(`RequireAuth`) + refresh body 동시 검증.

흐름:
1. `ParseRefresh` (만료된 토큰도 jti 추출 시도 — sub 일치만 검증). 토큰 형식 자체가 깨지면 400 `INVALID_INPUT`.
2. claims.Sub == `c.GetInt64("admin_id")` 검증, 아니면 403.
3. `Revoke(jti, adminID)` (멱등).
4. audit `logout`.
5. 204 No Content.

#### `POST /api/admin/password`

인증: access (`RequireAuth`). `MustChangePasswordGuard`가 막지 않는 라우트.

요청: `{ "current_password", "new_password" }`

흐름 (`WithTx`):
1. `ValidateStrength(new_password)` 실패 → 400 `WEAK_PASSWORD`.
2. `FindByID(adminID)` → `VerifyPassword(hash, current_password)` 실패 → 401 `UNAUTHORIZED`.
3. `HashPassword(new_password)` → `UpdatePassword`(hash 갱신 + flags 리셋 + `password_updated_at=now`).
4. **그 사용자의 모든 refresh 토큰 무효화** = `password_updated_at` 갱신만으로 충족(refresh 검증 시 `iat < password_updated_at`로 차단). 명시적으로 발급된 jti가 클라가 다시 보내올 가능성도 있으니 `revoked_refresh_tokens`에 별도 INSERT는 안 함(jti를 모르므로). access는 동일 모델로 다음 요청에서 401.
5. audit `password_change`.
6. 204 No Content.

### 8. 라우터 통합

```go
// cmd/server/main.go
issuer := &auth.Issuer{...}
authGroup := r.Group("/api/admin")
authGroup.POST("/login", admins_auth.Login(...))
authGroup.POST("/refresh", admins_auth.Refresh(...))

protected := r.Group("/api")
protected.Use(middleware.RequireAuth(issuer, pool))
protected.Use(middleware.MustChangePasswordGuard())
{
    // /api/admin/logout, /api/admin/password는 must_change_password 면제
    adminScoped := r.Group("/api/admin")
    adminScoped.Use(middleware.RequireAuth(issuer, pool))
    adminScoped.POST("/logout", admins_auth.Logout(...))
    adminScoped.POST("/password", admins_auth.PasswordChange(...))
}
```

`MustChangePasswordGuard`는 path를 보고 `/api/admin/{logout,password,refresh}`는 통과시킨다.

### 9. 핸들러 테스트 (httptest e2e)

각 라우트별 정상 + 카탈로그 에러 케이스 모두:

- **login**: 정상, 비번 틀림, 미존재 username, soft-deleted admin, 잠금 중, 임시 비번 만료, must_change_password=true 응답 플래그 확인
- **refresh**: 정상, 만료, 변조, jti revoked, 비번 변경 후 stale, soft-deleted admin
- **logout**: 정상, sub 불일치 403, 형식 깨진 refresh 400, 같은 jti 두 번 호출 멱등
- **password**: 정상, 현재 비번 틀림 401, 약한 비번 400, 변경 후 같은 access로 다음 요청 401, 같은 refresh로 다음 refresh 401

ROADMAP 검증 기준의 "5번 틀리고 6번째 정확해도 401 ACCOUNT_LOCKED, 15분 후 가능"·"reset-password 24h 만료 후 401 TEMP_PASSWORD_EXPIRED"·"전역 관리자가 reset-password 호출"은 step4에서 reset-password 라우트가 생긴 뒤 재검증.

## 핵심 규칙 (반드시 박는다)

- **claim 무결성**: 필수 필드 누락·타입 오류는 모두 401 `UNAUTHORIZED`(위조 토큰 차단).
- **Auth 미들웨어 1쿼리**: `GetForAccessCheck`는 deleted_at + password_updated_at을 한 SELECT로 조회. round-trip을 늘리지 마라.
- **로그인 응답 일관성**: 미존재 username과 비번 틀림은 같은 401 `UNAUTHORIZED`로 통일(username 존재 여부 노출 회피).
- **잠금/임시 비번 만료/약한 비번 분기**: 각각 정확한 코드(`ACCOUNT_LOCKED`, `TEMP_PASSWORD_EXPIRED`, `WEAK_PASSWORD`).
- **비번 변경 후 stale access/refresh 동시 무효화**: `password_updated_at` 갱신 + access/refresh 모두 `iat < password_updated_at`로 차단. 본인 디바이스도 다음 요청에서 강제 재로그인.
- **로그·에러 메시지·응답에 평문 비번/JWT/refresh 토큰 미포함**: 핸들러 에러 message에도 토큰 prefix 노출 금지.
- **`POST /api/admin/login` 응답 body는 access·refresh 둘 다 포함**: refresh를 쿠키로 내리지 마라(쿠키 미사용).
- **인증 라우트에 rate limit**: step2의 `Limiter`를 `/api/admin/login`·`/api/admin/refresh`에 적용.
- **로그인 성공 시 카운터 0 리셋, locked_until 경과 후 첫 시도가 정확하면 정상 처리**(0으로 리셋), 틀리면 1부터 다시 누적.
- **audit 호출은 핸들러 내부에서**(미들웨어 hook으로 자동화하지 마라 — step3·4 모두 명시적 호출).

## Acceptance Criteria

사전조건: 루트 `.env`에 다음 키가 채워져 있어야 한다(값 자체는 본 step.md에 노출하지 않는다 — Phase 0에서 주입).

- `DATABASE_URL`
- `JWT_ACCESS_SECRET`, `JWT_REFRESH_SECRET` (각 32바이트 이상의 무작위 문자열)
- `SEED_ADMIN_USERNAME`, `SEED_ADMIN_PASSWORD` (시드 적용 시 사용된 평문 — 첫 로그인 검증용)

```bash
set -a; source ../../.env; set +a
export TEST_DATABASE_URL="${TEST_DATABASE_URL:-$DATABASE_URL}"

# 비어있으면 사용자 개입 필요 (이 step의 e2e 검증은 토큰 발급이 핵심).
test -n "$JWT_ACCESS_SECRET" && test -n "$JWT_REFRESH_SECRET" || { echo "JWT 비밀키 미설정"; exit 1; }

cd backend
go vet ./...
go build ./...
go test -short -race ./...
go test -race -tags=integration ./...

# 시드된 전역 관리자(.env의 SEED_ADMIN_USERNAME)로 e2e 흐름.
# 환경변수는 set -a; source 로 이미 export 되어 있으므로 server 기동 라인에 재나열하지 않는다.
go build -o bin/server ./cmd/server
PORT=18080 APP_ENV=dev ./bin/server &
SERVER_PID=$!
sleep 1

# 시드 비번 평문은 .env에서만 읽고 출력하지 않는다.
LOGIN_RESP=$(curl -fsS -X POST http://localhost:18080/api/admin/login \
  -H 'Content-Type: application/json' \
  --data-binary @<(python3 -c "import json,os; print(json.dumps({'username':os.environ['SEED_ADMIN_USERNAME'],'password':os.environ['SEED_ADMIN_PASSWORD']}))"))
echo "$LOGIN_RESP" | python3 -c "import json,sys; d=json.load(sys.stdin); assert d['must_change_password']==True; assert d['role']=='global'; assert d['access_token']; assert d['refresh_token']"

kill $SERVER_PID
wait $SERVER_PID 2>/dev/null || true
```

추가 단위/통합 테스트 자가 점검:
- 5회 비번 틀림 → 6번째 정확해도 401 `ACCOUNT_LOCKED`. `FreezeTime`으로 15분 경과 시뮬 → 정확한 비번이 통과.
- refresh 정상 흐름: login → 30분 경과 시뮬 → access 만료 → refresh로 새 access 수령.
- logout → 같은 refresh로 refresh 시 401 (jti revoked).
- 비번 변경 → 변경 전 access로 다음 요청 401, 변경 전 refresh로 refresh 401.
- soft-deleted admin의 access로 호출 → 401.
- 약한 비번(`a1234567`은 통과, `aaaaaaaa`는 400, `12345678`은 400, 7자는 400).

## 검증 절차

1. 위 AC 명령을 직접 실행한다.
2. `code-reviewer` 서브에이전트를 Task tool로 호출. 입력: 단계 이름(`phase2-backend-scaffold/auth`), `git diff HEAD --stat`. PASS 응답 필요.
3. 결과에 따라 step3 status 업데이트:
   - PASS → `"status": "completed"` + `"summary": "/api/admin/login·refresh·logout·password 핸들러 + Auth 미들웨어(1쿼리 검증) + Must/Role 가드 + 로그인 잠금(5회·15분) + 임시 비번 만료 + 비번 강도 + JWT issuer + refresh 무효화(password_updated_at 통합) + audit 자동 기록(login_success/failure/logout/password_change) 추가."`
   - error/blocked는 동일 패턴.

## 금지사항

- `frontend/`·공유 파일 변경 금지.
- HS256 외 알고리즘 허용 금지(`alg=none` 거부).
- 비번 평문/JWT/refresh 토큰을 로그·에러 메시지·응답 본문(login/refresh 외)에 포함 금지.
- Auth 미들웨어가 매 요청 1쿼리 초과 호출 금지(deleted_at + password_updated_at 한 SELECT).
- username 미존재와 비번 틀림에 다른 코드/메시지 사용 금지(둘 다 `UNAUTHORIZED`).
- audit INSERT 실패가 핸들러 핵심 흐름을 막지 않게(slog로만 남기고 응답은 정상).
- 임시 비번을 audit metadata나 logger 필드에 포함 금지(평문 보존 금지).
- ADR 외 라이브러리 추가 금지.
- `MustChangePasswordGuard`가 `/api/admin/{logout,password,refresh}`를 막지 않게(예외 처리 누락 금지).
- step4의 reset-password 라우트는 여기서 만들지 마라(다음 step).
