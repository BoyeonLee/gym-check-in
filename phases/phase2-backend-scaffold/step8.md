---
agent: backend
depends_on: [checkins-sales-bulk]
summary: "internal/batch(4 트랜잭션 + 정리잡 3종) + cron KST 1 0 * * * + cmd/server batch run-expiry CLI + graceful shutdown 통합(cron→HTTP→DB) + Phase 2 ROADMAP 검증 기준 체크리스트 100% 커버."
---

# Step 8: 자정 KST 배치 + Phase 2 검증 기준 자가 점검

## 목표

회원권 상태 전환과 만료 데이터 정리를 자정 KST에 자동 실행하는 배치를 구현하고, Phase 2 ROADMAP 전체 검증 기준을 체크리스트화해 누락된 테스트를 보완하며 마무리한다.

산출물:
- `internal/batch/batch.go` — 4 트랜잭션 + 정리 잡 3종
- `internal/batch/scheduler.go` — `robfig/cron/v3` KST `1 0 * * *` 등록
- `cmd/server` — cron 시작/정지를 graceful shutdown에 통합 (cron 1순위 정지)
- `cmd/server batch run-expiry` — 외부 스케줄러용 1회 실행 모드
- Phase 2 검증 기준 체크리스트 (`backend/PHASE2_AC.md` 또는 테스트 코멘트 — `docs/`는 shared 영역이므로 backend 내부에 두기) + 누락 테스트 보강

## 읽어야 할 파일

- `CLAUDE.md`, `backend/CLAUDE.md` (`### 자정 KST 배치(internal/batch)` 섹션 통째로)
- `db/CLAUDE.md` — 자정 배치 SQL 규칙(`(now() AT TIME ZONE 'Asia/Seoul')::date`), 보존 기간(idempotency_keys 24h, revoked_refresh_tokens 15h, admin_audit_logs 1년)
- `docs/ROADMAP.md` — Phase 2 산출물·검증 기준 전체(체크리스트로 사용)
- `docs/TESTING.md` — 배치 테스트 카탈로그
- step1~7 산출물 (특히 `Clock` 인터페이스로 시계 주입)

## 작업

### 1. `internal/batch/batch.go`

```go
type Stats struct {
    ExpiredActivated   int   // active → expired
    PausedReactivated  int   // paused → active (pause_end_date 도래)
    ActiveToPaused     int   // active → paused (pause_start_date 도래)
    DeletedIdempotency int
    DeletedRefresh     int
    DeletedAuditLogs   int
    Errors             []error  // 부분 실패 기록 — 핸들러는 첫 에러만 반환하지 않고 모두 진행
}

func RunExpiry(ctx context.Context, pool *pgxpool.Pool, clock testutil.Clock) (Stats, error)
```

내부 트랜잭션 (각각 별 트랜잭션 — 한 단계가 실패해도 나머지는 진행):

```sql
-- 1) active → expired
UPDATE memberships
SET status = 'expired'
WHERE status = 'active'
  AND end_date < (now() AT TIME ZONE 'Asia/Seoul')::date;

-- 2) paused → active (정지 기간 종료)
UPDATE memberships
SET status = 'active', pause_start_date = NULL, pause_end_date = NULL
WHERE status = 'paused'
  AND pause_end_date < (now() AT TIME ZONE 'Asia/Seoul')::date;

-- 3) active → paused (예약된 정지 도래)
UPDATE memberships
SET status = 'paused'
WHERE status = 'active'
  AND pause_start_date = (now() AT TIME ZONE 'Asia/Seoul')::date;

-- 4) 정리 잡 3종 (각각 별 트랜잭션)
DELETE FROM idempotency_keys WHERE created_at < now() - interval '24 hours';
DELETE FROM revoked_refresh_tokens WHERE revoked_at < now() - interval '15 hours';
DELETE FROM admin_audit_logs WHERE created_at < now() - interval '1 year';
```

- 각 트랜잭션의 row 수를 `Stats`에 누적. 실패 시 `Stats.Errors`에 추가하고 다음 단계로 진행.
- 끝에 slog로 요약 출력 (`request_id` 대신 `batch_run_id`(uuid) 필드로 추적성).

단위 테스트:
- `Clock`을 주입해 KST 자정으로 시계 고정.
- 테스트 데이터: 어제 만료된 active, 오늘 만료될 active(미전환), 어제 끝난 paused, 오늘 시작 예약 active, idempotency_keys 25h 전 row, revoked_refresh_tokens 16h 전 row, admin_audit_logs 1년 전 row.
- `RunExpiry` 호출 후 각 카운트 검증.

### 2. `internal/batch/scheduler.go`

```go
type Scheduler struct {
    cron *cron.Cron   // robfig/cron/v3, KST timezone
    stop chan struct{}
}
func NewScheduler(loc *time.Location) *Scheduler
func (s *Scheduler) Register(spec string, fn func()) error   // spec="1 0 * * *"
func (s *Scheduler) Start()
func (s *Scheduler) Stop()                                    // graceful — 진행 중 작업 대기
```

`cmd/server`에서:

```go
loc, _ := time.LoadLocation("Asia/Seoul")
sched := batch.NewScheduler(loc)
sched.Register("1 0 * * *", func() {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
    defer cancel()
    stats, _ := batch.RunExpiry(ctx, pool, realClock)
    slog.Info("batch.run", "stats", stats)
})
sched.Start()

// graceful shutdown 순서:
//   1. sched.Stop() — 새 트리거 차단 + 진행 중 작업 대기
//   2. HTTP 서버 신규 연결 차단 + 진행 중 요청 30초 대기
//   3. DB 풀 close
```

### 3. `cmd/server batch run-expiry` — CLI 모드

```go
// cmd/server/main.go
if len(os.Args) >= 3 && os.Args[1] == "batch" && os.Args[2] == "run-expiry" {
    cfg := config.MustLoad()
    pool := repo.MustNewPool(...)
    defer pool.Close()
    stats, err := batch.RunExpiry(context.Background(), pool, realClock)
    if err != nil {
        fmt.Fprintln(os.Stderr, "batch failed:", err)
        os.Exit(1)
    }
    bytes, _ := json.Marshal(stats)
    fmt.Println(string(bytes))
    os.Exit(0)
}
```

- 외부 스케줄러(systemd timer, k8s CronJob 등)가 호출 가능.
- 실행 결과를 JSON으로 stdout에 1줄 출력.

### 4. 통합 테스트 (시계 시뮬)

`testutil.FreezeTime`을 사용해 다음 시나리오를 통합 테스트로 커버:
- 자정 직전(`23:59:59`)에 active+`end_date=오늘`인 회원권 → 그대로 active
- 자정 직후(`00:01:00`)에 같은 회원권 → expired
- paused이고 `pause_end_date=어제` → active로 복귀, `pause_*` NULL
- active이고 `pause_start_date=오늘` → paused
- idempotency_keys 정리: `created_at = 25h 전` → 삭제, `created_at = 23h 전` → 보존
- revoked_refresh_tokens: 16h 전 삭제, 14h 전 보존
- admin_audit_logs: 366일 전 삭제, 364일 전 보존

### 5. Phase 2 검증 기준 자가 점검

ROADMAP.md의 "Phase 2 검증 기준" 섹션의 모든 항목을 체크리스트화.

`backend/PHASE2_AC.md` (또는 `internal/testutil/phase2_checklist.go` 데이터로 — shared 영역인 `docs/`에 두지 않는다)에 다음 형식:

```
- [x] 시드 관리자로 로그인 → access/refresh + must_change_password=true (step3 e2e)
- [x] access 만료 → refresh로 재발급 → 원 요청 재시도 성공 (step3)
- [x] 로그아웃 후 같은 refresh로 refresh → 401 (step3)
- [x] 비번 변경 후 변경 전 refresh 무효 (step3)
- [x] 5번 비번 틀림 → 6번째 정확해도 401 ACCOUNT_LOCKED, 15분 후 가능 (step3)
- [x] reset-password 24h 만료 → TEMP_PASSWORD_EXPIRED (step4)
- [x] 약한 비번 변경 → WEAK_PASSWORD (step3)
- [x] PATCH branch_id 변경 → 해당 사용자 refresh 무효 (step4)
- [x] 본인 PATCH role/branch_id 변경 → CANNOT_MODIFY_SELF_ROLE (step4)
- [x] 지점 관리자 토큰으로 다른 지점 자원 접근 → 404 (step5/6)
- [x] 지점 관리자가 sales/admins/bulk-extend 접근 → 403 (step4/7)
- [x] admin_audit_logs 자동 기록 (step3/4)
- [x] 회원권 부여 amount<=0 → 400, amount>0 → 결제 row 생성 (step6)
- [x] 환불 후 매출 음수 row 자동 보정 (step6+step7)
- [x] 키오스크 검색 활성 회원권 없는 회원 제외 (step5)
- [x] 활성 회원권 없음 → NO_ACTIVE_MEMBERSHIP (step7)
- [x] paused 회원권 체크인 시도 → 422 (step7)
- [x] 횟수권 같은 날 두 번 → row 2, remaining 1만 감소 (step7)
- [x] 횟수권 마지막 → status=expired 같은 트랜잭션 (step7)
- [x] 같은 회원·지점 5초 내 두 번 체크인 → 같은 응답, row 1개 (step7)
- [x] 같은 회원권 정지 두 번째 → PAUSE_ALREADY_USED (step6)
- [x] paused 상태에서 unpause → end_date 단축 (step6)
- [x] 미래 예약 정지에 cancel-pause → 복원 + pause_used=false (step6)
- [x] start_date 어제 → INVALID_START_DATE (step6)
- [x] 키오스크 search 21명 → truncated=true (step5)
- [x] aggregate=daily 같은 회원 같은 날 1 row, raw 2 row, 92일 초과 RANGE_TOO_LARGE (step7)
- [x] 사용 중 지점 삭제 → BRANCH_IN_USE (step4)
- [x] 지점 주소 충돌 → ADDRESS_DUPLICATE (step4)
- [x] PATCH /api/members/:id에 branch_id 보내도 무시 (step5)
- [x] bulk-extend 같은 키·같은 body 멱등, 다른 body → CONFLICT (step7)
- [x] cursor 페이지 정상, limit=200 → INVALID_LIMIT, 잘못된 cursor → INVALID_CURSOR (step5/7)
- [x] 매출 응답 gross/refund/net 분리 (step7)
- [x] 자정 배치 수동 실행으로 active→expired/paused→active/active→paused/정리잡 (step8)
- [x] 회원권 부여 EXCLUDE 위반 → MEMBERSHIP_PERIOD_OVERLAP, 겹치지 않는 미래 통과 (step6)
- [x] paid_at은 항상 KST today (클라 입력 무시), branch_id 자동 (step6)
- [x] expired 회원권 환불 → MEMBERSHIP_ALREADY_EXPIRED (step6)
- [x] pause start_date < memberships.start_date → INVALID_PAUSE_RANGE (step6)
- [x] pause end_date 연장 결과 미래 회원권과 겹치면 MEMBERSHIP_PERIOD_OVERLAP (step6)
- [x] bulk-extend가 paused/예약정지 pause_* +days (step7)
- [x] bulk-extend 충돌 시 first_conflict_membership_id (step7)
- [x] soft-deleted admin access → 401 즉시 (step3)
- [x] 다른 지점 회원·회원권 → 404 (step5/6)
- [x] soft-deleted 회원에 회원권 부여 → 404 (step6)
- [x] Idempotency-Key UUIDv4 아님 → INVALID_IDEMPOTENCY_KEY (step6)
- [x] access claim 필수 필드 누락 → 401 (step3)
- [x] 미래 시작 회원권 체크인 → MEMBERSHIP_NOT_STARTED (step7)
- [x] 부여·환불 Idempotency-Key 누락 → 400 (step6)
- [x] bulk-extend days 0/91 → INVALID_EXTEND_DAYS (step7)
- [x] 모든 응답 +09:00 + X-Request-ID (step2)
- [x] panic → 500 INTERNAL, stack 미노출 (step2)
- [x] 동시 체크인 race → 40001/40P01 자동 retry, 다른 하나는 LRU 적중 (step7)
```

각 항목에 대응하는 테스트 파일·함수명을 옆에 적어 코드 검색 가능하게(예: `// covered: internal/http/admins_auth_test.go:TestLogin_AccountLocked`).

체크리스트를 채우면서 누락된 테스트가 있으면 이 step에서 보강. **이 보강 작업이 step8의 핵심**.

### 6. 커버리지 측정 (참고선)

```bash
go test -race -coverprofile=coverage.out -tags=integration ./...
go tool cover -func=coverage.out | tail
```

- 핸들러 80%, 도메인 90% 목표(절대 기준 아님).
- 미달 시 누락 케이스를 테스트로 추가.

## 핵심 규칙 (반드시 박는다)

- **자정 배치 KST 변환**: 모든 SQL은 `(now() AT TIME ZONE 'Asia/Seoul')::date`. `CURRENT_DATE` 금지.
- **각 단계 별 트랜잭션**: 한 단계 실패가 다음 단계를 막지 않게.
- **graceful shutdown 순서**: cron → HTTP → DB. 역순으로 하면 진행 중 배치가 풀 닫힌 DB에 접근.
- **batch CLI도 같은 코드 경로**: cron 트리거 콜백과 `batch run-expiry`가 같은 `RunExpiry(ctx, pool, clock)`를 호출. 코드 분기 두지 마라.
- **`Clock` 인터페이스 사용**: `time.Now()` 직접 호출 금지(테스트 결정성).
- **체크리스트는 backend/ 내부에**: `docs/`는 shared 영역이므로 이 step에서 수정 불가. `backend/PHASE2_AC.md` 같은 backend 내부 파일에.
- **누락 테스트 보강이 핵심**: step1~7에서 빠진 카탈로그 항목을 모두 채운다.

## Acceptance Criteria

```bash
set -a; source ../../.env; set +a
export TEST_DATABASE_URL="${TEST_DATABASE_URL:-$DATABASE_URL}"

cd backend
go vet ./...
go build ./...

# 단위 + 통합 + race
go test -short -race ./...
go test -race -tags=integration ./...

# 배치 1회 실행 — 종료 코드 0 + 통계 JSON
go build -o bin/server ./cmd/server
./bin/server batch run-expiry | python3 -c "import json,sys; d=json.load(sys.stdin); print('stats:', d)"

# cron 등록 확인 — 서버 기동 후 로그에 'cron.registered spec=1 0 * * *' 같은 라인이 있는지
PORT=18080 APP_ENV=dev ./bin/server &
SERVER_PID=$!
sleep 1
# (로그 검사는 stdout/stderr 캡처해 grep — 구현 기댓값에 맞춰)
kill $SERVER_PID
wait $SERVER_PID 2>/dev/null || true

# 커버리지 기준선 (참고선, 미달이어도 PASS)
go test -race -tags=integration -coverprofile=/tmp/cov.out ./...
go tool cover -func=/tmp/cov.out | grep -E '^total:'
```

자가 점검 (시계 시뮬 통합 테스트):
- `FreezeTime(2026-05-08T00:01:00+09:00)` 후 `RunExpiry` 호출 → expired/active/paused 카운트 일치
- 정리 잡 3종 카운트 일치
- 외부 CLI `batch run-expiry`가 같은 결과 반환

## 작업 마감 절차 (B 방안 — 책임 분리)

1. AC 명령 직접 실행해 빌드/테스트 통과 확인.
2. **체크리스트 100% 충족 자가 점검**: `backend/PHASE2_AC.md`의 모든 항목에 대응 테스트가 있는지 grep으로 확인. 누락 발견 시 그 step을 다시 보강.
3. 변경된 코드를 conventional commit으로 worktree(`feat/phase2-backend-scaffold-be`)에 commit. **`phases/`는 절대 만지지 마라** — hook이 차단한다.
4. status·summary·timestamp는 박지 마라. execute.py가 acceptance + code-reviewer gate 통과 시 main 인덱스에 frontmatter `summary`를 직접 박는다. **phase2 전체 status도 execute.py가 마지막 step 통과 시 자동으로 `completed`로 마크**(top-level `phases/index.json`).
5. 사용자 개입이 필요한 상황이면 commit하지 말고 stdout에 사유를 쓰고 종료.

## 금지사항

- `frontend/`·공유 파일(`docs/` 포함) 변경 금지.
- 자정 배치 SQL에 `CURRENT_DATE` 사용 금지(KST 변환 필수).
- cron 트리거 콜백과 CLI 모드가 다른 코드 경로 사용 금지(같은 `RunExpiry` 호출).
- graceful shutdown에서 DB 풀을 cron보다 먼저 닫지 마라(진행 중 배치 깨짐).
- ROADMAP/TESTING.md 같은 shared 문서 수정 금지 — 보완 사항이 있으면 별도 shared step으로.
- ADR 외 라이브러리 추가 금지(robfig/cron/v3는 ADR-008에 명시되어 있어야 함).
- 체크리스트 항목 누락한 채로 PASS 처리 금지.
- 정리 잡 보존 기간(24h/15h/1년)을 임의로 조정하지 마라.
- KST 자정 경계 race를 무시하고 `00:00`에 트리거 등록 금지(`1 0 * * *`로 1분 margin).
