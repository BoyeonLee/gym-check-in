---
agent: backend
depends_on: [batch-finalize]
summary: "backend/PHASE2_AC.md 작성(50+ 항목, 각 항목에 대응 테스트 파일·함수명 매핑) + grep 매핑 검증 + 누락 항목 보강 테스트 추가 + 커버리지 측정(go tool cover)."
---

# Step 9: Phase 2 검증 기준 자가 점검 + 누락 테스트 보강 + 커버리지

## 목표

step1~8까지 누적된 backend 코드가 ROADMAP의 "Phase 2 검증 기준"을 빠짐없이 커버하는지 체크리스트로 검증하고, 누락된 항목이 발견되면 테스트로 보강한다. 마지막에 커버리지(참고선)를 측정해 phase 2를 마감한다.

산출물:
- `backend/PHASE2_AC.md` — 50+ 항목 체크리스트, 각 항목에 대응 테스트 파일·함수명 매핑(`// covered: <test_file>:<func_name>` 형식)
- 누락 발견 시 테스트 보강 (가장 가변적이지만 step1~7이 reviewer PASS를 통과한 코드라 누락은 적을 것으로 예상)
- 커버리지 측정 결과 (참고선, 미달이어도 PASS)

## 읽어야 할 파일

- `CLAUDE.md`, `backend/CLAUDE.md` (전체)
- `docs/ROADMAP.md` — Phase 2 산출물·검증 기준 전체(이 체크리스트의 원천)
- `docs/TESTING.md` — 에러/엣지 카탈로그
- `docs/API.md` — 엔드포인트 명세·에러 코드(매핑 시 코드명 일치 확인용)
- step1~8 산출물 코드(`backend/internal/**/*_test.go` 전체) — grep 검증 대상

## 작업

### 1. `backend/PHASE2_AC.md` 작성

`backend/PHASE2_AC.md` (shared 영역인 `docs/`에 두지 않는다 — backend agent가 워크트리 안에서 직접 만들 수 있는 위치)에 다음 형식으로 50+ 항목을 적는다. 각 줄은 `- [x]`로 시작(이 항목들은 step1~8에서 이미 구현·테스트되어 있어야 한다는 가정).

```
- [x] 시드 관리자로 로그인 → access/refresh + must_change_password=true (step3 e2e)  // covered: internal/http/admins_auth_test.go:TestLogin_SeedAdmin
- [x] access 만료 → refresh로 재발급 → 원 요청 재시도 성공 (step3)  // covered: ...
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

각 항목 옆 `// covered:` 주석에 **실재하는 테스트 파일과 함수명**을 적는다. ROADMAP/TESTING.md를 다시 읽어 누락된 항목이 있다면 추가한다(50+는 최소선).

### 2. 매핑 검증 (grep)

각 `// covered: <file>:<func>` 항목에 대해 다음을 보장한다:
- 파일이 실제로 존재 (`ls backend/<file>`)
- 함수가 실제로 정의 (`grep -n "func <func>" backend/<file>`)

빠르게 일괄 검증할 수 있도록 PHASE2_AC.md 끝에 검증 스크립트 한 토막을 둔다(또는 `internal/testutil/phase2_audit_test.go`로 자동화):

```bash
# 스크립트 예시 — 모든 covered 항목이 grep으로 잡히는지 점검
awk -F'covered: ' '/covered:/{print $2}' backend/PHASE2_AC.md | while read -r entry; do
  file=$(echo "$entry" | awk -F: '{print $1}')
  func=$(echo "$entry" | awk -F: '{print $2}')
  grep -q "^func ${func}" "backend/${file}" || echo "MISS: ${file}:${func}"
done
```

`MISS:`가 0개여야 한다.

### 3. 누락 항목 보강 테스트

매핑 검증에서 `MISS:`가 발견되거나, 카탈로그 항목 자체가 step1~8에서 빠진 게 발견되면 **이 step에서 보강 테스트를 추가**한다. 핸들러 테스트(`internal/http/`) 또는 도메인 단위 테스트(`internal/domain/`) 적절한 위치에 새 `Test_*` 함수를 작성하고, `// covered:` 매핑을 업데이트한다.

보강 작업의 분량은 step1~7이 reviewer PASS를 통과한 만큼 크지 않을 것으로 예상되지만, 만약 누락이 광범위하면(예: 5개 이상) 이 step의 turn 예산을 넘길 수 있다. 그 경우 **commit 없이 stdout에 사유 보고 후 종료** — 사용자가 별도 step으로 분할하도록 한다.

### 4. 커버리지 측정 (참고선)

```bash
go test -race -tags=integration -coverprofile=/tmp/cov.out ./...
go tool cover -func=/tmp/cov.out | tail -20
go tool cover -func=/tmp/cov.out | grep -E '^total:'
```

- 핸들러 80%, 도메인 90% 목표(절대 기준 아님 — 미달이어도 PASS).
- PHASE2_AC.md 끝에 측정 결과 한 줄(`<!-- coverage: total 78.4%, internal/http 81.2%, internal/domain 89.5% (2026-05-08) -->` 형식)을 코멘트로 남긴다.

## 핵심 규칙 (반드시 박는다)

- **체크리스트는 backend/ 내부에**: `docs/`는 shared 영역이므로 이 step에서 수정 불가. `backend/PHASE2_AC.md`만 만든다.
- **누락 발견 시 보강이 핵심**: `MISS:` 카운트 0이 PASS의 전제 조건.
- **테스트 추가만 허용**: 이 step에서 프로덕션 코드(`internal/http/*.go`, `internal/domain/*.go`, `internal/repo/*.go` 비-test 파일)를 변경하지 마라. 누락이 발견되었는데 프로덕션 코드 수정 없이 테스트만으로 커버할 수 없다면 step1~8의 회귀이므로 **commit 없이 보고 후 종료**.
- **shared 문서 수정 금지**: `docs/ROADMAP.md`/`docs/TESTING.md`에 보완 사항이 있어도 이 step에서 만지지 말고 별도 shared step으로 사용자에게 제안.

## Acceptance Criteria

```bash
set -a; source ../../.env; set +a
export TEST_DATABASE_URL="${TEST_DATABASE_URL:-$DATABASE_URL}"

cd backend

# 빌드/테스트 — step8까지 통과한 상태 유지
go vet ./...
go build ./...
go test -short -race ./...
go test -race -tags=integration ./...

# PHASE2_AC.md 존재 + 50+ 체크 항목
test -f PHASE2_AC.md
test "$(grep -c '^- \[x\]' PHASE2_AC.md)" -ge 50

# covered 매핑 검증 — 모든 (file:func)가 실재해야 한다 (MISS 0)
awk -F'covered: ' '/covered:/{print $2}' PHASE2_AC.md | while read -r entry; do
  file=$(echo "$entry" | awk -F: '{print $1}')
  func=$(echo "$entry" | awk -F: '{print $2}')
  test -n "$file" && test -n "$func" || continue
  grep -q "^func ${func}" "${file}" || { echo "MISS: ${file}:${func}"; exit 1; }
done

# 커버리지 측정 (참고선, 결과 출력만 — 미달이어도 PASS)
go test -race -tags=integration -coverprofile=/tmp/cov.out ./...
go tool cover -func=/tmp/cov.out | grep -E '^total:'
```

자가 점검:
- `PHASE2_AC.md`의 모든 `- [x]` 항목 옆에 `// covered: <file>:<func>` 매핑이 있는지
- `MISS: ...` 출력이 0줄인지
- 보강 테스트가 추가되었다면 그 테스트가 실제로 PASS인지

## 작업 마감 절차 (B 방안 — 책임 분리)

1. AC 명령 직접 실행해 빌드/테스트 통과 확인. **commit 전에 모든 테스트가 통과해야 한다.**
2. **체크리스트 100% 충족 자가 점검**: `backend/PHASE2_AC.md`의 모든 항목에 대응 테스트가 grep으로 잡히는지, `MISS:`가 0개인지 직접 확인.
3. 변경된 코드(PHASE2_AC.md + 보강 테스트)를 conventional commit으로 worktree(`feat/phase2-backend-scaffold-be`)에 commit. **`phases/`는 절대 만지지 마라** — hook이 차단한다.
4. **commit 직후 즉시 종료**한다. 다음 행동은 모두 금지:
   - 추가 도구 호출(테스트 재실행, 파일 재읽기, code-review 시뮬레이션, 추가 commit 등) 금지
   - 마무리 요약·보고 메시지 출력 금지
   - status·summary·timestamp는 박지 마라

   부모 execute.py가 자식 종료 직후 acceptance(go vet/build/test -race)와 code-reviewer를 **다시** 돌린다. 자식이 commit 후 무엇을 더 해도 부모 검증이 항상 최종이라 자식의 추가 작업은 100% 폐기물이다. max-turns 도달의 가장 흔한 원인이 commit 이후의 불필요한 마무리 턴이라 이를 명시적으로 차단한다. **phase2 전체 status도 execute.py가 마지막 step 통과 시 자동으로 `completed`로 마크**한다(top-level `phases/index.json`).
5. 사용자 개입이 필요한 상황(누락 광범위, 프로덕션 코드 회귀 의심, 도구 미설치 등)이면 **commit하지 말고** stdout에 사유 한 단락만 쓰고 종료. execute.py가 retry/error/blocked로 판정한다. 이 경로는 "commit 후 즉시 종료"와 별개다 — commit 자체가 발생하지 않으면 마감 절차 4를 거치지 않는다.

## 금지사항

- `frontend/`·공유 파일(`docs/` 포함) 변경 금지.
- 프로덕션 코드(`internal/http/*.go`, `internal/domain/*.go`, `internal/repo/*.go` 비-test 파일, `cmd/server/*.go`, `internal/batch/*.go`) 변경 금지 — 이 step은 **테스트 보강 + PHASE2_AC.md 작성**만 한다.
- ROADMAP/TESTING.md 같은 shared 문서 수정 금지 — 보완 사항이 있으면 별도 shared step으로 사용자에게 제안.
- ADR 외 라이브러리 추가 금지.
- 체크리스트 항목 누락한 채로 PASS 처리 금지.
- 누락 보강 분량이 큰 경우(5개+) 무리하게 모두 채우지 말고 commit 없이 보고 후 종료 — max-turns 회피.
