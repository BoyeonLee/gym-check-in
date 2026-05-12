# Frontend — 체육관 체크인 UI

## 기술 스택
- Vite + React 18
- TypeScript strict mode
- Tailwind CSS
- React Router (라우트 분할)
- TanStack Query (서버 상태)
- 브라우저 Web Speech API (음성 인식, 외부 SDK 금지)

## 디렉토리 구조
```
frontend/
├── src/
│   ├── pages/
│   │   ├── kiosk/       # 회원용 태블릿 화면 (/)
│   │   │   ├── BranchSetup.tsx   # 지점 선택(관리자 인증 후)
│   │   │   ├── Idle.tsx          # 대기 화면 — 오늘 체크인 인원 수 표시
│   │   │   ├── InputSelect.tsx   # 음성/타이핑 선택
│   │   │   ├── VoiceSearch.tsx   # 음성 인식 (3회 실패 시 타이핑 전환)
│   │   │   ├── TypingSearch.tsx  # 이름 / 전화 뒷자리 4자리 / 회원 번호 3개 탭 분기
│   │   │   ├── MemberPick.tsx    # 동명이인 2명+일 때만 표시, 최근 체크인 순 정렬, 식별자는 마스킹
│   │   │   └── CheckInDone.tsx   # 완료 화면(타임아웃 후 Idle 복귀)
│   │   └── admin/       # 관리자 반응형 웹앱 (/admin/*)
│   │       ├── Login.tsx
│   │       ├── PasswordChange.tsx # 현재 비번 + 새 비번 — 최초 강제 변경/상시 변경 공용
│   │       ├── Members/           # 지점·전역 모두 가능 (지점 관리자=자기 지점만)
│   │       ├── Memberships/       # 부여(+ 결제 입력)·정지·조기 활성화(unpause)·미래 정지 취소(cancel-pause)·환불
│   │       ├── BulkExtend.tsx     # 전역 관리자 전용 대량 연장
│   │       ├── CheckIns/
│   │       ├── Sales/             # 전역 관리자 전용 일/월 매출 조회
│   │       ├── Admins/            # 전역 관리자 전용 — 관리자 계정 CRUD(생성·정보 수정[username/role/branch_id]·soft delete) + 비번 리셋
│   │       └── Branches/          # 전역 관리자 전용
│   ├── components/      # 공통 UI (Button, Card, Table, NumberPad 등)
│   ├── api/             # fetch 래퍼 (Gin API 호출, access JWT 헤더 자동, 401 시 refresh → 재시도)
│   ├── hooks/           # useBranch, useAuth, useSpeechRecognition, useIdleTimeout, useIdempotencyKey
│   ├── context/         # BranchContext, AuthContext
│   ├── types/           # API 응답·도메인 타입
│   └── styles/
├── index.html
├── public/
│   └── manifest.webmanifest   # PWA: display=fullscreen, 키오스크 풀스크린용
├── vite.config.ts
├── tailwind.config.ts
└── package.json
```

## 라우팅
- `/` — 키오스크 진입점. `localStorage.branchId`가 없으면 `/kiosk/setup`으로 리다이렉트.
- `/kiosk/*` — 지점 선택·대기·입력·검색·완료 하위 단계.
- `/admin/login` — 관리자 로그인.
- `/admin/*` — 보호 라우트. 미인증 시 로그인으로, `must_change_password`면 `/admin/password` 강제.

## 명령어
```
pnpm install    # 의존성 설치
pnpm dev        # 개발 서버 (Vite)
pnpm build      # 프로덕션 정적 빌드 → dist/
pnpm preview    # 빌드 결과 미리보기
pnpm lint       # ESLint
pnpm test       # Vitest
```

API 엔드포인트는 환경변수 `VITE_API_URL`로 주입. 개발은 `http://localhost:8080`, 운영은 `https://...`(HTTPS 강제).

API 명세는 `docs/API.md` 참고.

## 규칙
- **CRITICAL**: 모든 데이터 접근은 Gin API만 경유. `fetch`/axios를 외부 서비스·DB로 직접 보내지 않는다.
- **CRITICAL**: 음성 인식은 브라우저 Web Speech API(`window.SpeechRecognition`)만 사용. 외부 STT 서비스 SDK 도입 금지.
- **CRITICAL**: 회원 개인정보(전화번호, 생년월일)는 화면에 꼭 필요한 순간에만 표시, 로그·에러 메시지로 유출 금지.
- 키오스크 화면은 `touch-action: manipulation`, 최소 터치 타겟 64px(세부 값은 `docs/UI_GUIDE.md`).
- 키오스크는 풀스크린 기본, 오른쪽 위 숨김 제스처(5초 롱프레스)로 지점 재설정 화면 진입.
- 키오스크 풀스크린은 PWA(`manifest.webmanifest`, `display: fullscreen`) + 태블릿 홈 화면 추가로 달성한다(ADR-005). Fullscreen API는 보조용.
- 키오스크 대기·진행 화면 헤더에 **오늘 해당 지점 체크인 인원 수**를 표시. `GET /api/check-ins/today-count`를 React Query로 조회하고, 자체 체크인 성공 시 invalidate.
- `TypingSearch`는 "이름" / "전화 뒷자리 4자리" / "회원 번호" 세 탭.
  - 이름: prefix 일치, 최소 2자 이상 입력 시 검색 활성. API 호출 시 `mode=name`.
  - 전화 뒷자리 4자리: 4자리 숫자패드 입력 즉시 검색, 4자리 미만이면 비활성. `mode=phone`.
  - 회원 번호: 숫자패드 입력 후 확인 버튼. `mode=memberId`. 동명이인 식별이 어려운 경우의 정확 매칭용.
- `MemberPick`은 검색 결과가 2명 이상일 때만 진입(1명이면 자동으로 다음 단계). 결과는 **최근 체크인 순**으로 정렬된 채 서버에서 내려온다(클라 재정렬 금지). 식별 보조 정보는 마스킹된 형태로만 노출한다 — 회원 번호는 서버가 내려준 `member_id_display`(`#1234`)를 그대로 표시, 전화 가운데 마스킹(`010-****-1234`), 생년월일은 월·일만(`**-04-15`). 풀 전화·생년월일은 절대 표시하지 않는다.
- 키오스크 검색 결과는 백엔드가 **활성 회원권이 있는 회원만** 반환하므로, 프론트는 결과가 0건이면 "활성 회원권이 없거나 회원이 등록되어 있지 않습니다" 안내 후 Idle 복귀.
- 체크인 시 백엔드가 422 `MEMBERSHIP_NOT_STARTED`를 반환하면(미래 시작 회원권만 보유), "회원권 시작일이 아직 되지 않았습니다 (시작일: YYYY-MM-DD)" 안내 후 Idle 복귀. (정상적으로는 search 결과에서 제외되지만 race 보호용 분기)
- 키오스크 검색 응답에 `truncated: true`가 오면(서버가 limit 20으로 잘랐다는 뜻) MemberPick 상단에 "결과가 너무 많습니다. 회원 번호 또는 전화 4자리로 검색해주세요" 안내 배너 + 결과 20명까지 그대로 렌더.
- BulkExtend 폼은 `days` 입력을 **1~90 정수만 허용**(범위 밖 제출 비활성). 백엔드 400 `INVALID_EXTEND_DAYS`는 fallback. 응답 409 `MEMBERSHIP_PERIOD_OVERLAP`은 "연장 결과가 일부 회원의 미래 회원권과 겹칩니다. 미래 회원권의 시작일을 조정한 후 재시도하세요" 안내(전체 롤백되었음을 명시).
- 관리자 UI는 **반응형**. 모바일 레이아웃에서 테이블은 카드 스택으로 대체.
- 상태 지속: 태블릿 `branchId`·관리자 access/refresh JWT는 `localStorage`. 로그아웃 시 둘 다 제거 + `POST /api/admin/logout` 호출(refresh 토큰 서버측 무효화).
- **토큰 갱신**: API fetch 래퍼는 응답 401(만료) 시 자동으로 `POST /api/admin/refresh` 호출 → 새 access 토큰으로 원 요청 재시도. refresh 자체가 401이면 강제 로그아웃 후 `/admin/login`으로 리다이렉트. access 만료 30분 / refresh 만료 15시간.
- **Idempotency-Key**: 다음 폼은 모두 `useIdempotencyKey` 훅으로 마운트 시 `crypto.randomUUID()`로 키를 생성해 state로 보관, 제출 시 `Idempotency-Key` 헤더로 보낸다. 폼 초기화(성공 응답 후)에서만 새 키 발급. 추가로 confirm 모달 + 처리 중 버튼 비활성화로 사용자 실수 1차 차단.
  - `Memberships/` 회원권 부여 폼 (`POST /api/members/:id/memberships`)
  - `Memberships/` 환불 폼 (`POST /api/memberships/:id/refund`)
  - `BulkExtend.tsx` (`POST /api/memberships/bulk-extend`)
- 음성 인식 실패 카운터는 컴포넌트 상태로 관리, 3회 도달 시 자동으로 `TypingSearch`로 전환 + 안내 토스트.
- **음성 인식 가용성 체크**: `useSpeechRecognition` 훅 마운트 시 `'webkitSpeechRecognition' in window || 'SpeechRecognition' in window`를 검사 → 미지원이면 `InputSelect`에서 음성 버튼을 비활성/숨김. iOS는 모든 브라우저(iPad Chrome 포함)가 미지원 처리됨. 마이크 권한 거부(`NotAllowedError`) 시 즉시 `TypingSearch`로 전환 + 안내 토스트.
- **키오스크 idle 타임아웃**: `InputSelect/VoiceSearch/TypingSearch/MemberPick` 화면에 진입 후 10초 동안 입력·터치·음성 결과가 없으면 `Idle`로 자동 복귀. 타이머는 매 사용자 이벤트마다 reset. 공통 훅(`useIdleTimeout(10000)`) 권장.
- **관리자 화면은 풀 PII 표시**: 관리자 회원 목록·상세·체크인 이력 테이블에서는 전화번호(`010-1234-5678` 표기)·생년월일을 마스킹 없이 그대로 노출한다. 마스킹은 키오스크 `MemberPick`에 한정.
- **전화번호 입력**: 회원 등록·수정 폼은 숫자 11자리(`01012345678`)만 받는다. 입력 마스크/숫자 키패드, 11자리 미만 시 제출 비활성. 표시할 때는 `010-1234-5678`로 포맷.
- 회원권 부여 폼은 결제 입력(금액·수단[현금/카드])을 같은 폼에 포함. 제출은 `POST /api/members/:id/memberships` 단일 호출로. **`useIdempotencyKey` 훅으로 폼 마운트 시 UUID 발급, `Idempotency-Key` 헤더에 첨부, 성공 후 새 키 발급**(이중 클릭 결제 row 중복 방지).
  - `monthly` 선택 시 추가 입력: `months`(개월 수, 1 이상). 폼은 `1/3/6/12` 프리셋 + 직접 입력. 만료일은 `start_date + months month`로 자동 계산해 미리보기 표시.
  - `pass10` 선택 시 추가 입력 없음(자동으로 `start_date + 2 month`, `remaining=10`). 만료일 미리보기 표시.
  - `start_date`는 **오늘 또는 미래만 선택 가능**(과거 날짜 비활성화). 백엔드가 거부 시 400 `INVALID_START_DATE`를 받아 토스트 안내.
  - **다음 회원권 미리 등록 허용**: 회원이 active/paused 회원권을 보유 중이어도 기간이 겹치지 않으면 등록 가능. 폼 상단에 현재 회원권의 `end_date`를 보여주고 새 회원권 `start_date`의 기본값을 `end_date + 1일`로 제안. 백엔드가 409 `MEMBERSHIP_PERIOD_OVERLAP` 응답 시 "기간이 기존 회원권과 겹칩니다. 시작일을 조정하세요" 안내.
  - **결제일(`paid_at`)은 폼에 두지 않는다** — 서버가 자동 설정(회원권 등록일 = 결제일). 사전 결제로 `start_date`가 미래여도 `paid_at`은 오늘.
  - 결제 금액(`amount`)은 양수 정수(원 단위). `0`/공백은 제출 비활성. 무료/0원 결제 미지원.
- 정지 화면: 시작일·종료일·사유 입력. 한 회원권당 1회만 가능하므로 이미 정지 이력이 있으면(`pause_used=true`) 폼 자체 비활성 + 안내(단, 미래 예약 정지 상태에서는 아래 cancel-pause 버튼이 노출되어 취소 후 재등록 가능). 시작일이 미래여도 즉시 등록 가능(자정 배치가 도래일에 paused로 전환). 시작일이 오늘이면 즉시 paused.
- 정지 조기 활성화(unpause): `Memberships` 상세에서 **정지가 도달해 적용 중인 상태**(`status='paused'`)일 때만 노출. confirm 모달("오늘 날짜로 활성화하면 만료일이 N일 앞당겨집니다") + 사유 입력 후 `POST /api/memberships/:id/unpause`.
- 미래 예약 정지 취소(cancel-pause): `Memberships` 상세에서 **`status='active'` + `pause_used=true` + `pause_start_date > 오늘`**(아직 도달하지 않은 예약 정지)일 때만 노출. confirm 모달("예약된 정지를 취소하면 만료일이 원래 날짜로 되돌아가고 다시 정지를 등록할 수 있습니다") + 사유 입력 후 `POST /api/memberships/:id/cancel-pause`. 응답 후 `pause_used=false`로 폼이 다시 활성화된다.
- 환불 폼: **`useIdempotencyKey` 훅으로 `Idempotency-Key` 헤더 첨부**(이중 클릭으로 음수 결제 row 중복 방지). confirm 모달 + 사유 입력 후 `POST /api/memberships/:id/refund`. 폼 입력은 **`reason`만** — 환불 row의 `paid_at`/`method`/`amount`/`branch_id`는 모두 서버가 자동 채운다(원본 결제 row의 method·amount 부호 반전). confirm 모달에서는 이 자동 값들을 미리보기로 보여준다(예: "원본 결제 150,000원 카드 → 환불 -150,000원 카드, 처리일 오늘"). 응답 409 `MEMBERSHIP_ALREADY_EXPIRED` 시 "만료된 회원권은 환불할 수 없습니다" 토스트. 환불 가능 status는 active/paused/active+미래시작.
- `PasswordChange.tsx`는 항상 "현재 비밀번호 + 새 비밀번호 + 새 비밀번호 확인" 3필드. 최초 강제 변경 모드/상시 변경 모드 둘 다 같은 컴포넌트로 처리, 모드는 `must_change_password` 플래그로 분기(상단 안내 문구만 다름). 새 비밀번호는 클라이언트에서도 강도 검증(8자 이상 + 영문 1자 이상 + 숫자 1자 이상) — 미충족이면 제출 비활성 + 인라인 가이드. 백엔드 400 `WEAK_PASSWORD`는 토스트로 fallback.
- **비번 변경 성공 후 자동 로그아웃**: `POST /api/admin/password` 204 응답을 받으면 클라이언트는 access/refresh 토큰을 모두 폐기하고 `/admin/login`으로 리다이렉트(서버측 refresh는 이미 무효화됨, access는 다음 요청에서 미들웨어가 `iat < password_updated_at`로 401). "비밀번호가 변경되었습니다. 새 비밀번호로 다시 로그인하세요" 안내.
- 지점 관리자 비번 리셋(`POST /api/admins/:id/reset-password`)의 응답으로 받은 임시 비밀번호는 화면에 1회 표시(복사 버튼 포함) + 토스트로만 노출, `localStorage`·콘솔·로그·에러 메시지 어디에도 남기지 않는다. 응답의 `expires_at`(발급 후 24시간)을 함께 표시해 "이 비밀번호는 X시 Y분까지 유효합니다" 안내.
- 로그인 응답이 `TEMP_PASSWORD_EXPIRED`이면 폼에 "임시 비밀번호가 만료되었습니다. 전역 관리자에게 재발급을 요청하세요" 안내 + 비번 입력 비활성. 잠금/만료 안내는 별개 분기.
- `Admins/` 페이지: 목록·생성·삭제(soft delete)·비번 리셋 + **수정**(`PATCH /api/admins/:id` — username·role·branch_id 변경 가능, 비번/잠금 컬럼은 변경 불가). role을 `branch`↔`global`로 토글 시 branch_id 필드 활성/비활성 자동 전환. 본인 행에서는 role/branch_id 편집 컨트롤을 비활성(서버가 409 `CANNOT_MODIFY_SELF_ROLE` 반환). branch_id 변경 후에는 해당 사용자가 자동 로그아웃됨을 안내(서버가 refresh 토큰 무효화).
- `Sales/` 페이지와 `BulkExtend.tsx`, `Branches/`는 라우트 가드에서 `auth.role === 'global'`일 때만 노출. 지점 관리자는 메뉴에서도 숨긴다.
- 목록 화면(회원·체크인)은 cursor 페이지네이션. 한 번에 20개 로드, 무한 스크롤(또는 "더 보기" 버튼)로 다음 페이지 요청. `next_cursor`가 null이면 끝. 백엔드 400 `INVALID_CURSOR`/`INVALID_LIMIT`은 토스트 안내 후 첫 페이지로 리셋.
- 체크인 이력 페이지의 `aggregate=daily` 모드는 페이지네이션 없음. 기간 선택 UI는 최대 92일까지만 허용(초과 시 제출 비활성 + 안내). 응답 항목은 `(member_id, date)` 단위 + `checkin_count` 표시.
- 회원 수정 폼(`Members/`의 PATCH)은 **소속 지점(`branch_id`) 필드를 비활성/숨김** 처리한다(이전 불가, 다른 지점 등록은 새 회원 등록으로). **이름·전화·생년월일만 편집 가능** — 그 외 필드는 폼에 두지 않는다.
- 회원 목록은 응답의 `branch_name`을 그대로 표시(전역 관리자가 지점 식별). 지점 관리자는 본인 지점만 보이므로 컬럼 노출 여부 자율.
- **회원 상세**(`Members/`의 회원 행 클릭/상세 페이지)는 `GET /api/members/:id` 한 번으로 헤더(이름·전화·생년월일·지점) + active 회원권 카드 + 회원권 이력 표 + 결제 이력 표를 한 화면에 그린다. 페이지네이션 없음(서버가 최근 20개 회원권 + 그 결제까지 한 번에 내려줌). 회원권 행 클릭 시 회원권 상세 모달/서브 페이지 진입.
- **회원권 상세**(pause/unpause/cancel-pause/refund 폼이 진입하는 모달·페이지)는 `GET /api/memberships/:id` 한 번으로 회원권 현재 상태(`status`, `pause_used`, `pause_start_date`, `end_date`) + 결제 이력(부여+환불) + `membership_events` 이력을 가져와 폼 노출 여부를 결정한다. 폼 제출 후에는 같은 endpoint를 invalidate해 새 상태를 다시 가져온다.
- `Sales/` 페이지는 매출 응답의 `gross_total`/`refund_total`/`net_total`을 모두 표시한다. 카드 3개(총매출 / 환불 / 순매출) + 일별 표는 `gross/refund/net`을 각각 컬럼으로. 수단별 표(현금/카드)도 같은 분리 적용.
- 로그인 잠금: 백엔드 응답이 `ACCOUNT_LOCKED`이면 잠금 해제 시각까지 폼을 비활성화하고 카운트다운을 표시한다. `TEMP_PASSWORD_EXPIRED`는 별도 안내 분기로 처리.

## 디자인 참조
- **토큰 정본은 `frontend/ui-design/styles.css` + `docs/UI_GUIDE.md`**. Phase 3 step 2 scaffold에서 `frontend/ui-design/styles.css`의 CSS 변수(주석 포함)를 `frontend/src/styles/tokens.css`로 그대로 복사한다. `tailwind.config.ts`는 이 CSS 변수들을 참조해 색상·간격·타이포 토큰을 노출한다. 토큰을 직접 수정하지 않는다 — 변경이 필요하면 `frontend/ui-design/styles.css`(정본)를 먼저 고치고 `tokens.css`에 재복사한다.
- **화면 레이아웃은 `frontend/ui-design/*.jsx` 시안이 픽셀 단위 정본**이다 — `kiosk-screens-1.jsx`, `kiosk-screens-2.jsx`, `admin-shell.jsx`, `admin-members.jsx`, `admin-plan-grant.jsx`, `admin-sales-login.jsx`. JSX 구조·className·간격·아이콘 위치를 그대로 따른다. 시안과 달라야 할 정당한 이유가 있을 때(예: 반응형 모바일 분기, 접근성 보강)만 변경하고 step.md 또는 PR 본문에 사유를 적는다. 허용 오차 — 색상은 토큰 일치, 간격은 ±4px 이하.
- **시안에 없는 화면**(예: PasswordChange의 인라인 강도 가이드 텍스트 색상, 토스트, idle 타임아웃 카운트다운 안내 등)은 같은 토큰을 사용해 시안과 시각적으로 일관되게 만든다. 새 토큰을 추가하지 말고 기존 토큰의 조합으로 해결한다.
- **`frontend/ui-design/` 폴더는 참조 전용**이다. frontend 구현(`src/`) 어디에서도 `ui-design` 내부 파일을 `import` 하지 않는다. 토큰은 복사된 `tokens.css`로만, 레이아웃은 시안 파일을 "보고" 손으로 재작성한다(JSX를 그대로 import하면 시안 폴더와 구현 코드가 결합돼 단절이 깨진다).
