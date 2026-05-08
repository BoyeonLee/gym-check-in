---
agent: backend
depends_on: [checkins-sales-bulk]
summary: "internal/batch(4 트랜잭션 + 정리잡 3종 + Stats) + scheduler(robfig/cron/v3 KST 1 0 * * *) + cmd/server cron→HTTP→DB graceful shutdown + cmd/server batch run-expiry CLI + 통합 테스트(시계 시뮬 7 시나리오)."
---

# Step 8: 자정 KST 배치 + cron + graceful shutdown

## 목표

회원권 상태 전환과 만료 데이터 정리를 자정 KST에 자동 실행하는 배치를 구현하고, cron 등록·CLI 1회 실행·graceful shutdown 순서를 마무리한다. Phase 2 ROADMAP 검증 기준 자가 점검과 커버리지 측정·누락 테스트 보강은 다음 step(`phase2-acceptance-audit`)에서 처리한다.

산출물:
- `internal/batch/batch.go` — 4 트랜잭션 + 정리 잡 3종
- `internal/batch/scheduler.go` — `robfig/cron/v3` KST `1 0 * * *` 등록
- `cmd/server` — cron 시작/정지를 graceful shutdown에 통합 (cron 1순위 정지)
- `cmd/server batch run-expiry` — 외부 스케줄러용 1회 실행 모드
- 통합 테스트 (시계 시뮬 7 시나리오)

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

## 핵심 규칙 (반드시 박는다)

- **자정 배치 KST 변환**: 모든 SQL은 `(now() AT TIME ZONE 'Asia/Seoul')::date`. `CURRENT_DATE` 금지.
- **각 단계 별 트랜잭션**: 한 단계 실패가 다음 단계를 막지 않게.
- **graceful shutdown 순서**: cron → HTTP → DB. 역순으로 하면 진행 중 배치가 풀 닫힌 DB에 접근.
- **batch CLI도 같은 코드 경로**: cron 트리거 콜백과 `batch run-expiry`가 같은 `RunExpiry(ctx, pool, clock)`를 호출. 코드 분기 두지 마라.
- **`Clock` 인터페이스 사용**: `time.Now()` 직접 호출 금지(테스트 결정성).

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
```

자가 점검 (시계 시뮬 통합 테스트):
- `FreezeTime(2026-05-08T00:01:00+09:00)` 후 `RunExpiry` 호출 → expired/active/paused 카운트 일치
- 정리 잡 3종 카운트 일치
- 외부 CLI `batch run-expiry`가 같은 결과 반환

## 작업 마감 절차 (B 방안 — 책임 분리)

1. AC 명령 직접 실행해 빌드/테스트 통과 확인. **commit 전에 모든 테스트가 통과해야 한다.**
2. 변경된 코드를 conventional commit으로 worktree(`feat/phase2-backend-scaffold-be`)에 commit. **`phases/`는 절대 만지지 마라** — hook이 차단한다.
3. **commit 직후 즉시 종료**한다. 다음 행동은 모두 금지:
   - 추가 도구 호출(테스트 재실행, 파일 재읽기, code-review 시뮬레이션, 추가 commit 등) 금지
   - 마무리 요약·보고 메시지 출력 금지
   - status·summary·timestamp는 박지 마라

   부모 execute.py가 자식 종료 직후 acceptance(go vet/build/test -race)와 code-reviewer를 **다시** 돌린다. 자식이 commit 후 무엇을 더 해도 부모 검증이 항상 최종이라 자식의 추가 작업은 100% 폐기물이다. max-turns 도달의 가장 흔한 원인이 commit 이후의 불필요한 마무리 턴이라 이를 명시적으로 차단한다.
4. 사용자 개입이 필요한 상황(ADR 갱신, 도구 미설치 등)이면 **commit하지 말고** stdout에 사유 한 단락만 쓰고 종료. execute.py가 retry/error/blocked로 판정한다. 이 경로는 "commit 후 즉시 종료"와 별개다 — commit 자체가 발생하지 않으면 마감 절차 3을 거치지 않는다.

## 금지사항

- `frontend/`·공유 파일(`docs/` 포함) 변경 금지.
- 자정 배치 SQL에 `CURRENT_DATE` 사용 금지(KST 변환 필수).
- cron 트리거 콜백과 CLI 모드가 다른 코드 경로 사용 금지(같은 `RunExpiry` 호출).
- graceful shutdown에서 DB 풀을 cron보다 먼저 닫지 마라(진행 중 배치 깨짐).
- ROADMAP/TESTING.md 같은 shared 문서 수정 금지 — 보완 사항이 있으면 별도 shared step으로.
- ADR 외 라이브러리 추가 금지(robfig/cron/v3는 ADR-008에 명시되어 있어야 함).
- 정리 잡 보존 기간(24h/15h/1년)을 임의로 조정하지 마라.
- KST 자정 경계 race를 무시하고 `00:00`에 트리거 등록 금지(`1 0 * * *`로 1분 margin).
- Phase 2 검증 기준 자가 점검·`backend/PHASE2_AC.md` 작성·커버리지 측정·누락 테스트 보강은 **이 step에서 하지 마라**. 다음 step(`phase2-acceptance-audit`)이 전담한다.
