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
│   │   │   ├── Idle.tsx          # 대기 화면
│   │   │   ├── InputSelect.tsx   # 음성/타이핑 선택
│   │   │   ├── VoiceSearch.tsx   # 음성 인식 (3회 실패 시 타이핑 전환)
│   │   │   ├── TypingSearch.tsx  # 이름·전화번호 검색
│   │   │   ├── MemberPick.tsx    # 동명이인 대비 본인 선택
│   │   │   └── CheckInDone.tsx   # 완료 화면(타임아웃 후 Idle 복귀)
│   │   └── admin/       # 관리자 반응형 웹앱 (/admin/*)
│   │       ├── Login.tsx
│   │       ├── PasswordChange.tsx # must_change_password=true 강제
│   │       ├── Members/
│   │       ├── Memberships/       # 부여·정지·환불
│   │       ├── BulkExtend.tsx     # 전역 관리자 전용 대량 연장
│   │       ├── CheckIns/
│   │       └── Branches/          # 전역 관리자 전용
│   ├── components/      # 공통 UI (Button, Card, Table, NumberPad 등)
│   ├── api/             # fetch 래퍼 (Gin API 호출, JWT 헤더 자동)
│   ├── hooks/           # useBranch, useAuth, useSpeechRecognition
│   ├── context/         # BranchContext, AuthContext
│   ├── types/           # API 응답·도메인 타입
│   └── styles/
├── index.html
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

API 엔드포인트는 환경변수 `VITE_API_URL`로 주입.

## 규칙
- **CRITICAL**: 모든 데이터 접근은 Gin API만 경유. `fetch`/axios를 외부 서비스·DB로 직접 보내지 않는다.
- **CRITICAL**: 음성 인식은 브라우저 Web Speech API(`window.SpeechRecognition`)만 사용. 외부 STT 서비스 SDK 도입 금지.
- **CRITICAL**: 회원 개인정보(전화번호, 생년월일)는 화면에 꼭 필요한 순간에만 표시, 로그·에러 메시지로 유출 금지.
- 키오스크 화면은 `touch-action: manipulation`, 최소 터치 타겟 64px(세부 값은 `docs/UI_GUIDE.md`).
- 키오스크는 풀스크린 기본, 오른쪽 위 숨김 제스처(5초 롱프레스)로 지점 재설정 화면 진입.
- 관리자 UI는 **반응형**. 모바일 레이아웃에서 테이블은 카드 스택으로 대체.
- 상태 지속: 태블릿 `branchId`·관리자 JWT는 `localStorage`. 로그아웃 시 JWT 제거.
- 음성 인식 실패 카운터는 컴포넌트 상태로 관리, 3회 도달 시 자동으로 `TypingSearch`로 전환 + 안내 토스트.

## 디자인 참조
`docs/UI_GUIDE.md`의 색상·컴포넌트·타이포 토큰을 그대로 Tailwind 클래스/테마로 사용한다.
