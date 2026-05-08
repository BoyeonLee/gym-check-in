새 step.md 파일과 phases/<phase>/index.json 항목을 생성하라.

## 입력

사용자가 제공해야 할 인자(없으면 묻는다):
- 대상 phase 디렉토리(예: `0-mvp`, `2-checkin-api`)
- step 이름(kebab-case 권장: `checkin-be`, `members-search`)
- agent: `backend` | `frontend` | `both` | `shared`
- (선택) `parallel_group`: 정수 — 같은 그룹은 worktree 동시 실행
- (선택) `depends_on`: 같은 phase 안의 다른 step name 배열

## 절차

1. `phases/<phase>/index.json`을 읽어 현재 step 번호의 다음 값(N)을 결정.
2. `phases/<phase>/stepN.md`를 생성. 템플릿:

```markdown
---
agent: <agent>
# 선택 필드 — 필요 시 주석 해제
# parallel_group: 1
# depends_on: [<other-step-name>]
---

# Step N: <step-name>

## 읽어야 할 파일

먼저 아래 파일을 읽고 프로젝트의 아키텍처·계약·이전 산출물을 파악하라:

- `CLAUDE.md`(루트), `<agent>/CLAUDE.md`
- `docs/API.md`, `docs/ARCHITECTURE.md`, `docs/ADR.md`
- (이전 step에서 생성/수정된 파일이 있으면 경로 추가)

## 작업

(여기에 구체적 구현 지시. 파일 경로, 함수/타입 시그니처, 트랜잭션·검증 규칙 포함)

핵심 규칙:
- 단일 트랜잭션 / SELECT FOR UPDATE / branch_id 강제 / soft delete / PII 마스킹·노출 금지
- 외부 SDK 추가 금지(ADR 외)

## Acceptance Criteria

- backend: `cd backend && go build ./... && go test -race ./...`
- frontend: `cd frontend && pnpm lint && pnpm build && pnpm test --run`
- DB 변경: `goose -dir db/migrations postgres "$DATABASE_URL" up && down && up`

## 작업 마감 절차 (B 방안 — 책임 분리)

1. 위 AC 명령을 직접 실행해 통과 확인. **commit 전에 모든 테스트가 통과해야 한다.**
2. 변경된 코드를 conventional commit으로 worktree에 commit한다. 이게 자식의 마지막 산출물이다.
3. **commit 직후 즉시 종료**한다. 다음 행동은 모두 금지:
   - 추가 도구 호출(테스트 재실행, 파일 재읽기, code-review 시뮬레이션, 추가 commit 등) 금지
   - 마무리 요약·보고 메시지 출력 금지
   - `phases/` 디렉토리는 절대 만지지 마라 — `index.json` 수정·commit 금지

   부모 execute.py가 자식 종료 직후 acceptance와 code-reviewer를 **다시** 돌린다. 자식이 commit 후 무엇을 더 해도 부모 검증이 최종이라 추가 작업은 100% 폐기물이다. max-turns 도달의 가장 흔한 원인이 commit 이후의 불필요한 마무리 턴이라 이를 명시적으로 차단한다. status·summary·timestamp는 execute.py가 main 인덱스에 박는다(acceptance + code-reviewer gate 통과 후).
4. 사용자 개입이 필요한 상황(ADR 갱신, 도구 미설치 등)이면 **commit하지 말고** stdout에 사유 한 단락만 쓰고 종료. execute.py가 retry/error/blocked로 판정한다. 이 경로는 "commit 후 즉시 종료"와 별개다 — commit 자체가 발생하지 않으면 마감 절차 3을 거치지 않는다.

## 금지사항

- 다른 worktree 영역 침범 금지(precheck_path hook이 차단)
- 공유 파일 변경 금지(shared step만 가능)
- `phases/**` 변경·commit 금지 — hook이 차단함. 모든 step 메타는 execute.py가 main에서 박는다.
- ADR 외 라이브러리 추가 금지(precheck_bash hook이 차단)
- 기존 테스트를 깨뜨리지 마라
```

3. `phases/<phase>/index.json`의 `steps` 배열에 새 항목 추가:

```json
{ "step": N, "name": "<step-name>", "agent": "<agent>", "status": "pending" }
```

(parallel_group/depends_on이 지정됐으면 같이 포함)

4. 사용자에게 생성된 파일 경로와 다음 작업 안내(step.md 본문 채우기).

## 검증

- frontmatter의 agent가 `backend|frontend|both|shared` 중 하나인지.
- depends_on이 같은 phase 안에 실제로 존재하는 step name인지.
- parallel_group이 같은 그룹이라면 agent가 서로 달라야 함(같은 worktree 동시 사용 방지).
- 모두 통과 시 사용자에게 "step{N}.md를 채우세요" 안내.
