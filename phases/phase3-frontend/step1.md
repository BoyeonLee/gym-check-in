---
agent: shared
---

# Step 1: 공유 사전 작업 (ROADMAP·ADR 갱신, ui-design 참조 규칙)

## 목표

Phase 3의 화면 구현·자동 테스트가 docs 정합성 위에서 진행되도록 **`docs/ROADMAP.md`와 `docs/ADR.md`를 먼저 갱신**한다. 또한 `frontend/ui-design/` 시안을 어떻게 참조할지 규칙을 박는다. 이 step에서는 코드 변경은 없다 — 오직 문서.

이 step이 끝나면:
- ROADMAP의 Phase 3 정의가 "스캐폴드 + 빈 컴포넌트"에서 "스캐폴드 + 화면 구현 + Vitest 단위 + Playwright e2e"로 확장돼 있다.
- ADR-008(또는 신규 ADR-011) 라이브러리 화이트리스트에 Phase 3에서 새로 도입할 도구가 모두 기재돼 있다.
- ui-design 참조 규칙이 frontend/CLAUDE.md(또는 ROADMAP)에 명시돼 있어 이후 step의 frontend agent가 시안을 비교 기준으로 사용할 수 있다.

## 읽어야 할 파일

- `docs/ROADMAP.md` — Phase 3 §"산출물"·"작업 항목" (line 224~254)
- `docs/ADR.md` — 특히 ADR-005(PWA), ADR-008(라이브러리 스택). 신규 ADR 번호 결정용.
- `frontend/CLAUDE.md` — 디렉토리·라우팅·규칙. 이 step에선 새 규칙 1개만 추가(`ui-design` 참조 규칙).
- `frontend/ui-design/README.md`, `frontend/ui-design/styles.css` — 시안 토큰 이해.
- `CLAUDE.md` (루트) — 공통 CRITICAL 규칙.

## 작업

### 1. `docs/ADR.md` — Phase 3 라이브러리 화이트리스트 확장

기존 ADR-008(또는 그에 상응하는 스택 결정 ADR)에 다음 라이브러리가 **명시적으로 허용 목록에 등재**되도록 항목을 추가/갱신한다. 이미 일부가 있다면 누락된 것만 추가.

**Runtime 의존성**:
- `react@18`, `react-dom@18`
- `react-router-dom@6` (라우팅)
- `@tanstack/react-query@5` (서버 상태)
- `vite-plugin-pwa@0.x` (PWA manifest + service worker)
- `clsx` 또는 `class-variance-authority` (조건부 className. 중복 도입 금지 — 둘 중 하나 선택)

**Dev 의존성**:
- `vite@5`, `@vitejs/plugin-react@4`
- `typescript@5` (strict)
- `tailwindcss@3`, `postcss`, `autoprefixer`
- `vitest@1`, `@testing-library/react@14`, `@testing-library/jest-dom`, `@testing-library/user-event`
- `msw@2` (API mock — 컴포넌트/훅 테스트용)
- `@playwright/test@1.x` (e2e)
- `eslint`, `@typescript-eslint/parser`, `@typescript-eslint/eslint-plugin`, `eslint-plugin-react-hooks`
- `jsdom` (Vitest DOM 환경)

**금지 라이브러리** (명시):
- 외부 STT SDK (Whisper API SDK, Google Cloud Speech 등) — `window.SpeechRecognition`만 사용 (ADR-002 음성 인식 결정 재확인).
- UI 라이브러리 (MUI, Chakra, shadcn 등) — 시안이 픽셀 단위로 정해져 있으므로 Tailwind + 자체 컴포넌트로 구현.
- 상태 관리 라이브러리 (Redux, Zustand, Jotai 등) — TanStack Query + React Context로 충분.
- `dayjs`/`moment`/`date-fns` — KST 표시는 백엔드가 `+09:00`으로 직렬화하므로 `Intl.DateTimeFormat`과 `Date`만으로 처리. 필요하면 다음 phase에서 재논의.

ADR 작성은 기존 ADR과 같은 톤(결정 / 배경 / 대안 / 결과)으로 추가.

### 2. `docs/ROADMAP.md` — Phase 3 정의 확장

기존 Phase 3 섹션(line 224~)의 본래 정의는 "스캐폴드 + 빈 컴포넌트 + 라우팅"이었다. 이를 다음으로 확장한다(기존 항목은 유지하고 누락분 추가).

**§"산출물"에 추가**:
- 화면 구현 완성형 (키오스크 7화면 + 관리자 전 화면). 빈 컴포넌트 X.
- `frontend/ui-design/` 시안과 픽셀 단위로 정합(허용 오차 명시: 색상은 토큰 일치, 간격은 ±4px 이하).
- Vitest 단위/컴포넌트 테스트 셋업 + 핵심 훅·API 래퍼·마스킹·폼 검증 커버.
- Playwright e2e 셋업 + 골든 패스 시나리오 12~15개. CI 빌드에 포함.
- `frontend/ui-design/assets/`의 사용자 제공 PWA 아이콘을 manifest에 연결.

**§"작업 항목"에 추가** (체크박스):
- [ ] 관리자 화면 전체 구현 (회원·회원권·체크인·매출·대량연장·지점·관리자)
- [ ] 회원권 부여/정지/조기활성화/취소/환불 폼 + Idempotency-Key 처리
- [ ] 매출 페이지 gross/refund/net 분리 표시 (전역 전용)
- [ ] BulkExtend 폼 (전역 전용, days 1~90 정수)
- [ ] 관리자 CRUD + 비번 리셋 (임시비번 1회 표시·복사)
- [ ] 회원 상세 한 화면 (active 회원권 + 회원권 이력 + 결제 이력)
- [ ] 회원권 상세 페이지 (events + payments)
- [ ] Vitest + MSW 셋업 + 단위/컴포넌트 테스트
- [ ] Playwright 셋업 + e2e 골든 패스 시나리오
- [ ] PWA 아이콘 (사용자 제공) manifest 연결

§"검증 기준"이 있으면 다음을 추가(없으면 신설):
- `pnpm build`가 통과한다.
- `pnpm test` (Vitest)가 모두 통과한다.
- `pnpm test:e2e` (Playwright)가 모두 통과한다.
- 브라우저에서 키오스크/관리자 골든 패스를 수동으로도 한 번 클릭해본다.

### 3. `frontend/CLAUDE.md` — ui-design 참조 규칙 추가

기존 "디자인 참조" 섹션이 한 줄("`docs/UI_GUIDE.md`의 색상·컴포넌트·타이포 토큰을 그대로 Tailwind 클래스/테마로 사용한다.")로 짧다. 이를 다음과 같이 확장:

- 색상·간격·타이포 토큰: `docs/UI_GUIDE.md` + `frontend/ui-design/styles.css`의 CSS 변수를 정본으로 한다. step 2 scaffold에서 `frontend/ui-design/styles.css`를 `frontend/src/styles/tokens.css`로 그대로 복사(주석 포함). 토큰은 직접 수정하지 않고, Tailwind config가 이 변수들을 참조한다.
- 화면 레이아웃: `frontend/ui-design/*.jsx`(kiosk-screens-1.jsx, kiosk-screens-2.jsx, admin-shell.jsx, admin-members.jsx, admin-plan-grant.jsx, admin-sales-login.jsx)는 **픽셀 단위 시안**이다. JSX 구조·className·간격·아이콘 위치를 그대로 따른다. 시안과 달라야 할 정당한 이유가 있을 때만(예: 반응형 모바일 분기, 접근성 보강) 변경하고 step.md 또는 PR 본문에 사유를 적는다.
- 시안에 없는 화면(예: PasswordChange의 인라인 강도 가이드 텍스트 색상, 토스트 등)은 같은 토큰을 사용해 시안과 시각적으로 일관되게 만든다.
- ui-design 폴더는 **참조 전용**. frontend 구현에서 ui-design 내부 파일을 import 하지 않는다(복사된 tokens.css를 통해서만 토큰 사용).

### 4. 신규 ADR-011 검토 (선택)

ADR-008이 백엔드 스택 결정이라면, 프론트엔드 스택을 별도 ADR(ADR-011 "Frontend 라이브러리 화이트리스트")로 분리하는 게 깔끔할 수 있다. 기존 ADR.md 톤에 따라 결정:
- ADR-008이 "전체 스택"이라면 거기에 항목 추가
- ADR-008이 백엔드 한정이라면 ADR-011 신설

ADR.md 마지막 ADR 번호를 확인하고 일관되게 처리한다.

### 5. (참고) ROADMAP의 Phase 4·Phase 5에 영향 없음

Phase 3 확장이 Phase 4(배포)·Phase 5(부가 기능) 작업을 침범하지 않는지 확인. Playwright e2e는 Phase 3에서 셋업하고, CI 통합은 Phase 4에서. 만약 ROADMAP에 명시적 Phase 4 항목으로 "CI 통합"이 있으면 Playwright 빌드 step만 추가하라는 비고를 단다.

## 핵심 규칙

- **이 step은 shared이므로 `backend/`·`frontend/`·`db/` 폴더는 일체 수정하지 않는다**(hook이 차단). 변경은 `docs/`와 `frontend/CLAUDE.md`(공유 규칙 문서) 한정.
- ADR 추가 시 기존 ADR 번호 충돌 금지 — 마지막 ADR 번호 +1.
- ROADMAP 체크박스는 새로 추가하는 항목만 `[ ]` 미완료로 둔다 (이미 완료된 항목 체크 해제 금지).
- 시안 참조 규칙은 "복사된 tokens.css만 사용"임을 명확히 — ui-design 폴더를 src에서 import 금지.

## Acceptance Criteria

1. `docs/ADR.md`에 Phase 3 라이브러리 화이트리스트(react, react-router-dom, @tanstack/react-query, vite-plugin-pwa, vitest, msw, @playwright/test, tailwindcss 등)가 명시돼 있다.
2. `docs/ADR.md`에 금지 라이브러리 목록(외부 STT, UI 라이브러리, 상태 관리 라이브러리, 날짜 라이브러리)도 함께 있다.
3. `docs/ROADMAP.md` Phase 3 §"산출물"·"작업 항목"에 화면 구현·Vitest·Playwright·PWA 아이콘 항목이 추가돼 있다.
4. `frontend/CLAUDE.md`의 "디자인 참조" 섹션이 ui-design 폴더 참조 규칙으로 확장돼 있다 (복사된 tokens.css만 사용, JSX 시안은 픽셀 단위 정본, ui-design 직접 import 금지).
5. `git diff --stat`이 `docs/ADR.md`, `docs/ROADMAP.md`, `frontend/CLAUDE.md`만 변경된 것으로 나온다. `backend/`·`frontend/src/`·`db/`·`scripts/`·`.claude/`는 0 변경.

## 검증 절차

1. AC 1~5를 만족하는지 직접 확인.
2. `git diff` 검토 — 의도하지 않은 파일이 끼지 않았는지.
3. `code-reviewer` 서브에이전트 호출 (선택, 분량 적으면 skip 가능). 입력: step 이름(`phase3-frontend/shared-frontend-prep`), `git diff HEAD`.
4. PASS 시 `phases/phase3-frontend/index.json`의 step1 status를 `completed` + `summary` 갱신.

## 금지사항

- `backend/`, `frontend/src/`, `frontend/package.json`, `frontend/public/`, `db/`, `scripts/`, `.claude/`, 루트 `CLAUDE.md`(루트는 별도 ADR 절차) 변경 금지.
- 기존 ADR의 결정을 뒤집는 변경 금지 — 이 step은 추가만. 결정 변경은 새 ADR로.
- ROADMAP의 다른 Phase(1, 2, 4, 5) 항목 수정 금지.
- 시안의 픽셀 단위 디자인을 임의로 바꾸는 규칙 추가 금지 — 시안 정본 원칙 유지.
