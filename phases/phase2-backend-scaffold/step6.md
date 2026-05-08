---
agent: backend
depends_on: [members-kiosk]
summary: "memberships 라이프사이클(grant/pause/unpause/cancel-pause/refund) + GET memberships/:id(payments+events) + idempotency_keys 인프라(UUIDv4·24h·body hash·conflict) + EXCLUDE → MEMBERSHIP_PERIOD_OVERLAP 변환 + 환불 음수 결제 row 자동 추가."
---

# Step 6: 회원권 라이프사이클 (부여·정지·환불) + idempotency_keys 인프라

## 목표

회원권의 등록·정지·재개·취소·환불을 한 step에 마무리한다. 또한 `Idempotency-Key`를 사용하는 라우트들이 공유할 **idempotency_keys 인프라**를 여기서 한 번만 만들고, step7의 `bulk-extend`도 이를 재사용한다.

산출물:
- `internal/idempotency/idempotency.go` — UUIDv4 검증 + lookup/store 헬퍼 + body 다른 키 충돌 처리
- `internal/repo/memberships_repo.go` — 부여·정지·재개·취소·환불 + 단건 조회(events·payments 포함)
- `internal/repo/payments_repo.go` — 결제 row INSERT, 환불 음수 row INSERT
- `internal/repo/events_repo.go` — `membership_events` INSERT
- `internal/http/memberships.go` — `GET /api/memberships/:id`, `POST /api/members/:id/memberships`, `POST /api/memberships/:id/{pause,unpause,cancel-pause,refund}`
- `apperr.FromDBError`의 `23P01` → 409 `MEMBERSHIP_PERIOD_OVERLAP` 검증 통과

## 읽어야 할 파일

- `CLAUDE.md`, `backend/CLAUDE.md` (`### 회원권` 섹션 통째로)
- `db/CLAUDE.md` — memberships/membership_events/payments/idempotency_keys 컬럼·CHECK·EXCLUDE 제약(`memberships_no_period_overlap`)·상태 전환 정책
- `docs/API.md` — `/api/members/:id/memberships`, `/api/memberships/:id/*` 명세, 에러 코드(`INVALID_AMOUNT`, `INVALID_START_DATE`, `MEMBERSHIP_PERIOD_OVERLAP`, `PAUSE_ALREADY_USED`, `INVALID_PAUSE_RANGE`, `NOT_PAUSED`, `PAUSE_NOT_SCHEDULED`, `MEMBERSHIP_ALREADY_EXPIRED`, `INVALID_IDEMPOTENCY_KEY`, `IDEMPOTENCY_KEY_REQUIRED`, `IDEMPOTENCY_KEY_CONFLICT`)
- `docs/TESTING.md` — 회원권 테스트 카탈로그 (정상 + EXCLUDE + idempotency + 권한)
- step1·2·3·4·5 산출물 (특히 testutil의 `CreateMembership`, `WithTx`, `apperr.FromDBError`)

## 작업

### 1. `internal/idempotency/idempotency.go` — 공용 인프라

```go
type Result struct {
    Found  bool
    Status int
    Body   []byte   // jsonb로 저장된 응답
}

// 헤더가 UUIDv4 형식인지 검증. 빈 문자열이면 키없음 케이스를 caller가 분기.
func ValidateKey(s string) error    // 위반 시 apperr 400 INVALID_IDEMPOTENCY_KEY

// 같은 (admin_id, endpoint, request_hash)로 적재된 적 있는지 조회.
// hash 같으면 Found=true + 저장 응답 반환. hash 다르면 apperr 409 IDEMPOTENCY_KEY_CONFLICT.
// 24시간 지난 row는 같은 key더라도 무효(SELECT 시 created_at >= now() - interval '24 hours' 필터).
func Lookup(ctx context.Context, q Querier, key, endpoint string, adminID int64, requestHash string) (Result, error)

// 응답 적재. 트랜잭션이 commit된 후에 호출하는 것이 안전(부수효과 보존).
func Store(ctx context.Context, q Querier, key, endpoint string, adminID int64, requestHash string, status int, body []byte) error

// helper: request body의 JSON canonical form을 SHA-256으로 해시.
// 같은 의미의 body가 키 비교에서 다르게 보이지 않도록 키 정렬·whitespace 제거.
func HashRequest(body []byte) (string, error)
```

핸들러 사용 패턴:

```go
key := c.GetHeader("Idempotency-Key")
if key == "" { return apperr.New(400, "IDEMPOTENCY_KEY_REQUIRED", "...") }
if err := idempotency.ValidateKey(key); err != nil { return err }

bodyBytes, _ := io.ReadAll(c.Request.Body)
hash, _ := idempotency.HashRequest(bodyBytes)

// 1) 조회
res, err := idempotency.Lookup(ctx, pool, key, endpoint, adminID, hash)
if err != nil { return err }
if res.Found {
    c.Data(res.Status, "application/json", res.Body); return nil
}

// 2) 실제 작업 (WithTx)
respStatus, respBody := doWork(...)

// 3) 적재 (실패해도 응답은 정상 반환 — slog만)
_ = idempotency.Store(ctx, pool, key, endpoint, adminID, hash, respStatus, respBody)

c.Data(respStatus, "application/json", respBody)
```

단위 테스트:
- ValidateKey: 정상 UUIDv4, 빈 문자열, 임의 string, UUIDv1, 길이 다름
- HashRequest: 같은 의미 다른 표기(`{"a":1,"b":2}` vs `{"b":2,"a":1}`)가 같은 hash
- Lookup: 처음 호출 Found=false, Store 후 같은 키·같은 hash → Found=true + 저장 응답, 같은 키·다른 hash → 409
- 24시간 지난 row는 같은 key라도 처음 호출처럼 처리(테스트는 시계 주입)

### 2. `internal/repo/payments_repo.go`

```go
type PaymentRow struct {
    ID            int64
    MembershipID  int64
    BranchID      int64
    Amount        int      // 양수=부여, 음수=환불
    Method        string   // "cash" | "card"
    PaidAt        time.Time  // date — KST 오늘로 핸들러가 채워서 전달
    Memo          *string
    PerformedBy   int64
    CreatedAt     time.Time
}
func InsertPayment(ctx, q Querier, row PaymentRow) (int64, error)
// CHECK violation(amount=0)는 23514 → apperr 400 INVALID_INPUT.

// 환불 시 원본 결제 row 조회 (오래된 부여 결제 1건).
func GetOriginalGrantPayment(ctx, q Querier, membershipID int64) (*PaymentRow, error)
// WHERE membership_id=? AND amount>0 ORDER BY paid_at ASC, id ASC LIMIT 1.

// membership_id로 결제 이력 전체(부여+환불) 조회. created_at ASC 정렬.
func ListPaymentsByMembership(ctx, q Querier, membershipID int64) ([]PaymentRow, error)
```

### 3. `internal/repo/events_repo.go`

```go
type EventRow struct {
    ID                int64
    MembershipID      int64
    Action            string  // "pause" | "unpause" | "cancel_pause" | "refund" | "bulk_extend"
    PauseStartDate    *time.Time
    PauseEndDate      *time.Time
    ActualPauseEnd    *time.Time
    ExtendDays        *int
    Reason            string
    PerformedBy       int64
    CreatedAt         time.Time
}
func InsertEvent(ctx, q Querier, row EventRow) error
func ListEventsByMembership(ctx, q Querier, membershipID int64) ([]EventRow, error)
```

### 4. `internal/repo/memberships_repo.go`

```go
type MembershipRow struct {
    ID              int64
    MemberID        int64
    Type            string  // "monthly" | "pass10"
    Months          *int
    StartDate       time.Time
    EndDate         time.Time
    Remaining       *int
    Status          string  // "active" | "paused" | "refunded" | "expired"
    PauseStartDate  *time.Time
    PauseEndDate    *time.Time
    PauseUsed       bool
    CreatedAt       time.Time
    UpdatedAt       time.Time
}

// member의 branch_id로 scope 검증 강제. caller가 ScopeBranchID 전달.
func GetMembership(ctx, q Querier, id int64, scopeBranchID *int64) (*MembershipRow, error)

// 단건 + 결제 이력 + events. 핸들러 GET /api/memberships/:id에서 사용.
type MembershipDetail struct {
    Membership MembershipRow
    Payments   []PaymentRow
    Events     []EventRow
}
func GetMembershipDetail(ctx, q Querier, id int64, scopeBranchID *int64) (*MembershipDetail, error)

// 회원의 active/paused 회원권 중 가장 최신 1건. POST 부여 폼의 prefill용.
func GetCurrentMembership(ctx, q Querier, memberID int64) (*MembershipRow, error)

// 부여 INSERT (트랜잭션 내). 23P01 → caller가 apperr.FromDBError로 변환.
type GrantInput struct {
    MemberID  int64
    Type      string   // monthly|pass10
    Months    *int     // monthly일 때 설정
    Remaining *int     // pass10이면 10
    StartDate time.Time
    EndDate   time.Time
}
func InsertMembership(ctx, tx pgx.Tx, in GrantInput) (int64, error)

// 정지 등록.
type PauseInput struct {
    ID            int64
    PauseStartDate time.Time
    PauseEndDate   time.Time
    Today          time.Time
}
// 트랜잭션:
//   1) status='active' AND pause_used=false 검증, 아니면 caller가 apperr.
//   2) end_date += (pause_end - pause_start)
//   3) pause_start_date/pause_end_date 세팅, pause_used=true
//   4) pause_start_date <= today면 status='paused'
func ApplyPause(ctx, tx pgx.Tx, in PauseInput) error

// 조기 활성화. status='paused' 한정.
type UnpauseInput struct { ID int64; ActualPauseEnd time.Time }
// 트랜잭션:
//   1) status='paused' 검증, 아니면 caller가 NOT_PAUSED.
//   2) remaining_days = pause_end_date - actual_pause_end
//   3) end_date -= remaining_days, pause_*=NULL, status='active'
func ApplyUnpause(ctx, tx pgx.Tx, in UnpauseInput) error

// 미래 예약 정지 취소. status='active' AND pause_used=true AND pause_start_date>today 한정.
type CancelPauseInput struct { ID int64; Today time.Time }
func ApplyCancelPause(ctx, tx pgx.Tx, in CancelPauseInput) error
//   end_date -= (pause_end - pause_start), pause_*=NULL, pause_used=false

// 환불. 호출 가능 status: active / paused / active+미래시작.
type RefundInput struct { ID int64 }
// 트랜잭션:
//   1) status='expired' → caller가 409 MEMBERSHIP_ALREADY_EXPIRED.
//   2) status='refunded' → 멱등(idempotency_keys가 1차 방어, 안전망)
//   3) UPDATE status='refunded'
func ApplyRefund(ctx, tx pgx.Tx, in RefundInput) error
```

`*UPDATE end_date*`로 인한 EXCLUDE(`23P01`) 위반은 핸들러가 catch해 409 `MEMBERSHIP_PERIOD_OVERLAP`로 변환.

### 5. 핸들러: 6개 라우트

#### `GET /api/memberships/:id`

- 인증 + must_change_password 가드 통과.
- 지점 관리자: 회원권의 member_id → member.branch_id 검증. 다른 지점 → 404.
- 응답: `{ membership: {...}, payments: [...], events: [...] }`. payments/events는 ASC 정렬.

#### `POST /api/members/:id/memberships` — 회원권 부여

- 지점 스코프 검증 (member의 branch_id 일치).
- 회원이 soft-deleted/미존재 → 404.
- header: `Idempotency-Key` (UUIDv4) 필수. 누락 → 400 `IDEMPOTENCY_KEY_REQUIRED`. 형식 위반 → 400 `INVALID_IDEMPOTENCY_KEY`.
- body:
  - `type`: "monthly" | "pass10". 그 외 → 400.
  - `months`: monthly일 때 1 이상의 정수. 누락/0/음수 → 400.
  - `start_date`: KST today 또는 미래(YYYY-MM-DD). 과거 → 400 `INVALID_START_DATE`.
  - `amount`: 양수 정수(원). `<= 0` → 400 `INVALID_AMOUNT`.
  - `method`: "cash" | "card". 그 외 → 400.
- 서버 자동:
  - `end_date`: monthly = `start_date + months * 1month`(달력 기준 — `start_date::date + (months || ' months')::interval`). pass10 = `start_date + 2 month`.
  - `remaining`: pass10이면 10, monthly면 NULL.
  - `payments.paid_at`: KST today.
  - `payments.branch_id`: member의 branch_id (클라가 보내도 무시).
  - `payments.performed_by`: 호출 admin.
- idempotency: `endpoint = "POST /api/members/:id/memberships"`, body hash 비교.
- `WithTx`: `InsertMembership` → `InsertPayment` → 응답.
- EXCLUDE 위반(`23P01`) → 409 `MEMBERSHIP_PERIOD_OVERLAP`.
- 응답 201 + `{ membership: {...}, payment: {...} }`.

#### `POST /api/memberships/:id/pause`

- 지점 스코프.
- body: `{ start_date, end_date, reason }`. 빈 reason → 400.
- 검증:
  - `pause_used == true` → 409 `PAUSE_ALREADY_USED`.
  - `start_date > end_date` → 400 `INVALID_PAUSE_RANGE`.
  - `start_date < today` → 400 `INVALID_PAUSE_RANGE`.
  - `start_date < memberships.start_date` → 400 `INVALID_PAUSE_RANGE`. (미래 시작 회원권의 시작 전 정지 차단)
  - `end_date > memberships.end_date` → 400 `INVALID_PAUSE_RANGE`.
- `WithTx`: `ApplyPause` → `InsertEvent(action='pause', pause_start, pause_end, reason, performed_by)`.
- EXCLUDE 위반 → 409 `MEMBERSHIP_PERIOD_OVERLAP` (end_date 연장 결과가 미래 회원권과 겹침).
- 응답 200 + 갱신 `MembershipRow`.

#### `POST /api/memberships/:id/unpause`

- 지점 스코프.
- body: `{ reason }`.
- `status != 'paused'` → 409 `NOT_PAUSED`.
- `WithTx`: `ApplyUnpause(today)` → `InsertEvent(action='unpause', actual_pause_end=today, reason)`.
- 응답 200 + 갱신 row.

#### `POST /api/memberships/:id/cancel-pause`

- 지점 스코프.
- body: `{ reason }`.
- `status != 'active' || !pause_used || pause_start_date <= today` → 409 `PAUSE_NOT_SCHEDULED`.
- `WithTx`: `ApplyCancelPause(today)` → `InsertEvent(action='cancel_pause', reason)`.
- 응답 200 + 갱신 row.

#### `POST /api/memberships/:id/refund`

- 지점 스코프.
- header: `Idempotency-Key` 필수.
- body: `{ reason }` (다른 필드 무시 — 서버가 자동).
- 호출 가능 status: active / paused / active+미래시작. expired → 409 `MEMBERSHIP_ALREADY_EXPIRED`. refunded → 409 (idempotency가 1차 방어).
- `WithTx`:
  1. `ApplyRefund(id)` (UPDATE status='refunded')
  2. `GetOriginalGrantPayment` → 음수 결제 row INSERT (`paid_at=KST today`, `branch_id=원본`, `method=원본`, `amount=-원본`, `performed_by=호출자`, `memo=NULL`).
  3. `InsertEvent(action='refund', reason, performed_by)`.
- 응답 200 + `{ membership: {...}, refund_payment: {...} }`.

### 6. 라우트 등록

```go
protected := r.Group("/api")
protected.Use(middleware.RequireAuth(...), middleware.MustChangePasswordGuard())
{
    // 부여
    protected.POST("/members/:id/memberships", memberships.Grant)

    // 회원권 단건 + 라이프사이클
    protected.GET("/memberships/:id", memberships.Get)
    protected.POST("/memberships/:id/pause", memberships.Pause)
    protected.POST("/memberships/:id/unpause", memberships.Unpause)
    protected.POST("/memberships/:id/cancel-pause", memberships.CancelPause)
    protected.POST("/memberships/:id/refund", memberships.Refund)
}
```

지점 관리자 가드는 핸들러 내부에서 member의 branch_id를 검증해 다른 지점은 404.

### 7. 핸들러 테스트 — 정상 + 카탈로그 에러

각 라우트별:
- **부여 정상** (monthly+months / pass10) — end_date 자동 계산 + payments row 생성
- 부여 — `Idempotency-Key` 누락 400, 형식 위반 400, 같은 키·같은 body 두 번 → 회원권 1개·결제 1 row, 같은 키·다른 body → 409
- 부여 — start_date 어제 → 400 `INVALID_START_DATE`
- 부여 — amount=0/-1 → 400 `INVALID_AMOUNT`
- 부여 — type 외 값 → 400, monthly+months 누락 → 400
- 부여 — 다른 지점 회원 → 404 (지점 관리자), soft-deleted 회원 → 404
- 부여 — 기존 active+overlap → 409 `MEMBERSHIP_PERIOD_OVERLAP`, **겹치지 않는 미래 등록 통과**
- 부여 — paid_at은 항상 KST today (클라가 다른 값 보내도 무시), branch_id는 회원의 branch_id로 자동
- pause — 정상(즉시 paused / 미래 active 유지), pause_used=true → 409, 범위 위반 400, EXCLUDE → 409
- unpause — 정상(end_date 단축), status=active에서 호출 → 409 `NOT_PAUSED`
- cancel-pause — 정상(end_date 복원, pause_used=false), 도래한 정지에서 호출 → 409 `PAUSE_NOT_SCHEDULED`
- refund — 정상(active/paused/active+미래시작), expired → 409, refunded 재호출 멱등(같은 키)
- refund — payments 음수 row 추가, 매출 합계 자동 보정(step7에서 검증)
- 모든 응답의 `paid_at`/`created_at` timestamp가 `+09:00` 오프셋
- `apperr.FromDBError`가 `23P01` → `MEMBERSHIP_PERIOD_OVERLAP`로 매핑(unit + 통합 둘 다)

## 핵심 규칙 (반드시 박는다)

- **단일 트랜잭션**: 부여(membership+payment), 환불(membership+payment+event), pause/unpause/cancel-pause(membership+event)는 모두 한 트랜잭션. WithTx로 retry.
- **`paid_at`/`branch_id` 서버 자동**: 클라 입력 무시 — 핸들러가 결정.
- **회원권 부여 amount > 0**: 0/음수는 400. 무료 결제 미지원.
- **start_date >= today**: 과거 부여 불가.
- **기간 겹침**: EXCLUDE 제약이 강제. 핸들러는 `apperr.FromDBError`만 거치면 자동 변환. 추가 잠금 불필요.
- **pause는 회원권당 1회**: `pause_used=true`면 409. 단, cancel-pause로 미래 예약을 취소하면 다시 false로 돌아가 재등록 가능.
- **idempotency 24h 만료**: 24시간 지난 키는 새 키처럼 처리.
- **idempotency 응답 적재 실패가 핵심 흐름을 막지 않음**: `Store` 실패는 slog만, 응답은 정상 반환. 다음 같은 키 호출이 실제 작업을 한 번 더 할 수도 있는 트레이드오프 — 받아들임.
- **다른 지점 자원 → 404**: 회원/회원권 모두.
- **soft-deleted 회원/회원권 → 404**: 회원권은 `deleted_at` 컬럼 없음 → status가 refunded/expired인 회원권에 대한 접근은 GET은 허용하지만 부여/pause는 거부 분기.
- **환불 row 자동 채움**: `paid_at`/`method`/`amount`/`branch_id`는 서버가 원본 결제 row에서 가져온다. 클라는 `reason`만 보낸다.
- **MVP는 전체 환불만**: 부분 환불 미지원. 환불 row의 amount는 원본 amount의 부호 반전.

## Acceptance Criteria

```bash
set -a; source ../../.env; set +a
export TEST_DATABASE_URL="${TEST_DATABASE_URL:-$DATABASE_URL}"

cd backend
go vet ./...
go build ./...
go test -short -race ./...
go test -race -tags=integration ./...
```

자가 점검 시나리오(통합 테스트로 커버):
- 부여 → 같은 키 두 번 → membership 1개·payment 1 row
- 부여 → 같은 키·다른 body → 409 IDEMPOTENCY_KEY_CONFLICT
- 정지 → end_date 연장 + 즉시/예약 분기
- unpause → end_date 단축 (4/1~4/7 정지 + 5/30 만료에서 4/6 unpause → 5/29)
- cancel-pause → end_date 복원 + pause_used=false
- 환불 → status=refunded + 음수 결제 row + sales 집계 검증(step7)
- expired 환불 시도 → 409
- pause 등록으로 end_date가 미래 회원권과 겹치면 409 (롤백 — 미래 회원권은 그대로)

## 작업 마감 절차 (B 방안 — 책임 분리)

1. AC 명령 직접 실행해 빌드/테스트 통과 확인.
2. 변경된 코드를 conventional commit으로 worktree(`feat/phase2-backend-scaffold-be`)에 commit. **`phases/`는 절대 만지지 마라** — hook이 차단한다.
3. status·summary·timestamp는 박지 마라. execute.py가 acceptance(go vet/build/test) + code-reviewer gate 통과 시 main 인덱스에 frontmatter `summary`를 직접 박는다.
4. 사용자 개입이 필요한 상황(ADR 갱신, 도구 미설치 등)이면 commit하지 말고 stdout에 사유를 쓰고 종료. execute.py가 retry/error/blocked로 판정한다.

## 금지사항

- `frontend/`·공유 파일 변경 금지.
- 부여/환불에서 `paid_at`/`branch_id`를 클라 입력값 그대로 사용 금지(서버 자동).
- 부여 amount=0 허용 금지.
- start_date 과거 허용 금지.
- 같은 회원에 active/paused 기간이 겹치는 회원권 강제 적재 금지(EXCLUDE를 비활성화하지 마라).
- pause를 `pause_used=true`인 회원권에 두 번째로 등록 허용 금지(cancel-pause로 false 복원 후만 가능).
- 환불 row의 `amount`/`method`/`branch_id`/`paid_at`을 클라 입력으로 채우지 마라.
- Idempotency-Key 헤더 누락 시 정상 처리 금지(부여·환불 모두 400).
- ADR 외 라이브러리 추가 금지.
- step7의 체크인/매출/bulk-extend는 여기서 만들지 마라.
- 부분 환불 구현 금지(MVP 범위 밖).
