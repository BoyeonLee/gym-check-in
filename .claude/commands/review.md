현재 브랜치(또는 worktree)의 변경 사항을 `code-reviewer` 서브에이전트에 위임해 검증하라.

## 위임 절차

1. 변경 범위 파악: `git status --short`, `git diff HEAD --stat`로 어떤 파일들이 바뀌었는지 사용자에게 한 줄로 알린다.
2. `code-reviewer` 에이전트를 Task tool로 호출해 다음을 위임:
   - 입력 컨텍스트: 작업 중인 phase·step 이름(있다면), 변경 파일 목록
   - 기준 문서: `CLAUDE.md`, `backend/CLAUDE.md`, `frontend/CLAUDE.md`, `db/CLAUDE.md`, `docs/{ARCHITECTURE,ADR,API}.md`
   - 요구 출력: `PASS` 또는 `BLOCK` + 구체적 위반 항목(파일:라인 + 카테고리)
3. 에이전트의 응답을 받아 사용자에게 그대로 전달.
   - `PASS`면 추가 작업 없이 종료.
   - `BLOCK`이면 위반 항목별로 수정 방안을 사용자와 논의(코드 수정은 사용자 승인 후).

## 사용 시점

- step 완료 직전 자가 검증
- worktree를 메인으로 merge하기 전
- PR 생성 직전
- 사용자가 `/review`를 명시적으로 호출했을 때

코드는 직접 수정하지 않는다. 위반 사항을 보고하고, 수정 여부·방법은 사용자 또는 backend-engineer/frontend-engineer에게 위임한다.
