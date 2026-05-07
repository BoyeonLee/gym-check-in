---
agent: backend
depends_on: [memberships]
---

# Step 7: 체크인 + 매출 집계 + 대량 연장

## 목표

step6에서 만든 회원권을 **소비**(체크인)·**집계**(매출)·**일괄 조작**(대량 연장)하는 라우트들을 한 step에 끝낸다. step6의 `idempotency_keys` 인프라와 step5의 cursor 헬퍼를 재사용한다.

산출물:
- `internal/cache/lru.go` — 5초 TTL 인-프로세스 LRU (체크인 멱등성)
- `internal/repo/checkins_repo.go` — 체크인 INSERT(트랜잭션, 활성 회원권 lock), 조회(raw/daily)
- `internal/http/checkins.go` — `POST /api/check-ins`, `GET /api/check-ins`
- `internal/http/sales.go` — `GET /api/sales/summary` (전역 전용)
- `internal/http/bulk_extend.go` — `POST /api/memberships/bulk-extend` (전역 + idempotency)
- `internal/repo/memberships_repo.go` 확장 — `BulkExtend` 트랜잭션

## 읽어야 할 파일

- `CLAUDE.md`, `backend/CLAUDE.md` (`### 체크인`, `### 회원권`(bulk-extend 부분), `### 자정 KST 배치`, 매출 섹션)
- `db/CLAUDE.md` — check_ins/payments/memberships 테이블, KST 변환 SQL 규칙(`(now() AT TIME ZONE 'Asia/Seoul')::date`)
- `docs/API.md` — 체크인/매출/bulk-extend 명세, 에러 코드(`NO_ACTIVE_MEMBERSHIP`, `MEMBERSHIP_NOT_STARTED`, `INVALID_AGGREGATE`, `RANGE_TOO_LARGE`, `INVALID_EXTEND_DAYS`, `MEMBERSHIP_PERIOD_OVERLAP`)
- `docs/TESTING.md` — 체크인/매출 테스트 카탈로그 (race·동시성·5초 LRU·횟수권 차감)
- step1~6 산출물 (특히 `idempotency`, `apperr.FromDBError`, `WithTx`, cursor 공용 헬퍼)

## 작업

### 1. `internal/cache/lru.go` — 5초 TTL 멱등성 캐시

```go
type CheckInResult struct {
    ID          int64
    CheckedInAt time.Time
    Body        []byte    // 응답 JSON
    Status      int
}

type LRU struct {
    // sync.Mutex 보호 map[string]entry, doubly linked list로 LRU 관리.
    // 키 = "<member_id>:<branch_id>". TTL 5초.
}
func NewLRU(maxEntries int, ttl time.Duration, clock testutil.Clock) *LRU
func (c *LRU) Get(key string) (CheckInResult, bool)   // bool=false면 만료/미존재
func (c *LRU) Set(key string, v CheckInResult)
```

- 단일 인스턴스 가정 — 코드 주석으로 명시(루트 CLAUDE.md CRITICAL).
- `maxEntries`는 1000 권장(키오스크 트래픽이 작으므로 충분).
- `Get`은 만료 검사 후 만료면 false 반환 + 엔트리 lazy 제거.
- 단위 테스트: Set→Get true, TTL 경과 후 false, max 초과 시 LRU eviction.

### 2. `internal/repo/checkins_repo.go`

```go
type CheckInRow struct {
    ID            int64
    MemberID      int64
    BranchID      int64
    MembershipID  int64
    CheckedInAt   time.Time
    CreatedAt     time.Time
    UpdatedAt     time.Time
}

// 활성 회원권 잠금 + 체크인 + 횟수권 차감을 한 트랜잭션.
type CheckInInput struct {
    MemberID  int64
    BranchID  int64
    Today     time.Time     // KST 오늘
}
type CheckInResult struct {
    Row              CheckInRow
    Membership       MembershipRow   // 잠긴 회원권 (응답 미노출 — 디버깅용)
    DecrementedRemaining bool         // 횟수권 차감 여부
    NewlyExpired     bool             // 차감 후 remaining=0으로 전환됐는지
}
func DoCheckIn(ctx, tx pgx.Tx, in CheckInInput) (CheckInResult, error)
```

`DoCheckIn` 트랜잭션 내부 SQL:

```sql
-- 1) 활성 회원권 잠금 (없으면 caller가 적절한 apperr 분기)
SELECT id, type, status, start_date, end_date, remaining
FROM memberships
WHERE member_id = $1
  AND status = 'active'
  AND start_date <= $today
  AND end_date >= $today
ORDER BY end_date ASC
FOR UPDATE
LIMIT 1;
```

caller(핸들러)가 결과 분기:
- 잠긴 row 없음 → `status='active'` 회원권을 `start_date>today`로 다시 조회해 있으면 `MEMBERSHIP_NOT_STARTED`, 없으면 `NO_ACTIVE_MEMBERSHIP`.

```sql
-- 2) 같은 회원·같은 날·같은 지점의 첫 체크인인지
SELECT 1 FROM check_ins
WHERE member_id = $1
  AND branch_id = $2
  AND (checked_in_at AT TIME ZONE 'Asia/Seoul')::date = $today;
```

```sql
-- 3) 첫 체크인이고 type='pass10'이면 차감
UPDATE memberships
SET remaining = remaining - 1,
    status = CASE WHEN remaining - 1 <= 0 THEN 'expired' ELSE status END
WHERE id = $1 AND type = 'pass10';
```

```sql
-- 4) check_ins INSERT (membership_id NOT NULL)
INSERT INTO check_ins (member_id, branch_id, membership_id) VALUES (...) RETURNING ...;
```

```go
// 관리자 조회 — raw 모드 (cursor 페이지)
type ListCheckInsInput struct {
    ScopeBranchID *int64       // 지점 관리자는 자기 지점만
    BranchFilter  *int64        // 전역이 특정 지점 필터
    From, To      time.Time
    Cursor        *Cursor
    Limit         int           // default 20, max 100
}
func ListCheckInsRaw(ctx, q Querier, in ListCheckInsInput) (rows []CheckInRow, nextCursor *Cursor, err error)

// 관리자 조회 — daily 모드 (페이지네이션 없음, 92일 제한)
type DailyCheckInRow struct {
    MemberID    int64
    Date        time.Time   // KST date
    CheckinCount int
}
func ListCheckInsDaily(ctx, q Querier, in ListCheckInsInput) ([]DailyCheckInRow, error)
```

### 3. `internal/http/checkins.go` — 2개 라우트

#### `POST /api/check-ins` (인증 면제 — 키오스크)

- body: `{ "branchId": 1, "memberId": 42 }`.
- 5초 LRU 조회: key = `"<member_id>:<branch_id>"`. 적중 시 저장된 응답·status 그대로 반환(새 row 미생성).
- KST today 계산.
- `WithTx`로 `DoCheckIn` 호출:
  - 활성 회원권 lock 실패 → `MEMBERSHIP_NOT_STARTED`/`NO_ACTIVE_MEMBERSHIP` 분기 후 422.
  - 성공 → 응답 200 + `{ id, checked_in_at, member_id, branch_id, membership: { type, remaining_after?, expired_after? } }`. **PII(이름·전화) 미포함** — 키오스크 본인 확인용 응답에는 회원 ID만.
- LRU에 저장.

#### `GET /api/check-ins` (인증 + 가드)

- 지점 관리자: `ScopeBranchID` 강제. 전역: 미강제.
- query: `from`, `to` (YYYY-MM-DD, KST), `branchId?` (전역만), `aggregate=raw|daily` (default raw), `cursor?`, `limit?`.
- `aggregate` 외 값 → 400 `INVALID_AGGREGATE`.
- `to - from > 92일` → 400 `RANGE_TOO_LARGE`.
- `aggregate=daily`는 페이지네이션 없음. `aggregate=raw`는 cursor.
- 응답:
  - raw: `{ items: [{id, member_id, member_name, branch_id, membership_type, checked_in_at}], next_cursor }`
  - daily: `{ items: [{member_id, member_name, date, checkin_count}] }` — 92일 한도라 클라가 한 번에 받음.

### 4. `internal/http/sales.go` — `GET /api/sales/summary` (전역 전용)

- `RequireGlobal` 가드.
- query: `from`, `to` (YYYY-MM-DD, KST), `branchId?`.
- `to - from > 365일`은 허용(매출은 연 단위 조회 가능). 다만 60일 미만 권장(주석).
- SQL: `payments` 테이블에서 `paid_at` 기반 집계.
  ```sql
  SELECT
    SUM(CASE WHEN amount > 0 THEN amount ELSE 0 END)         AS gross_total,
    SUM(CASE WHEN amount < 0 THEN -amount ELSE 0 END)        AS refund_total,
    SUM(amount)                                               AS net_total,
    method,
    paid_at
  FROM payments
  WHERE paid_at BETWEEN $from AND $to
    AND ($branchId IS NULL OR branch_id = $branchId)
  GROUP BY ROLLUP(paid_at, method);
  ```
  (실제 SQL은 단일 쿼리로 안 풀리면 3 쿼리: total / by_method / by_day. 한 트랜잭션 안에서 SNAPSHOT 일관성.)
- 응답:
  ```json
  {
    "gross_total": 12000000,
    "refund_total": 200000,
    "net_total": 11800000,
    "by_method": [
      { "method": "cash", "gross_total": ..., "refund_total": ..., "net_total": ... },
      { "method": "card", "gross_total": ..., "refund_total": ..., "net_total": ... }
    ],
    "by_day": [
      { "date": "2026-05-07", "gross_total": ..., "refund_total": ..., "net_total": ... },
      ...
    ]
  }
  ```

### 5. `internal/http/bulk_extend.go` — `POST /api/memberships/bulk-extend`

- `RequireGlobal` 가드.
- header: `Idempotency-Key` 필수. 누락 → 400 `IDEMPOTENCY_KEY_REQUIRED`. 형식 위반 → 400.
- body: `{ branch_id?, type?, days, reason }`.
  - `days`: 양의 정수 1~90. 그 외 → 400 `INVALID_EXTEND_DAYS`.
  - `reason`: 빈 문자열 거부.
- idempotency lookup → 같은 키·같은 body면 첫 응답 그대로 반환. 다른 body → 409 `IDEMPOTENCY_KEY_CONFLICT`.
- 대상 범위: `status IN ('active','paused')` AND optional branch/type 필터. soft-deleted 회원의 회원권은 제외.
- `WithTx` 한 번에 전체 처리:
  ```sql
  -- 1) 대상 잠금
  SELECT id, status, end_date, pause_start_date, pause_end_date, pause_used
  FROM memberships
  WHERE status IN ('active','paused')
    AND ($branch IS NULL OR member_id IN (SELECT id FROM members WHERE branch_id = $branch AND deleted_at IS NULL))
    AND ($type IS NULL OR type = $type)
  ORDER BY id ASC
  FOR UPDATE;

  -- 2) UPDATE end_date += days. paused 또는 active+미래 예약 정지면 pause_*도 +days.
  UPDATE memberships SET
    end_date = end_date + $days * interval '1 day',
    pause_start_date = CASE
      WHEN status = 'paused' OR (status = 'active' AND pause_used = true AND pause_start_date > $today)
      THEN pause_start_date + $days * interval '1 day' ELSE pause_start_date END,
    pause_end_date = CASE
      WHEN status = 'paused' OR (status = 'active' AND pause_used = true AND pause_start_date > $today)
      THEN pause_end_date + $days * interval '1 day' ELSE pause_end_date END
  WHERE id = ANY($ids);

  -- 3) membership_events INSERT (각 row마다 action='bulk_extend', extend_days=$days, reason)
  ```
- EXCLUDE 위반(`23P01`) → 409 `MEMBERSHIP_PERIOD_OVERLAP` + body에 `first_conflict_membership_id` (충돌 row의 id — 트랜잭션 롤백 전에 미리 식별). `extended_count = 0`.
- 응답 200 + `{ extended_count: N, days: 30, reason: "..." }`.
- audit: 별도 admin_audit_logs 액션은 정의하지 않음 — `membership_events` row가 누가(performed_by) 무엇을 했는지 모두 기록하므로.

### 6. 라우트 등록

```go
// 키오스크 공개
public.POST("/check-ins", checkins.Create)

// 인증 보호
protected.GET("/check-ins", checkins.List)
protected.GET("/sales/summary", middleware.RequireGlobal(), sales.Summary)
protected.POST("/memberships/bulk-extend", middleware.RequireGlobal(), memberships.BulkExtend)
```

`POST /api/check-ins`는 키오스크가 부르므로 인증 면제. body의 `branchId`/`memberId` 검증은 핸들러가 수행하되, 검증 실패는 422가 아닌 400(검증 단계).

### 7. 핸들러 테스트 — 정상 + 카탈로그 에러

#### 체크인
- 정상 (monthly·pass10 둘 다)
- 활성 회원권 없음 → 422 `NO_ACTIVE_MEMBERSHIP`
- 미래 시작 회원권만 보유 → 422 `MEMBERSHIP_NOT_STARTED`
- paused 회원권 → 422 `NO_ACTIVE_MEMBERSHIP`
- expired 회원권 → 422 `NO_ACTIVE_MEMBERSHIP`
- soft-deleted 회원 → 422 (회원이 없는 것과 같음. 또는 404 — 프로젝트 정책: 키오스크는 422로 통일)
- 같은 날 두 번 체크인 → check_ins 2 row, remaining 1만 감소 (pass10)
- pass10 마지막 체크인 → remaining=0 + status='expired' 같은 트랜잭션
- 같은 회원·지점에 5초 안에 두 번 → 같은 응답 반환, row 1개만
- 5초 경과 후 다시 체크인 → 새 row 1개 (LRU 만료)
- 동시 체크인 race(같은 회원에 2 요청 동시) → 둘 중 하나는 LRU 적중 또는 retry로 단일 row
- 응답 timestamp `+09:00` 오프셋

#### 체크인 조회
- 지점 관리자 자기 지점만, 전역은 전체
- 전역이 `branchId` 필터 사용 가능
- aggregate=raw cursor 페이지네이션
- aggregate=daily 페이지네이션 없음, 같은 회원·같은 날 1 row
- aggregate=invalid → 400 `INVALID_AGGREGATE`
- 92일 초과 → 400 `RANGE_TOO_LARGE`
- 잘못된 cursor → 400 `INVALID_CURSOR`
- limit 100/101 → 통과/400

#### 매출 집계
- 전역만 200, 지점 관리자 → 403
- gross_total/refund_total/net_total 분리
- by_method (cash/card 분리)
- by_day 일별 row
- 환불 row가 정확히 음수 합계로 반영
- branch_id 필터링

#### bulk-extend
- 정상 — 대상 회원권의 end_date += days, paused는 pause_* +days, 미래 예약 정지(active+pause_used+pause_start_date>today)도 pause_* +days, 일반 active는 end_date만
- days=0/91/-1 → 400 `INVALID_EXTEND_DAYS`
- 같은 키·같은 body 두 번 → 첫 응답, end_date 한 번만 연장
- 같은 키·다른 body → 409 `IDEMPOTENCY_KEY_CONFLICT`
- 지점 관리자 호출 → 403
- 충돌 발생 시 → 409 `MEMBERSHIP_PERIOD_OVERLAP` + `first_conflict_membership_id` + 전체 롤백 (다른 회원권 end_date 그대로)
- expired/refunded 회원권은 영향 없음
- soft-deleted 회원의 회원권은 제외

## 핵심 규칙 (반드시 박는다)

- **활성 회원권 SELECT FOR UPDATE**: 동시 체크인 race를 막는 1차 방어. retry 헬퍼는 40001/40P01 흡수.
- **5초 LRU**: 인-프로세스 메모리. 단일 인스턴스 가정. 코드 주석으로 명시.
- **횟수권 차감 트랜잭션 단일성**: lock → 같은 날 첫 체크인 검사 → 차감 → INSERT 한 트랜잭션. lock과 차감 사이에 다른 트랜잭션 끼면 race.
- **`MEMBERSHIP_NOT_STARTED` 분기**: 잠긴 row 없음 + 미래 시작 row 있음일 때만. start_date>today 검사는 핸들러가 별도 SELECT로 명시.
- **체크인 응답 PII 미포함**: 키오스크 응답은 ID만. 이름·전화 미노출.
- **매출은 payments만 본다**: memberships/check_ins로 매출을 역산하지 마라.
- **bulk-extend 전체 롤백**: 충돌 발생 시 한 row도 변경되지 않음. 응답 `extended_count=0`.
- **bulk-extend는 EXCLUDE 위반 식별 정보 제공**: `first_conflict_membership_id`로 운영자가 즉시 디버깅 가능.
- **체크인 KST 날짜 비교**: `(checked_in_at AT TIME ZONE 'Asia/Seoul')::date` 사용. `CURRENT_DATE` 금지.
- **응답 timestamp KST `+09:00`**: 모든 timestamptz 응답.
- **idempotency 24h 만료**: bulk-extend 키도 동일.

## Acceptance Criteria

```bash
set -a; source ../../.env; set +a
export TEST_DATABASE_URL="${TEST_DATABASE_URL:-$DATABASE_URL}"

cd backend
go vet ./...
go build ./...
go test -short -race ./...
go test -race -tags=integration ./...

# 5초 LRU 동시성 — 통합 테스트가 race 모드로 검증.
```

자가 점검 시나리오:
- 같은 회원에 동시 2 체크인 → row 1개 (LRU 적중)
- 키오스크 미인증 호출 → 200 (인증 면제)
- 매출 = 부여 결제 합 - 환불 결제 합 (수동 검산)
- bulk-extend로 paused 회원권의 pause_end_date도 +days
- bulk-extend 충돌 시 다른 회원권 end_date 변경 없음

## 검증 절차

1. AC 명령 직접 실행.
2. `code-reviewer` 서브에이전트 호출. 입력: 단계 이름(`phase2-backend-scaffold/checkins-sales-bulk`), `git diff HEAD --stat`. PASS 응답 필요.
3. step7 status 업데이트:
   - PASS → `"status": "completed"` + `"summary": "POST /api/check-ins(5초 LRU + FOR UPDATE + 횟수권 차감 + remaining=0→expired) + GET /api/check-ins(raw/daily, 92일 한도) + GET /api/sales/summary(gross/refund/net 분리, 전역) + POST /api/memberships/bulk-extend(전역 + idempotency + paused/예약정지 pause_* +days + first_conflict_membership_id)."`

## 금지사항

- `frontend/`·공유 파일 변경 금지.
- 키오스크 체크인 응답에 회원 PII(이름·전화·생년월일) 노출 금지.
- 매출을 memberships/check_ins로 역산 금지.
- bulk-extend가 한 row라도 변경한 채 충돌 응답 금지(전체 롤백).
- aggregate=daily에 페이지네이션 추가 금지.
- 체크인 SQL에서 `CURRENT_DATE`/`now()::date` 사용 금지(KST 변환 필수).
- LRU TTL을 5초보다 길게 두지 마라(중복 클릭 방어용 — 운영 정상 흐름을 막으면 안 됨).
- bulk-extend `days`에 음수·0·91+ 허용 금지.
- ADR 외 라이브러리 추가 금지.
- step8의 batch는 여기서 만들지 마라.
