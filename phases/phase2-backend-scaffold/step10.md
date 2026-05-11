---
agent: backend
depends_on: [memberships]
summary: "step6 회귀 보강 — internal/http/memberships.go 신설(grant/get-detail/pause/unpause/cancel-pause/refund 6개 핸들러) + cmd/server/main.go 라우트 등록 + e2e 테스트(internal/http/memberships_test.go). repo 레이어(memberships/payments/events/idempotency)는 step6에 이미 존재."
---

# Step 10: 회원권 HTTP 핸들러 (step 6 회귀 보강)

## 배경 — 왜 이 step이 필요한가

step6 자식 Claude는 산출물 7개 중 SQL repo 6개(`internal/idempotency/idempotency.go`, `internal/repo/memberships_repo.go`, `internal/repo/payments_repo.go`, `internal/repo/events_repo.go`, `internal/repo/idempotency_repo.go` + 테스트)는 만들었지만 **`internal/http/memberships.go` 핸들러 파일을 통째로 누락**했다. step6 commit `f551b46`의 `git show --stat`로 직접 확인됨. step6 reviewer/acceptance는 SQL 레이어 테스트만으로 PASS 받아 회귀가 step9까지 묻혀 있었다.

step9 자식 Claude가 ROADMAP 매핑 도중 발견:

> `internal/http/memberships.go` 파일이 존재하지 않음. ... 라우터에 6개 라우트가 미등록.
> - `POST /api/members/:id/memberships` (회원권 부여)
> - `GET  /api/memberships/:id` (회원권 상세)
> - `POST /api/memberships/:id/pause`
> - `POST /api/memberships/:id/unpause`
> - `POST /api/memberships/:id/cancel-pause`
> - `POST /api/memberships/:id/refund`

이 step10에서 정확히 그 6개를 보강한다. **repo 레이어는 추가/수정하지 않는다**(이미 정상). 핸들러·라우트·테스트만.

## 산출물

- `backend/internal/http/memberships.go` — 6개 핸들러
- `backend/internal/http/memberships_test.go` — 정상 + 카탈로그 에러 e2e 테스트
- `backend/cmd/server/main.go` — 라우트 6개 등록(이미 등록된 `bulk-extend`와 같은 그룹/미들웨어 체인)

이미 존재하는 것(건드리지 마라):
- `backend/internal/repo/memberships_repo.go` (`InsertMembership`, `GetMembership`, `GetMembershipDetail`, `ApplyPause/Unpause/CancelPause/Refund` 등)
- `backend/internal/repo/payments_repo.go` (`InsertPayment`, `GetOriginalGrantPayment`, `ListPaymentsByMembership`)
- `backend/internal/repo/events_repo.go` (`InsertEvent`, `ListEventsByMembership`)
- `backend/internal/repo/idempotency_repo.go` + `backend/internal/idempotency/idempotency.go` (`ValidateKey`, `Lookup`, `Store`, `HashRequest`)

## 읽어야 할 파일

- `CLAUDE.md`, `backend/CLAUDE.md` (`### 회원권` 섹션 통째로 + `### 인증·세션`의 Idempotency-Key 형식 검증)
- `db/CLAUDE.md` — memberships/membership_events/payments/idempotency_keys 컬럼·CHECK·EXCLUDE(`memberships_no_period_overlap`)·상태 전환 정책
- `docs/API.md` — `/api/members/:id/memberships`, `/api/memberships/:id`, `/api/memberships/:id/{pause,unpause,cancel-pause,refund}` 명세
- `docs/TESTING.md` — 회원권 테스트 카탈로그
- `phases/phase2-backend-scaffold/step6.md` — 원래 step6의 작업 5·6·7번 섹션(이 step의 사실상 명세)
- 이미 만들어진 repo·idempotency 코드(시그니처와 트랜잭션 경계 파악):
  - `backend/internal/repo/memberships_repo.go`
  - `backend/internal/repo/payments_repo.go`
  - `backend/internal/repo/events_repo.go`
  - `backend/internal/idempotency/idempotency.go`
  - `backend/internal/repo/tx.go` (`WithTx`)
- 다른 핸들러의 예시(에러 변환·미들웨어 사용·테스트 작성 스타일):
  - `backend/internal/http/bulk_extend.go` + `backend/internal/http/bulk_extend_test.go` — Idempotency-Key 사용 패턴, EXCLUDE→409 변환
  - `backend/internal/http/members.go` + `backend/internal/http/members_test.go` — branch scope 검증, 404 통일
- `backend/internal/apperr/apperr.go` — 에러 코드 enum, `FromDBError`(`23P01` → 409 `MEMBERSHIP_PERIOD_OVERLAP` 매핑 이미 존재)

## 작업

### 1. `internal/http/memberships.go` — 6개 핸들러

#### 공통 사항

- 인증 미들웨어(`RequireAuth` + `MustChangePasswordGuard`)는 라우트 등록 시점에 적용. 핸들러는 context에서 `adminID`, `role`, `branchID`(branch 관리자만)를 꺼내 사용.
- **지점 스코프 검증**: 핸들러 진입 직후 대상 회원 또는 회원권의 member.branch_id와 호출자의 branchID(role='branch'일 때)를 비교 → 다른 지점은 **404로 통일**(403 아님). 전역 관리자(role='global')는 검증 생략.
- **soft-deleted 회원**: `members.deleted_at IS NOT NULL`이면 부여·GET 모두 404.
- **트랜잭션 단일화**: 부여(membership+payment), 환불(membership+payment+event), pause/unpause/cancel-pause(membership+event)는 모두 `repo.WithTx`로 한 트랜잭션. retry는 `WithTx`가 처리.
- **EXCLUDE 위반(`23P01`) 변환**: 트랜잭션 내 INSERT/UPDATE에서 `apperr.FromDBError`를 거치면 자동으로 409 `MEMBERSHIP_PERIOD_OVERLAP`로 변환된다(이미 step6에서 매핑됨). 핸들러는 그 에러를 그대로 응답.
- **응답 timestamp**: 모든 `time.Time`은 `Asia/Seoul` Location으로 변환 후 직렬화. 헬퍼는 `internal/http/render.go`에 이미 존재할 것 — 없으면 한 줄 헬퍼 추가.
- **에러 응답**: `apperr.AppError`는 미들웨어/render에 의해 `{"error":{"code":"...","message":"..."}}`로 직렬화.

핸들러 시그니처(예시; 정확한 receiver/struct 패턴은 다른 핸들러와 일치시킬 것):

```go
type MembershipsHandler struct {
    Pool      *pgxpool.Pool
    Repo      *repo.MembershipsRepo   // 또는 repo 패키지 함수 직접 호출
    Payments  *repo.PaymentsRepo
    Events    *repo.EventsRepo
    Members   *repo.MembersRepo
    Idem      idempotency.Store        // 또는 함수 그룹
    Clock     util.Clock               // KST today 계산용
}

func (h *MembershipsHandler) Get(c *gin.Context)         // GET    /api/memberships/:id
func (h *MembershipsHandler) Grant(c *gin.Context)       // POST   /api/members/:id/memberships
func (h *MembershipsHandler) Pause(c *gin.Context)       // POST   /api/memberships/:id/pause
func (h *MembershipsHandler) Unpause(c *gin.Context)     // POST   /api/memberships/:id/unpause
func (h *MembershipsHandler) CancelPause(c *gin.Context) // POST   /api/memberships/:id/cancel-pause
func (h *MembershipsHandler) Refund(c *gin.Context)      // POST   /api/memberships/:id/refund
```

기존 `bulk_extend.go`나 `members.go`가 어떤 스타일(receiver, free function, repo wiring)을 쓰는지에 맞춰라 — 일관성 우선. 새로운 패턴 도입 금지.

#### 1-a. `GET /api/memberships/:id` — 회원권 단건

- 지점 스코프 검증.
- repo: `GetMembershipDetail(ctx, q, id, scopeBranchID)` 호출 → `{ Membership, Payments[], Events[] }` 반환.
  - 존재하지 않거나 다른 지점 → 404 `NOT_FOUND`.
- 응답 200 + `{ "membership": {...}, "payments": [...], "events": [...] }`. payments/events는 `created_at ASC` 정렬(repo가 보장).

#### 1-b. `POST /api/members/:id/memberships` — 회원권 부여

- URL `:id` = `member_id`.
- 지점 스코프 검증(member.branch_id), soft-deleted member → 404.
- header `Idempotency-Key`:
  - 누락 → 400 `IDEMPOTENCY_KEY_REQUIRED`.
  - `idempotency.ValidateKey` 위반 → 400 `INVALID_IDEMPOTENCY_KEY`.
- body 검증:
  - `type` ∈ {"monthly","pass10"}, 그 외 400 `INVALID_INPUT`.
  - `months`: monthly일 때 정수 ≥ 1, 누락/0/음수 → 400 `INVALID_INPUT`. pass10일 때는 무시(NULL로 저장).
  - `start_date`: `YYYY-MM-DD` 파싱. KST today 미만 → 400 `INVALID_START_DATE`.
  - `amount`: 정수 ≥ 1. 0/음수/누락 → 400 `INVALID_AMOUNT`.
  - `method` ∈ {"cash","card"}, 그 외 400 `INVALID_INPUT`.
- 서버 자동 채움(클라 입력 무시):
  - `end_date`: monthly = `start_date + months month`(달력 기준 — `start_date + (months || ' months')::interval`). pass10 = `start_date + 2 month`.
  - `remaining`: pass10이면 10, monthly면 NULL.
  - `payments.paid_at`: KST today (`h.Clock.Now().In(KST).Truncate(24h)` 또는 `(now() AT TIME ZONE 'Asia/Seoul')::date`). **클라가 보낸 paid_at은 무시한다**.
  - `payments.branch_id`: member의 branch_id. **클라가 보낸 branch_id는 무시한다**.
  - `payments.performed_by`: 호출 admin.
- idempotency 패턴(`bulk_extend.go`와 동일):
  1. body 읽고 `HashRequest`로 hash 생성 (단, `c.Request.Body`를 두 번 읽기 위해 `io.ReadAll` + `c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))` 패턴 사용).
  2. `idempotency.Lookup(ctx, pool, key, "POST /api/members/:id/memberships", adminID, hash)`.
     - `Found=true` → 저장 응답 그대로 반환(`c.Data(res.Status, "application/json", res.Body)`).
     - hash 다른 키 → 409 `IDEMPOTENCY_KEY_CONFLICT`(Lookup이 apperr 반환).
  3. `WithTx`:
     - `InsertMembership(ctx, tx, GrantInput{...})` → `id, err`. err가 `23P01`이면 `apperr.FromDBError`가 409 `MEMBERSHIP_PERIOD_OVERLAP`로 변환.
     - `InsertPayment(ctx, tx, PaymentRow{MembershipID: id, BranchID: ..., Amount: amount, Method: method, PaidAt: kstToday, PerformedBy: adminID})`.
  4. 응답 body 직렬화 → `idempotency.Store(ctx, pool, key, endpoint, adminID, hash, 201, body)` (실패는 slog만, 응답은 그대로 반환).
  5. 응답 201 + `{ "membership": {...}, "payment": {...} }`.

#### 1-c. `POST /api/memberships/:id/pause`

- 지점 스코프(membership → member.branch_id 검증).
- body: `{ "start_date": "YYYY-MM-DD", "end_date": "YYYY-MM-DD", "reason": "..." }`. 빈 reason → 400 `INVALID_INPUT`.
- repo: `GetMembership(ctx, pool, id, scope)` → membership row.
  - 존재하지 않거나 다른 지점 → 404.
- 검증(이 순서로):
  - `pause_used == true` → 409 `PAUSE_ALREADY_USED`.
  - `start_date > end_date` → 400 `INVALID_PAUSE_RANGE`.
  - `start_date < KST today` → 400 `INVALID_PAUSE_RANGE`.
  - `start_date < memberships.start_date` → 400 `INVALID_PAUSE_RANGE`.
  - `end_date > memberships.end_date` → 400 `INVALID_PAUSE_RANGE`.
- `WithTx`:
  - `ApplyPause(ctx, tx, PauseInput{ID, PauseStartDate, PauseEndDate, Today: kstToday})`.
    - 내부에서 `end_date += (pause_end - pause_start)`, `pause_start_date/pause_end_date` 세팅, `pause_used=true`. 도달 분기(`pause_start_date <= today` → status='paused')도 repo가 처리.
    - EXCLUDE 위반(연장 결과가 미래 회원권과 겹침) → `apperr.FromDBError`가 409 `MEMBERSHIP_PERIOD_OVERLAP`.
  - `InsertEvent(ctx, tx, EventRow{MembershipID, Action: "pause", PauseStartDate: &start, PauseEndDate: &end, Reason: reason, PerformedBy: adminID})`.
- 응답 200 + 갱신된 membership row(다시 `GetMembership` 또는 `ApplyPause`가 반환).

#### 1-d. `POST /api/memberships/:id/unpause`

- 지점 스코프.
- body: `{ "reason": "..." }`. 빈 reason → 400.
- `GetMembership` → `status != 'paused'`이면 409 `NOT_PAUSED`.
- `WithTx`:
  - `ApplyUnpause(ctx, tx, UnpauseInput{ID, ActualPauseEnd: kstToday})`.
    - `remaining = pause_end_date - actual_pause_end`(양수일 때만), `end_date -= remaining`, `pause_*=NULL`, `status='active'`.
  - `InsertEvent(action="unpause", actual_pause_end=&kstToday, reason, performed_by)`.
- 응답 200 + 갱신 row.

#### 1-e. `POST /api/memberships/:id/cancel-pause`

- 지점 스코프.
- body: `{ "reason": "..." }`. 빈 reason → 400.
- `GetMembership` → `status != 'active' || !pause_used || pause_start_date <= kstToday` → 409 `PAUSE_NOT_SCHEDULED`.
- `WithTx`:
  - `ApplyCancelPause(ctx, tx, CancelPauseInput{ID, Today: kstToday})`.
    - `end_date -= (pause_end - pause_start)`, `pause_*=NULL`, `pause_used=false`.
  - `InsertEvent(action="cancel_pause", reason, performed_by)`.
- 응답 200 + 갱신 row.

#### 1-f. `POST /api/memberships/:id/refund`

- 지점 스코프.
- header `Idempotency-Key` 필수(누락 400 `IDEMPOTENCY_KEY_REQUIRED`, 형식 위반 400 `INVALID_IDEMPOTENCY_KEY`).
- body: `{ "reason": "..." }`. **다른 필드는 무시**. 빈 reason → 400.
- `GetMembership` → 호출 가능 status 검증:
  - `expired` → 409 `MEMBERSHIP_ALREADY_EXPIRED`.
  - `refunded` → 409(같은 idempotency key 재호출이면 1차 방어가 위에서 막음 — 키가 다르면 여기서 막힘. 안전망).
  - `active` / `paused` / `active+미래시작` → 통과.
- idempotency 패턴(grant와 동일).
- `WithTx`:
  1. `GetOriginalGrantPayment(ctx, tx, id)` → `PaymentRow{Amount, Method, BranchID, ...}` (양수 결제, 가장 오래된 row).
  2. `ApplyRefund(ctx, tx, RefundInput{ID})` → `UPDATE status='refunded'`.
  3. `InsertPayment(ctx, tx, PaymentRow{MembershipID, BranchID: orig.BranchID, Amount: -orig.Amount, Method: orig.Method, PaidAt: kstToday, PerformedBy: adminID, Memo: nil})`.
  4. `InsertEvent(action="refund", reason, performed_by)`.
- 응답 200 + `{ "membership": {...}, "refund_payment": {...} }`.

### 2. `cmd/server/main.go` — 라우트 등록

이미 등록된 `apiGlobal.POST("/memberships/bulk-extend", ...)` 옆/위에 다음을 추가. 정확한 그룹 변수명·미들웨어 chain은 기존 코드를 따라 일관되게.

```go
// 인증 + 비번 변경 가드가 적용된 그룹 (예: apiAuth)
apiAuth.POST("/members/:id/memberships",        membershipsHandler.Grant)
apiAuth.GET ("/memberships/:id",                membershipsHandler.Get)
apiAuth.POST("/memberships/:id/pause",          membershipsHandler.Pause)
apiAuth.POST("/memberships/:id/unpause",        membershipsHandler.Unpause)
apiAuth.POST("/memberships/:id/cancel-pause",   membershipsHandler.CancelPause)
apiAuth.POST("/memberships/:id/refund",         membershipsHandler.Refund)
```

핸들러 wiring(`MembershipsHandler` 인스턴스화 + 의존성 주입)도 같은 파일에서 다른 핸들러와 같은 패턴으로.

### 3. `internal/http/memberships_test.go` — e2e 테스트

`testutil`(이미 존재: `SetupDB`, `TruncateAll`, `CreateAdmin/Branch/Member/Membership`, `Login`, `AuthRequest`, `FreezeTime`)를 그대로 사용. `bulk_extend_test.go` / `members_test.go`의 패턴 일관 유지.

각 라우트별 시나리오 — **카탈로그 모두 커버**(이건 step9 PHASE2_AC.md의 매핑 대상이 된다):

#### Grant
- ✅ monthly + months=3 → membership 1개 + payment 1 row, end_date = start_date + 3 month
- ✅ pass10 → remaining=10, end_date = start_date + 2 month
- ✅ 같은 Idempotency-Key + 같은 body 두 번 → membership 1개·payment 1 row(두 번째도 201, body 동일)
- ✅ 같은 Idempotency-Key + 다른 body → 409 `IDEMPOTENCY_KEY_CONFLICT`
- ✅ Idempotency-Key 누락 → 400 `IDEMPOTENCY_KEY_REQUIRED`
- ✅ Idempotency-Key가 UUIDv4 아님(임의 string) → 400 `INVALID_IDEMPOTENCY_KEY`
- ✅ start_date = 어제 → 400 `INVALID_START_DATE`
- ✅ amount = 0 / -1 → 400 `INVALID_AMOUNT`
- ✅ type 외 값 → 400 `INVALID_INPUT`, monthly + months 누락/0 → 400 `INVALID_INPUT`
- ✅ method 외 값 → 400 `INVALID_INPUT`
- ✅ 지점 관리자가 다른 지점 회원에 부여 → 404 `NOT_FOUND`
- ✅ soft-deleted 회원에 부여 → 404 `NOT_FOUND`
- ✅ 기존 active 회원권과 기간 겹침 → 409 `MEMBERSHIP_PERIOD_OVERLAP`
- ✅ **겹치지 않는 미래 등록 통과** (5/30 만료 active 보유 + 6/1~ 시작 새 회원권 등록)
- ✅ 클라가 paid_at = "어제"·branch_id = 다른 지점을 보내도 응답 payment.paid_at = KST today, branch_id = 회원의 branch_id

#### Get
- ✅ 정상 조회 → membership + payments(부여 1) + events([])
- ✅ 환불 후 조회 → payments(부여 + 환불 음수 row), events([refund]) 포함
- ✅ 다른 지점 회원권 → 404 (지점 관리자)
- ✅ 존재하지 않는 id → 404

#### Pause
- ✅ 즉시 정지(start=today) → status=paused, end_date 연장
- ✅ 미래 예약(start=내일+) → status=active 유지, end_date 연장
- ✅ pause_used=true에서 다시 → 409 `PAUSE_ALREADY_USED`
- ✅ start > end → 400 `INVALID_PAUSE_RANGE`
- ✅ start < today → 400 `INVALID_PAUSE_RANGE`
- ✅ start < memberships.start_date → 400 `INVALID_PAUSE_RANGE`
- ✅ end > memberships.end_date → 400 `INVALID_PAUSE_RANGE`
- ✅ pause로 인한 end_date 연장이 미래 회원권과 겹침 → 409 `MEMBERSHIP_PERIOD_OVERLAP`(전체 롤백, 미래 회원권 그대로)
- ✅ 다른 지점/존재 안 함 → 404

#### Unpause
- ✅ 정상(4/1~4/7 정지 + 5/30 만료, 4/6 unpause → end_date=5/29, status=active, pause_*=NULL)
- ✅ status=active에서 호출 → 409 `NOT_PAUSED`
- ✅ 다른 지점/존재 안 함 → 404

#### CancelPause
- ✅ 정상(미래 예약 정지 취소, end_date 복원, pause_used=false)
- ✅ 도달한 정지(status=paused)에서 호출 → 409 `PAUSE_NOT_SCHEDULED`
- ✅ pause_used=false에서 호출 → 409 `PAUSE_NOT_SCHEDULED`
- ✅ 다른 지점/존재 안 함 → 404

#### Refund
- ✅ active 회원권 환불 → status=refunded + payments에 음수 row(`amount=-원본`, `method=원본`, `branch_id=원본`)
- ✅ paused 회원권 환불 → 동일
- ✅ active+미래시작 회원권 환불 → 동일
- ✅ expired → 409 `MEMBERSHIP_ALREADY_EXPIRED`
- ✅ 같은 Idempotency-Key 재호출 → 같은 응답, payment row 1개만 추가
- ✅ Idempotency-Key 누락/형식 위반 → 400
- ✅ 클라가 amount/method/paid_at/branch_id 보내도 무시(서버 자동)
- ✅ 다른 지점/존재 안 함 → 404

#### 인증/권한
- ✅ Authorization 헤더 누락 → 401 `UNAUTHORIZED`
- ✅ must_change_password=true 토큰 → 403 `MUST_CHANGE_PASSWORD`

전체 응답에서 `paid_at`/`created_at` 등 timestamp는 `+09:00` 오프셋으로 직렬화됨을 1~2개 핵심 테스트에서 확인.

## 핵심 규칙 (반드시 박는다)

- **단일 트랜잭션**: 부여(membership+payment), 환불(membership+payment+event), pause/unpause/cancel-pause(membership+event)는 모두 한 트랜잭션. `WithTx`로 retry.
- **`paid_at`/`branch_id` 서버 자동**: 클라 입력 무시 — 핸들러가 결정. 무시했음을 테스트로 보장.
- **회원권 부여 amount > 0**: 0/음수는 400. 무료 결제 미지원.
- **start_date >= today**: 과거 부여 불가.
- **EXCLUDE → 409 변환**: `apperr.FromDBError`(`23P01` → `MEMBERSHIP_PERIOD_OVERLAP`) 자동 매핑 활용. 추가 잠금 불필요.
- **pause는 회원권당 1회**: `pause_used=true`면 409. cancel-pause로 미래 예약을 취소하면 다시 false로 돌아가 재등록 가능.
- **idempotency 24h 만료**: 24시간 지난 키는 새 키처럼 처리(repo가 처리).
- **idempotency 응답 적재 실패가 핵심 흐름을 막지 않음**: `Store` 실패는 slog만, 응답은 정상 반환. 다음 같은 키 호출이 실제 작업을 한 번 더 할 수도 있는 트레이드오프 — 받아들임.
- **다른 지점/soft-deleted/미존재 → 404 통일**(403 아님).
- **환불 row 자동 채움**: `paid_at`/`method`/`amount`/`branch_id`는 서버가 원본 결제 row에서 가져온다. 클라는 `reason`만 보낸다.
- **MVP는 전체 환불만**: 부분 환불 미지원. 환불 row의 amount는 원본 amount의 부호 반전.

## Acceptance Criteria

```bash
set -a; source ../../.env; set +a
export TEST_DATABASE_URL="${TEST_DATABASE_URL:-$DATABASE_URL}"

cd backend

# 빌드/테스트
go vet ./...
go build ./...
go test -short -race ./...
go test -race -tags=integration ./...

# memberships.go 파일이 실제 만들어졌는지 (step6 회귀 재발 방지)
test -f internal/http/memberships.go

# 6개 핸들러 함수가 정의됐는지
for fn in Grant Get Pause Unpause CancelPause Refund; do
  grep -q "func (.*MembershipsHandler) ${fn}\b\|func ${fn}MembershipsHandler\|func ${fn}Membership\b" internal/http/memberships.go \
    || { echo "MISS handler: ${fn}"; exit 1; }
done

# 6개 라우트가 main.go에 등록됐는지
grep -q '/members/:id/memberships'        cmd/server/main.go || { echo "MISS route: POST grant"; exit 1; }
grep -q '/memberships/:id"'               cmd/server/main.go || { echo "MISS route: GET detail"; exit 1; }
grep -q '/memberships/:id/pause'          cmd/server/main.go || { echo "MISS route: pause"; exit 1; }
grep -q '/memberships/:id/unpause'        cmd/server/main.go || { echo "MISS route: unpause"; exit 1; }
grep -q '/memberships/:id/cancel-pause'   cmd/server/main.go || { echo "MISS route: cancel-pause"; exit 1; }
grep -q '/memberships/:id/refund'         cmd/server/main.go || { echo "MISS route: refund"; exit 1; }

# e2e 테스트가 카탈로그를 커버하는지(키워드 grep으로 최소 검증)
TEST=internal/http/memberships_test.go
test -f "$TEST"
for kw in INVALID_START_DATE INVALID_AMOUNT IDEMPOTENCY_KEY_REQUIRED IDEMPOTENCY_KEY_CONFLICT INVALID_IDEMPOTENCY_KEY MEMBERSHIP_PERIOD_OVERLAP MEMBERSHIP_ALREADY_EXPIRED PAUSE_ALREADY_USED INVALID_PAUSE_RANGE NOT_PAUSED PAUSE_NOT_SCHEDULED MUST_CHANGE_PASSWORD; do
  grep -q "$kw" "$TEST" || { echo "MISS test coverage: ${kw}"; exit 1; }
done
```

위 모든 명령이 exit 0이어야 PASS.

## 작업 마감 절차 (B 방안 — 책임 분리)

1. AC 명령 직접 실행해 빌드/테스트·grep 검증 모두 통과 확인. **commit 전에 모든 검증이 통과해야 한다.**
2. 변경된 코드(`internal/http/memberships.go`, `internal/http/memberships_test.go`, `cmd/server/main.go`)를 conventional commit으로 worktree(`feat/phase2-backend-scaffold-be`)에 commit.
3. **`phases/`는 절대 만지지 마라** — hook이 차단한다. status·summary·timestamp는 박지 마라.
4. **commit 직후 즉시 종료**한다. 다음 행동은 모두 금지:
   - 추가 도구 호출(테스트 재실행, 파일 재읽기, code-review 시뮬레이션, 추가 commit 등) 금지
   - 마무리 요약·보고 메시지 출력 금지

   부모 execute.py가 자식 종료 직후 acceptance와 code-reviewer를 다시 돌린다.
5. 사용자 개입이 필요한 상황(ADR 갱신, 도구 미설치, 의도와 다른 repo 시그니처 발견 등)이면 **commit하지 말고** stdout에 사유 한 단락만 쓰고 종료. execute.py가 retry/error/blocked로 판정한다.

## 금지사항

- `frontend/`·공유 파일(`docs/` 포함) 변경 금지.
- **repo 레이어(`internal/repo/memberships_repo.go`, `payments_repo.go`, `events_repo.go`, `idempotency_repo.go`, `internal/idempotency/idempotency.go`) 수정 금지** — 이미 step6에서 검증된 SQL이다. 시그니처가 명세와 다르면 핸들러 쪽에서 어댑트하거나 commit 없이 보고.
- 부여/환불에서 `paid_at`/`branch_id`를 클라 입력값 그대로 사용 금지(서버 자동).
- 부여 amount=0 허용 금지.
- start_date 과거 허용 금지.
- pause를 `pause_used=true`인 회원권에 두 번째로 등록 허용 금지(cancel-pause로 false 복원 후만 가능).
- 환불 row의 `amount`/`method`/`branch_id`/`paid_at`을 클라 입력으로 채우지 마라.
- Idempotency-Key 헤더 누락 시 정상 처리 금지(부여·환불 모두 400).
- ADR 외 라이브러리 추가 금지.
- step7의 체크인/매출/bulk-extend는 여기서 만들지 마라(이미 존재).
- 부분 환불 구현 금지(MVP 범위 밖).
- **다른 지점·soft-deleted·미존재에 403 응답 금지(404 통일)**.
- **테스트 누락한 채로 PASS 처리 금지** — 위 grep 검증이 그것을 강제한다.
