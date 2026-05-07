---
agent: backend
depends_on: [auth]
---

# Step 4: 관리자·지점 CRUD + 임시 비번 리셋 + audit 자동 기록

## 목표

전역 관리자가 지점 관리자/지점을 운영하기 위한 라우트 묶음을 한 step에 마무리한다. 임시 비밀번호 리셋(`reset-password`)은 step3에서 만든 인증 흐름을 완성하는 마지막 조각이다. audit 자동 기록의 admin/branch 액션도 여기서 완성된다.

산출물:
- `internal/repo/branches_repo.go` — CRUD + 사용 중 검사
- `internal/repo/admins_repo.go` 확장 — `Create`, `Update`, `SoftDelete`, `ResetPassword`, `List`
- `internal/http/branches.go` — `GET/POST/PATCH/DELETE /api/branches`
- `internal/http/admins.go` — `GET/POST/PATCH/DELETE /api/admins`, `POST /api/admins/:id/reset-password`
- audit 호출 박기: `admin_create/update/delete`, `branch_create/update/delete`, `password_reset`

## 읽어야 할 파일

- `CLAUDE.md`, `backend/CLAUDE.md` (`### 관리자·지점` 섹션, 임시 비번 정책)
- `db/CLAUDE.md` — admins/branches 컬럼·CHECK·deleted_at·constraint 이름
- `docs/API.md` — `/api/admins/*`, `/api/branches/*` 명세, 에러 코드(`USERNAME_DUPLICATE`, `ADDRESS_DUPLICATE`, `BRANCH_IN_USE`, `CANNOT_DELETE_SELF`, `CANNOT_MODIFY_SELF_ROLE`, `INVALID_INPUT`)
- `docs/TESTING.md` — admin/branch 테스트 카탈로그
- step1·2·3 산출물: testutil, apperr, audit 헬퍼, JWT issuer, RequireAuth/RequireGlobal 가드, password 헬퍼(`GenerateTempPassword`, `HashPassword`)

## 작업

### 1. `internal/repo/branches_repo.go`

```go
type BranchRow struct {
    ID        int64
    Name      string
    Address   *string  // NULL 허용
    DeletedAt *time.Time
    CreatedAt time.Time
    UpdatedAt time.Time
}

// 모두 deleted_at IS NULL 강제.
func ListBranches(ctx, q Querier) ([]BranchRow, error)
func GetBranch(ctx, q Querier, id int64) (*BranchRow, error)
func InsertBranch(ctx, q Querier, name string, address *string) (int64, error)
func UpdateBranch(ctx, q Querier, id int64, name *string, address *string) error
// 사용 중 검사 + soft delete 한 트랜잭션. 사용 중이면 apperr 409 BRANCH_IN_USE.
func SoftDeleteBranch(ctx, tx pgx.Tx, id int64, now time.Time) error
// 위 SoftDelete가 호출하는 헬퍼.
func CountActiveMembers(ctx, q Querier, branchID int64) (int, error)
func CountActiveAdmins(ctx, q Querier, branchID int64) (int, error)
```

`InsertBranch`/`UpdateBranch`는 unique 위반(`branches_address_key`) 시 `apperr.FromDBError`가 409 `ADDRESS_DUPLICATE`로 변환.

### 2. `internal/repo/admins_repo.go` 확장

```go
type AdminListRow struct {
    ID                 int64
    Username           string
    Role               string
    BranchID           *int64
    BranchName         *string  // JOIN 결과. role=global이면 NULL.
    MustChangePassword bool
    LastLoginAt        *time.Time
    CreatedAt          time.Time
}

func ListAdmins(ctx, q Querier) ([]AdminListRow, error)   // deleted_at IS NULL 강제 + branches LEFT JOIN

type CreateAdminInput struct {
    Username  string
    Role      string  // "global" | "branch"
    BranchID  *int64
    PlainPassword string  // 호출자가 평문 전달 → repo가 hash
}
func CreateAdmin(ctx, tx pgx.Tx, in CreateAdminInput, now time.Time) (id int64, err error)
// must_change_password=true, temp_password_expires_at=now+24h, password_updated_at=NULL 자동.

type UpdateAdminInput struct {
    Username *string
    Role     *string
    BranchID *int64   // role 변경에 따라 NULL/non-NULL 자동 검증은 핸들러 책임
}
func UpdateAdmin(ctx, tx pgx.Tx, id int64, in UpdateAdminInput, now time.Time) error

func SoftDeleteAdmin(ctx, tx pgx.Tx, id int64, now time.Time) error
// soft delete + (이 사용자의) password_updated_at = now()로 갱신해 access/refresh 모두 무효화.

// 임시 비번 리셋: hash 갱신 + must_change_password=true + temp_password_expires_at=now+24h +
//                failed_login_count=0 + locked_until=NULL.
//                password_updated_at=now()도 갱신해 기존 토큰 모두 무효화.
func ResetPassword(ctx, tx pgx.Tx, id int64, newHash string, now time.Time) error
```

### 3. `internal/http/branches.go` — 4개 라우트

#### `GET /api/branches`

- 인증 면제는 아님 — 키오스크가 부르는 라우트는 step5의 `GET /api/branches`(공개 또는 별도 엔드포인트)와 충돌. 결정: **`GET /api/branches`는 인증 + must_change_password 가드 통과 후 모두 접근 가능**(전역·지점 관리자 둘 다). 키오스크 초기화는 별도 공개 라우트가 필요하면 step5에서 결정.
- 응답: `[{id, name, address, created_at, updated_at}]` (`deleted_at`은 응답 미노출)
- 정렬: `id ASC`(작은 운영, 페이지네이션 없음)

#### `POST /api/branches`

- `RequireGlobal` 가드.
- body: `{name, address?}`. name 1~50자, address는 NULL 또는 비-공백.
- 응답 201 + 생성된 row.
- audit `branch_create`.

#### `PATCH /api/branches/:id`

- `RequireGlobal`.
- body: `{name?, address?}` (둘 중 하나 이상). 나머지 필드 무시.
- soft-deleted 또는 미존재 → 404.
- 응답 200 + 갱신 row.
- audit `branch_update`.

#### `DELETE /api/branches/:id`

- `RequireGlobal`.
- `WithTx` 안에서 `CountActiveMembers`/`CountActiveAdmins` > 0이면 409 `BRANCH_IN_USE` (롤백).
- soft delete (`deleted_at = now()`).
- 응답 204.
- audit `branch_delete` (metadata에 카운트 포함).

### 4. `internal/http/admins.go` — 5개 라우트

#### `GET /api/admins`

- `RequireGlobal`.
- `ListAdmins` 호출. deleted_at IS NULL만. branch_name JOIN.
- 응답: `[{id, username, role, branch_id, branch_name, must_change_password, last_login_at, created_at}]`. **password_hash·temp_password_expires_at은 응답 미노출**(temp 만료는 응답 본문에 두지 않음 — 운영자에게 임시 비번 발급 시점에만 표시).

#### `POST /api/admins`

- `RequireGlobal`.
- body: `{username, password, role, branch_id?}`.
- 검증:
  - `ValidateStrength(password)` 미달 → 400 `WEAK_PASSWORD`.
  - `role=='branch'`인데 `branch_id` 없음 → 400 `INVALID_INPUT`.
  - `role=='global'`인데 `branch_id` 존재 → 400 `INVALID_INPUT`.
  - branch_id가 존재하지 않거나 soft-deleted → 400 `INVALID_INPUT`.
  - username 중복 → 409 `USERNAME_DUPLICATE` (apperr가 23505 자동 매핑).
- 응답 201 + `{id, username, role, branch_id, must_change_password: true}`. 비번 평문은 응답에 **포함하지 마라**(클라이언트가 입력한 비번을 그대로 운영자 화면에 노출 가능). 운영자가 비번을 모르는 상태로 만든 경우는 `reset-password`로 발급.
- audit `admin_create`.

#### `PATCH /api/admins/:id`

- `RequireGlobal`.
- body 화이트리스트: `{username?, role?, branch_id?}`. password/must_change_password/failed_login_count/locked_until/temp_password_expires_at은 무시.
- 검증:
  - 본인 행에서 role 또는 branch_id 변경 시도 → 409 `CANNOT_MODIFY_SELF_ROLE`.
  - role 변경 후 branch_id 일관성(global=NULL/branch=NOT NULL) 검증 → 400 `INVALID_INPUT`.
  - username 중복 → 409 `USERNAME_DUPLICATE`.
  - 대상이 soft-deleted 또는 미존재 → 404.
- branch_id 변경 시: `password_updated_at = now()`로 갱신해 해당 사용자 모든 토큰 무효화.
- 응답 200 + 갱신 row.
- audit `admin_update` (metadata에 변경 필드 목록).

#### `DELETE /api/admins/:id`

- `RequireGlobal`.
- 본인 삭제 시도 → 409 `CANNOT_DELETE_SELF`.
- 대상 soft-deleted/미존재 → 404.
- soft delete + `password_updated_at = now()` (토큰 무효화).
- 응답 204.
- audit `admin_delete`.

#### `POST /api/admins/:id/reset-password`

- `RequireGlobal`.
- 대상 soft-deleted/미존재 → 404.
- 본인 자신의 reset-password 호출은 허용(전역 관리자가 자기 비번을 잊은 경우 — 단 다른 전역 관리자가 호출해야 안전. MVP는 단일 전역 관리자라 사실상 본인이 다시 시드 도구로 리셋해야 함. 정책: **본인 reset-password는 409 `CANNOT_RESET_SELF`로 차단** — 안전망. 단, .env 시드의 1인 운영을 가정해 운영 가이드는 OPERATIONS.md 참조).
- `GenerateTempPassword(crypto/rand.Reader)` → 12자 평문.
- `HashPassword(plain)` → 해시.
- `WithTx`:
  - `ResetPassword(id, hash, now)` (must_change_password=true, temp_password_expires_at=now+24h, failed_login_count=0, locked_until=NULL, password_updated_at=now).
- 응답 200 + `{ "temp_password": "<plain>", "expires_at": "<KST +09:00 ISO8601>" }`.
- audit `password_reset` — **metadata에 temp_password 평문을 절대 넣지 마라**. metadata 예시: `{ "expires_at": "..." }`.

### 5. 라우트 등록

```go
adminProtected := r.Group("/api")
adminProtected.Use(middleware.RequireAuth(...), middleware.MustChangePasswordGuard())
{
    g := adminProtected.Group("", middleware.RequireGlobal())
    g.GET("/branches", branches.List)
    g.POST("/branches", branches.Create)
    g.PATCH("/branches/:id", branches.Update)
    g.DELETE("/branches/:id", branches.Delete)
    g.GET("/admins", admins.List)
    g.POST("/admins", admins.Create)
    g.PATCH("/admins/:id", admins.Update)
    g.DELETE("/admins/:id", admins.Delete)
    g.POST("/admins/:id/reset-password", admins.ResetPassword)
}
```

지점 관리자는 `RequireGlobal`에서 차단되어 위 라우트 모두 403.

### 6. 핸들러 테스트 — 정상 + 카탈로그 에러

각 라우트별:
- 정상
- 미인증/만료/약한 비번/잠금 등 step3에서 다룬 공통 케이스 + 이 step의 권한·도메인 케이스
- 다른 지점 자원 접근(이 step은 전역 전용이라 해당 없음 — step5에서 본격화)
- soft-deleted/미존재 → 404
- 중복(username/address) → 409
- 본인 self-modify(role/branch_id 변경 시도) → 409 `CANNOT_MODIFY_SELF_ROLE`
- 본인 self-delete → 409 `CANNOT_DELETE_SELF`
- 사용 중 지점 삭제 → 409 `BRANCH_IN_USE`
- reset-password 응답에 temp_password 평문 + expires_at 포함, 그 비번으로 즉시 로그인 가능 (login → must_change_password=true 확인 → password 변경 → 다음 로그인 정상)
- reset-password 24h 후(`FreezeTime`으로 시뮬) 같은 임시 비번으로 로그인 시 401 `TEMP_PASSWORD_EXPIRED`
- branch_id 변경 후 해당 사용자의 기존 access/refresh 모두 401 (`password_updated_at` 갱신으로)

audit 검증:
- 위 라우트 호출 후 `admin_audit_logs`에 정확히 한 row가 적재되는지(`action`/`target_type`/`target_id`/`metadata.request_id`).
- reset-password의 metadata에 평문 temp_password가 **포함되지 않는지**(검증 필수).

## 핵심 규칙 (반드시 박는다)

- **응답에 평문 비밀번호 노출 금지**: `POST /api/admins`(생성)는 평문 미노출(클라이언트 입력 비번이라도 응답에 echo 금지). `reset-password`만 1회 평문 응답.
- **audit metadata에 temp_password/password_hash 절대 미포함** — 평문/해시 모두.
- **CANNOT_MODIFY_SELF_ROLE / CANNOT_DELETE_SELF**: 자기 자신을 잠그지 못하게.
- **branch_id 변경 시 토큰 무효화**: `password_updated_at = now()` 한 번만 갱신해 access/refresh 동시 차단.
- **soft delete 일관성**: 모든 조회는 `deleted_at IS NULL` 필터. 미존재와 soft-deleted를 동일 404로 통일.
- **constraint 이름 의존**: `apperr.FromDBError`가 `admins_username_key`/`branches_address_key`로 분기하므로, 마이그레이션이 만든 정확한 이름(자동 생성 이름)을 확인 — 다른 이름이면 매핑이 빠진다(이 step에서 한 번 검증).
- **GET /api/branches는 인증 필수**: 키오스크 초기화용 공개 GET이 필요하면 별도 라우트로 step5에서 결정.
- **HSTS는 prod에서만**: dev에서 헤더 누락은 정상.

## Acceptance Criteria

```bash
set -a; source ../../.env; set +a
export TEST_DATABASE_URL="${TEST_DATABASE_URL:-$DATABASE_URL}"

cd backend
go vet ./...
go build ./...
go test -short -race ./...
go test -race -tags=integration ./...

# e2e 스모크: 시드 전역 관리자로 로그인 → admins POST → 새 지점 관리자 생성 → 그 계정 reset-password →
# 임시 비번으로 로그인 (must_change_password=true) → password 변경 → 정상 흐름.
# 자세한 시나리오는 통합 테스트가 커버. 여기선 빌드+테스트 통과만 확인.
```

ad-hoc 검증 자가 점검(반드시 통과):
- branches: 사용 중 지점 삭제 → 409 / 빈 지점 삭제 → 204 / 주소 중복 → 409 / 미존재 → 404
- admins: 본인 role 변경 → 409 / 본인 삭제 → 409 / 미존재 → 404 / username 중복 → 409 / 약한 비번 생성 → 400
- reset-password: 응답 body에 평문 12자 + KST `+09:00` expires_at / DB에 평문 미저장 / metadata에 평문 미포함 / 24h 후 `TEMP_PASSWORD_EXPIRED`
- branch_id 변경 후 그 사용자의 기존 access/refresh 모두 401

## 검증 절차

1. AC 명령 직접 실행.
2. `code-reviewer` 서브에이전트 호출. 입력: 단계 이름(`phase2-backend-scaffold/admins-branches`), `git diff HEAD --stat`. PASS 응답 필요.
3. step4 status 업데이트:
   - PASS → `"status": "completed"` + `"summary": "/api/branches CRUD(사용중 검사 BRANCH_IN_USE) + /api/admins CRUD + reset-password(12자 임시 비번·24h 만료) 추가; 본인 self-modify/self-delete 차단; branch_id 변경 시 토큰 무효화; audit admin_*·branch_*·password_reset 자동 기록(metadata에 평문 미포함)."`

## 금지사항

- `frontend/`·공유 파일 변경 금지.
- 응답에 평문 비번/해시/JWT/refresh 토큰 미포함(reset-password의 `temp_password` 1회만 예외).
- audit metadata에 평문 비번·해시 미포함.
- `password_hash`·`failed_login_count`·`locked_until`·`temp_password_expires_at`을 `PATCH /api/admins/:id` 화이트리스트에 절대 추가 금지.
- 본인 self-modify(role/branch_id) 또는 self-delete 우회 경로 만들지 마라.
- 미존재와 soft-deleted를 다른 코드로 구분 노출 금지(둘 다 404).
- ADR 외 라이브러리 추가 금지.
- step5의 `GET /api/members/search`나 키오스크 라우트는 여기서 만들지 마라.
- step3의 `password_updated_at` 통합 토큰 무효화 모델을 변경 금지(별도 컬럼 도입 시 마이그레이션 필요).
