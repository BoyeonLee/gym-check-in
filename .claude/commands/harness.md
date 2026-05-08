이 프로젝트는 Harness 프레임워크를 사용한다. 아래 워크플로우에 따라 작업을 진행하라.

체육관 체크인 시스템(모노레포: `frontend/` Vite+React, `backend/` Go+Gin, `db/` PostgreSQL+goose)에 맞춰 step 단위로 backend·frontend 두 worktree에서 **병렬 개발**한다.

---

## 워크플로우

### A. 탐색

다음 문서를 우선 읽고 프로젝트의 기획·아키텍처·계약·로드맵을 파악한다. 필요하면 Explore 서브에이전트를 병렬로 사용한다.

- `docs/PRD.md`, `docs/ARCHITECTURE.md`, `docs/ADR.md` — 전체 그림
- `docs/API.md` — 엔드포인트 계약(병렬 개발의 기준이 되는 문서)
- `docs/ROADMAP.md` — Phase 단위 산출물·검증 기준
- `docs/UI_GUIDE.md`, `docs/DEV_SETUP.md`, `docs/OPERATIONS.md`
- 모듈별 규칙: `CLAUDE.md`(루트), `frontend/CLAUDE.md`, `backend/CLAUDE.md`, `db/CLAUDE.md`

### B. 논의

구현을 위해 구체화하거나 기술적으로 결정해야 할 사항이 있으면 사용자에게 제시하고 논의한다. 특히 `docs/API.md` 계약이 변경되어야 하는지, BE/FE 둘 다 영향을 받는지를 먼저 합의한다(변경 시 별도 `agent: shared` step으로 분리한다).

### C. Step 설계

사용자가 구현 계획 작성을 지시하면 여러 step으로 나뉜 초안을 작성해 피드백을 요청한다.

#### Phase 단계 정책

- **Phase 0 (`.env`·`docker-compose.yml`·`.env.example` 부트스트랩)**: 메인 세션이 단독 처리(`agent: shared`). 코드 변경 없음, 환경 파일·공유 설정만 다룬다.
- **Phase 1 (DB 스키마/시드 — `db/migrations/`·`db/seeds/`)**: `agent: backend`로 처리한다. `.worktrees/backend/`에서 실행. db는 backend 책임 영역이며(`backend/CLAUDE.md` `## 책임 범위`), shared 위치에서는 코드(`backend/`·`frontend/`·`db/`) 변경이 차단되므로 worktree로 옮긴다. Phase 1 단계에선 frontend worktree는 만들지 않는다(병렬화 이득 없음).
- **Phase 2 이후**: `docs/API.md` 계약이 고정된 시점부터 BE/FE를 step 단위로 병렬화한다. 같은 phase 안에서 BE step과 FE step을 독립 worktree에서 동시에 진행한다.

#### Step 분할 원칙

1. **단일 worktree·단일 모듈 자기완결성**: 한 step은 backend 또는 frontend 한 worktree 안에서 완결된다. 두 worktree에 걸친 변경(예: API.md 변경, 스펙 합의)은 먼저 `agent: shared` step으로 처리한 뒤 BE/FE step으로 분리한다.
2. **자기완결성**: 각 step 파일은 독립된 Claude 세션에서 실행된다. "이전 대화에서 논의한 바와 같이" 같은 외부 참조 금지. 필요한 정보·읽어야 할 파일·이전 step 산출물은 step 파일에 모두 명시한다.
3. **시그니처 수준 지시**: 함수/타입의 인터페이스만 제시하고 내부 구현은 에이전트 재량. 단, 설계 의도에서 벗어나면 안 되는 핵심 규칙(멱등성, 트랜잭션 단일화, branch_id 강제, soft delete, PII 마스킹·노출 금지 등)은 반드시 명시한다.
4. **AC는 실행 가능한 커맨드**: "동작해야 한다" 대신 `cd backend && go test ./...` 같은 실제 명령. 백엔드는 `go build && go test`, 프론트는 `pnpm lint && pnpm build && pnpm test`, DB 변경은 `goose up && down && up` 왕복.
5. **주의사항은 구체적으로**: "조심하라" 대신 "X를 하지 마라. 이유: Y" 형식.
6. **네이밍**: `kebab-case-slug`. BE/FE가 분리된 step은 접미사로 구분(`step1-be.md`, `step1-fe.md`).

#### 공유 파일(shared step에서만 수정 가능)

다음 파일은 BE/FE worktree에서 read-only다. 변경이 필요하면 `agent: shared` step으로 분리해 메인 worktree에서 처리한다.

- `docs/**`
- 루트 `CLAUDE.md`, `.env.example`, `docker-compose.yml`, `.gitignore`
- `scripts/**`, `.claude/**`

### D. 파일 생성

사용자가 승인하면 아래 파일을 생성한다.

#### D-1. `phases/index.json` (전체 현황)

여러 task를 관리하는 top-level 인덱스. 이미 존재하면 `phases` 배열에 새 항목을 추가한다.

```json
{
  "phases": [
    { "dir": "0-mvp", "status": "pending" }
  ]
}
```

- `dir`: task 디렉토리명.
- `status`: `"pending"` | `"completed"` | `"error"` | `"blocked"`. execute.py가 자동 갱신.
- 타임스탬프(`completed_at` 등)는 execute.py가 기록한다.

#### D-2. `phases/{task-name}/index.json` (task 상세)

```json
{
  "project": "gym-check-in",
  "phase": "checkin-api",
  "steps": [
    { "step": 0, "name": "api-contract",  "agent": "shared",   "status": "pending" },
    { "step": 1, "name": "checkin-be",    "agent": "backend",  "status": "pending", "parallel_group": 1 },
    { "step": 2, "name": "checkin-fe",    "agent": "frontend", "status": "pending", "parallel_group": 1 },
    { "step": 3, "name": "checkin-tests", "agent": "both",     "status": "pending" }
  ]
}
```

필드:

- `step`: 0부터 시작하는 순번.
- `name`: kebab-case slug.
- `agent`: `backend` | `frontend` | `both` | `shared`.
- `status`: 초기값 `"pending"`. execute.py가 전이 관리.
- `parallel_group` *(선택)*: 같은 정수 값을 갖는 인접 step들은 worktree에서 동시 실행된다. 일반적으로 BE step과 FE step을 짝지을 때 사용.

상태 전이와 자동 기록 필드 (책임 분리 — 자식 Claude는 phases 인덱스를 만지지 않는다):

| 전이 | 기록 필드 | 기록 주체 |
|------|-----------|-----------|
| → `completed` | `completed_at`, `summary` | execute.py 전부 (acceptance + code-reviewer PASS 후 자동) |
| → `error` | `failed_at`, `error_message` | execute.py 전부 (3회 재시도 후 gate 실패 시) |
| → `blocked` | `blocked_at`, `blocked_reason` | execute.py 전부 (도구 미설치 등) — 메인 세션이 사전에 `blocked`로 두었다면 존중 |

`summary`는 다음 step 프롬프트에 컨텍스트로 누적 전달된다. **step 작성자가 step.md frontmatter `summary:` 필드에 산출물 한 줄을 미리 적어두면 execute.py가 PASS 시점에 main 인덱스에 기록한다.** 자식 Claude는 status·summary를 절대 박지 않는다(자식은 worktree 안에서 commit하지만 main worktree의 phases 인덱스는 그 worktree 브랜치와 독립이라 보이지 않으며, 옛 코드는 max-turns 사고 시 영원히 retry 폭주에 빠졌다).

`created_at`/`started_at`은 execute.py가 자동 기록(생성 시 넣지 말 것).

#### D-3. `phases/{task-name}/step{N}.md` (각 step마다 1개)

step 파일 첫 줄에 frontmatter를 둔다(없으면 `agent: shared`로 처리됨).

```markdown
---
agent: backend
depends_on: [step0]   # 선택: 같은 phase 안의 다른 step name
summary: "<산출물·핵심 결정 한 줄. execute.py가 PASS 시 main 인덱스에 기록>"
---

# Step {N}: {이름}

## 읽어야 할 파일

먼저 아래 파일을 읽고 프로젝트의 아키텍처·계약·이전 산출물을 파악하라:

- `CLAUDE.md`, `backend/CLAUDE.md`, `db/CLAUDE.md` *(agent에 맞게 조정)*
- `docs/API.md`, `docs/ARCHITECTURE.md`, `docs/ADR.md`
- {이전 step에서 생성/수정된 파일 경로}

## 작업

{구체적인 구현 지시. 파일 경로, 함수/타입 시그니처, 트랜잭션·검증 규칙을 포함.
구현체는 에이전트 재량이지만 다음 핵심 규칙은 반드시 박는다:
- 단일 트랜잭션 / SELECT FOR UPDATE / branch_id 강제 / soft delete / PII 마스킹·노출 금지 등
- 외부 SDK 추가 금지(ADR 외)}

## Acceptance Criteria

`agent`에 맞는 명령만 남긴다.

- backend: `cd backend && go build ./... && go test ./...`
- frontend: `cd frontend && pnpm lint && pnpm build && pnpm test`
- DB 변경: `goose -dir db/migrations postgres "$DATABASE_URL" up && down && up`

## 작업 마감 절차 (자식 Claude 책임)

1. 위 AC 명령을 직접 실행해 통과 확인. **commit 전에 모든 테스트가 통과해야 한다.**
2. **변경된 코드를 conventional commit으로 worktree에 commit**한다. 이게 자식의 마지막 산출물이다.
3. **commit 직후 즉시 종료**한다. 다음 행동은 모두 금지:
   - 추가 도구 호출(테스트 재실행, 파일 재읽기, code-review 시뮬레이션, 추가 commit 등) 금지
   - 마무리 요약·보고 메시지 출력 금지
   - **`phases/` 디렉토리는 절대 만지지 않는다** — `index.json` 수정·commit 금지

   부모 execute.py가 자식 종료 직후 acceptance(go vet/build/test -race)와 code-reviewer를 **다시** 돌린다. 자식이 commit 후 무엇을 더 해도 부모 검증이 항상 최종이라 자식의 추가 작업은 100% 폐기물이다. max-turns 도달의 가장 흔한 원인이 commit 이후의 불필요한 마무리 턴이라 이를 명시적으로 차단한다. status·summary·timestamp는 execute.py가 main 인덱스에 직접 박는다(acceptance + code-reviewer gate 통과 후).
4. 사용자 개입이 필요하면(API 키, ADR 갱신 필요 등) **commit하지 말고** stdout에 사유 한 단락만 쓰고 종료. execute.py가 gate 통과 여부로 retry vs error를 판정한다. 이 경로는 "commit 후 즉시 종료"와 별개다 — commit 자체가 발생하지 않으면 마감 절차 3을 거치지 않는다.

## 금지사항

- 다른 worktree의 책임 영역 침범 금지(backend step은 `frontend/` 변경 금지, 그 반대도)
- 공유 파일(`docs/`, `.env.example`, `docker-compose.yml`, 루트 `CLAUDE.md`, `.gitignore`, `scripts/`, `.claude/`) 변경 금지 — shared step이 처리한다
- **`phases/**` 변경·commit 금지** — hook이 차단함. 모든 step 메타는 execute.py가 main에서 박는다.
- 기존 테스트를 깨뜨리지 마라
- ADR 외 라이브러리 추가 금지 — 추가가 정말 필요하면 작업을 멈추고 사용자에게 보고(commit 없이 종료 → execute.py가 retry → 사용자가 수동 개입)
```

`agent: both`인 step은 본문에 `## Backend 작업`과 `## Frontend 작업` 두 섹션을 두고, execute.py가 두 worktree에서 같은 step.md를 동시 실행한다.

### E. 실행

```bash
python3 scripts/execute.py {task-name}              # 실 실행
python3 scripts/execute.py {task-name} --dry-run    # Claude 호출 없이 실행 계획만
python3 scripts/execute.py {task-name} --push       # 완료 후 origin에 push
```

execute.py가 자동으로 처리하는 것:

- **worktree 자동 관리**: `agent: backend`/`frontend`/`both` step이 있으면 `.worktrees/backend`, `.worktrees/frontend`를 main에서 분기해 생성. `feat/{phase}-be`, `feat/{phase}-fe` 브랜치. 생성 시 `.claude/settings.local.json`도 함께 복사해 worktree 안에서도 hook이 동작.
- **병렬 dispatch**: 같은 `parallel_group`을 가진 step들을 동시 실행(BE worktree와 FE worktree에서 별도 `claude` 프로세스).
- **agent별 가드레일 슬림화**: 시스템 프롬프트에 agent 책임 영역에 해당하는 docs만 주입(루트 + 모듈 CLAUDE.md + 관련 docs).
- **컨텍스트 누적**: 완료된 step의 `summary`를 다음 step 프롬프트에 전달.
- **frontmatter 검증**: 실행 시작 시 모든 step.md의 `agent`/`depends_on`/`parallel_group`을 검증. 위반 시 즉시 종료.
- **post-completion gate (책임 분리, B 방안)**: 자식 Claude의 status update에 의존하지 않는다 — 자식이 종료한 시점에 execute.py가 직접 다음을 실행해 status를 결정한다:
  - **acceptance 명령**: agent에 따라 `go build && go test -race`(backend) / `pnpm lint && pnpm build && pnpm test --run`(frontend)을 직접 실행.
  - **code-reviewer 호출**: 별도 `claude -p`로 `/review` 위임 → `PASS` 응답이 와야 통과.
  - 둘 다 PASS면 main 인덱스에 `status: "completed"` + frontmatter `summary` + `completed_at` 자동 기록.
  - 둘 중 하나라도 실패하면 main 인덱스의 status를 `pending`으로 되돌리고 재시도(최대 3회). 도구 미설치(`go`/`pnpm`/`claude`)는 즉시 `blocked`.
  - 자식이 max-turns에 도달해 status를 못 박아도 무관 — 코드 commit이 살아 있고 acceptance가 통과하면 PASS.
- **자가 교정**: 실패 시 최대 3회 재시도. 이전 에러 메시지를 프롬프트에 피드백.
- **2단계 커밋**: 코드 변경(`feat`)과 메타데이터(`chore`) 분리.
- **merge-back**: phase 완료 시 BE→FE 순으로 main에 merge. 충돌 발생 시 abort + `blocked`.
- **dirty tree 가드**: 메인 working tree가 dirty면 즉시 종료(stash/commit 후 재실행).
- **타임스탬프**: `started_at`, `completed_at`, `failed_at`, `blocked_at` KST 자동 기록.

#### 자동 가드레일(`.claude/settings.json` hook 시스템)

자율 실행 모드에서 Claude가 정책을 무시해도 hook이 도구 호출을 차단한다. 모든 hook은 `scripts/hooks/`에 있고 단위 테스트가 `scripts/test_*.py`에 있다.

| Hook | Trigger | 역할 |
|------|---------|------|
| `precheck_bash.py` | PreToolUse / Bash | 위험 명령 차단 — `rm -rf /etc`, `git push --force`, `--no-verify`, `git --amend`, `goose reset`, `docker compose down -v`, `> .env`, `pnpm add` / `npm install` / `go get`(ADR 외 라이브러리), `psql ... DROP/TRUNCATE` 등 |
| `precheck_path.py` | PreToolUse / Edit·Write | worktree·shared 경계 강제. `.worktrees/backend/`는 `backend/`+`db/`만, `.worktrees/frontend/`는 `frontend/`만, 메인(shared)은 `docs/`+`scripts/`+`.claude/`+모듈 `CLAUDE.md`+`phases/`+루트 설정만 수정 가능. **자식 Claude(backend/frontend agent)는 `phases/**` 전면 차단** — step 메타는 execute.py가 main에서 박는 책임 분리(B 방안)라 자식이 만질 일이 없다. 그 외 차단 |
| `postcheck_diff.py` | PostToolUse / Edit·Write | 변경된 파일에 PII(휴대폰 번호 하드코딩)·시크릿(JWT/bcrypt 평문)·PII 로깅(`slog ... phone`)·ADR 외 import(axios/firebase/logrus 등) 검출 |
| `postcheck_tdd.py` | PostToolUse / Edit·Write | backend `internal/{http,domain,repo,auth,batch}` 파일 변경에 대응하는 `_test.go`가 없으면 차단(TDD 강제). `cmd/`·`testutil/`·`apperr/`·`config/`는 면제 |
| `session_start.py` | SessionStart | 새 세션 시작 시 git branch·worktree 위치·phases 진행 상황을 자동 주입 |
| `statusline.py` | statusLine | 항상 보이는 한 줄(`phase:<dir> step:<N>/<total> agent:<be|fe|sh> branch:<...>`) |
| `stop_summary.py` | Stop | 세션 종료 시 `git status --short` + `git diff --stat HEAD` 출력 |

차단(exit 2) 시 stderr에 `BLOCKED: <카테고리> <사유>`가 출력되고 Claude가 그것을 도구 결과로 받아 자가 교정한다. 만약 의도적으로 정책을 갱신해야 하면 `scripts/hooks/`의 정규식·매트릭스를 직접 수정하고 `scripts/test_*.py` 테스트를 같이 갱신한다(shared step에서).

에러 복구:

- **error 발생 시**: `phases/{task-name}/index.json`에서 해당 step의 `status`를 `"pending"`으로, `error_message`를 삭제 후 재실행.
- **blocked 발생 시**: `blocked_reason`을 해소한 뒤 `status`를 `"pending"`으로, `blocked_reason`을 삭제 후 재실행.

### F. 정리

phase가 모두 완료되면:

1. `phases/{task-name}/` 디렉토리는 보존(이력 추적용).
2. `.worktrees/{backend,frontend}` 디렉토리는 사용자가 명시적으로 정리(`git worktree remove`)할 때까지 유지한다. 다음 phase에서 재사용해 `pnpm install`/`go mod download` 비용을 줄인다.
3. PR/배포는 ROADMAP의 Phase별 검증 기준(`docs/ROADMAP.md`)을 통과한 뒤에 진행한다.
