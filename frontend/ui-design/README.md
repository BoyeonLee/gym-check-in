# Handoff: P-BOY MMA Check-in System

## Overview
P-BOY MMA(MMA 체육관)의 회원 체크인 + 운영 관리 시스템 디자인. 매장에 설치된 가로형 태블릿 키오스크와 관리자 데스크탑/모바일 콘솔로 구성됩니다.

- **키오스크**: 회원이 매장 입장 시 본인 식별·체크인 (Idle → 입력 방식 선택 → 음성/타이핑 검색 → 동명이인 선택 → 완료)
- **관리자 콘솔**: 회원 목록, 회원권 부여, 매출 대시보드, 로그인

## About the Design Files
**이 폴더의 HTML/JSX 파일들은 디자인 레퍼런스입니다 — 의도한 외관·인터랙션을 보여주는 프로토타입이며, 그대로 production에 복사하는 코드가 아닙니다.**

작업 목표는 이 HTML 디자인을 **타겟 코드베이스의 기존 환경**(React / Next.js / Vue / SwiftUI 등)에서 그 코드베이스의 정착된 패턴·라이브러리를 사용해 재구현하는 것입니다. 환경이 아직 없다면, 프로젝트에 가장 적합한 프레임워크를 정해서 거기에 구현하세요.

## Fidelity
**High-fidelity (hifi)** — 색상·타이포그래피·여백·인터랙션이 모두 확정된 픽셀 단위 목업입니다. 코드베이스 기존 컴포넌트 라이브러리로 픽셀 단위로 재현하세요.

## Brand
- 로고: `assets/pboy-logo.jpg` (검정 배경 + 빨간 "MB" 모노그램)
- 브랜드 컬러: **`#E10600`** (P-BOY Red)

## Screens / Views

### 1. 키오스크 Idle (1280×800 가로 태블릿)
**Energetic 변형으로 확정.** Quiet/Functional 변형은 폐기.
- **목적**: 평소 매장에 띄워두는 화면. 사용자가 화면 어디든 터치하면 다음 단계로.
- **구성**:
  - 풀스크린 검정 (#0A0A0A) 배경에 거대한 흐릿한 워드마크 ("P-BOY/MMA", Space Grotesk 700, 280px+, 1px stroke #ffffff0a)
  - 헤더: 좌상단 로고 + "강남점 · GANGNAM"
  - 우상단: 오늘 체크인 카운터 pill
  - 중앙: "터치하여 시작" (Pretendard 800, 80px, white) + 부제 + 큰 펄스 인디케이터
  - 하단: 시간/날짜 + 영업 상태
- 파일: `kiosk-screens-1.jsx` (KioskIdle)

### 2. 키오스크 InputSelect
- **목적**: 음성 검색 / 타이핑 검색 두 가지 방법 중 선택
- 큰 버튼 2개 (각 minHeight 96px, fontSize 28). 뒤로 버튼은 헤더 좌측.

### 3. 키오스크 VoiceSearch
- **목적**: 음성으로 이름 검색
- 상태 2개: `listening` (빨간 펄스 원) / `failed` (회색 + 경고색 보더 + transcript 표시)
- 3회 실패 시 자동으로 TypingSearch로 전환

### 4. 키오스크 TypingSearch
- **목적**: 전화 뒷자리 또는 한글 이름 입력
- 탭 2개: `phone` (숫자 키패드) / `name` (한글 키패드)
- 큰 디스플레이 박스 + 커스텀 키패드

### 5. 키오스크 MemberPick
- **목적**: 검색 결과가 여러 명일 때 본인 선택 (동명이인)
- 마스킹된 row: 회원번호 강조, 이름·생년월일 일부, 회원권 종류만 노출 (개인정보 보호)

### 6. 키오스크 Done
- **목적**: 체크인 완료 확인
- 큰 체크 아이콘, 회원명·회원권·잔여일, 5초 자동 카운트다운 후 Idle 복귀

### 7. 관리자 회원 목록 (1440×900 데스크탑 + 390×844 모바일)
- 좌측 다크 사이드바 + 우측 라이트 컨텐츠
- 데스크탑: 테이블 (회원번호 / 이름 / 연락처 / 회원권 / 상태 / 시작일 / 만료일 / 잔여 / 액션)
- 모바일: 카드 스택
- 검색 + 필터 칩 + 페이지네이션
- 밀도 토글: Compact / Comfortable

### 8. 관리자 회원권 부여
- 좌측 폼 (회원권 종류 / 기간 / 결제수단) + 우측 sticky 요약 카드

### 9. 관리자 매출 대시보드
- KPI 카드 3개 + 일별 차트 + 결제수단 비중 + 일별 상세 표

### 10. 관리자 로그인
- **다크 배경(`#0A0A0A`) 기본** — 키오스크와 시각적으로 연결
- 중앙 정렬: P-BOY 로고 (88px) → "P-BOY MMA ADMIN" 아이브로(빨강) → "관리자 로그인" 타이틀(26px/800) → 로그인 카드
- 카드: 아이디 / 비밀번호 + "비밀번호 찾기" / 로그인 상태 유지 체크박스(빨강) / 로그인 버튼
- Light 변형도 제공

## 다크/라이트 모드
- **키오스크**: 다크가 기본. 라이트 변형은 `.theme-kiosk-light` CSS 클래스로 토큰 swap
- **관리자**: 라이트가 기본. 다크 변형은 `.theme-admin-dark` CSS 클래스로 토큰 swap
- 컴포넌트 포크 없이 CSS 변수만 반전하므로, 코드베이스에서도 같은 패턴 권장 (단일 컴포넌트 + 테마 컨텍스트)

## Design Tokens

### Colors
```css
/* Brand */
--pb-red: #E10600;
--pb-red-hover: #C20500;
--pb-red-dim: rgba(225, 6, 0, 0.12);

/* Dark (kiosk default) */
--k-bg: #0A0A0A;
--k-surface: #141414;
--k-surface-2: #1C1C1C;
--k-border: #262626;
--k-border-strong: #333333;
--k-text: #FAFAFA;
--k-text-dim: #A3A3A3;
--k-text-muted: #737373;

/* Light (admin default) */
--a-bg: #FAFAFA;
--a-surface: #FFFFFF;
--a-surface-2: #F5F5F5;
--a-border: #E5E5E5;
--a-border-strong: #D4D4D4;
--a-text: #0A0A0A;
--a-text-dim: #525252;
--a-text-muted: #A3A3A3;
--a-sidebar: #0A0A0A;

/* Status */
--s-success: #16A34A; --s-success-bg: #DCFCE7;
--s-warning: #D97706; --s-warning-bg: #FEF3C7;
--s-danger:  #DC2626; --s-danger-bg:  #FEE2E2;
--s-info:    #2563EB; --s-info-bg:    #DBEAFE;
```

### Typography
- 한글: **Pretendard** (https://cdn.jsdelivr.net/gh/orioncactus/pretendard@v1.3.9/dist/web/static/pretendard.min.css)
- 영문/디스플레이: **Space Grotesk** (Google Fonts, 400/500/600/700)
- 숫자/모노: **JetBrains Mono** (Google Fonts, 400/500/600)

스케일:
- 키오스크: 본문 24px+ / 버튼 28px+ / 디스플레이 80–280px / 터치타겟 64px+
- 관리자: 본문 13–14px / 헤딩 18–32px / 입력 필드 40px / 라벨 11px mono uppercase

### Spacing & Radius
- 모서리: **각진 디자인** (대부분 `border-radius: 4px`, 카드는 6–8px)
- gap/padding: 4의 배수 (8, 12, 16, 20, 24, 28, 32, 40, 48, 56, 64)

### Hit targets
- 키오스크 버튼: minHeight 64px+, 큰 버튼 96px+
- 관리자 버튼: 32–40px
- 모바일 터치: 44px+ 절대 준수

## Interactions & Behavior

### 키오스크 플로우
1. **Idle** → 화면 터치 → InputSelect
2. **InputSelect** → 음성 → VoiceSearch / 타이핑 → TypingSearch
3. **VoiceSearch** → 3초 후 결과 (mock: 항상 listening → failed) → 3회 실패 시 자동 TypingSearch
4. **TypingSearch** → 입력 → 검색 버튼 → MemberPick (또는 단일 결과 시 바로 Done)
5. **MemberPick** → 회원 row 탭 → Done
6. **Done** → 5초 카운트다운 후 자동으로 Idle

뒤로가기 버튼은 모든 단계의 헤더 좌측에. Done은 뒤로가기 없음.

### 관리자
- 사이드바 네비: 회원 / 회원권 / 결제 / 매출 / 체크인 이력 / 설정
- 테이블 row hover, 액션 버튼 (편집/연장/정지)
- 회원권 부여 폼: 종류 선택 시 우측 요약 카드 자동 업데이트
- 모바일: 사이드바 → 햄버거 메뉴

## State Management
- 키오스크는 단순 화면 머신 (`screen` 단일 상태) + 입력값/선택 회원/카운트
- 관리자는 react-query/SWR 등으로 회원·회원권·매출 데이터 fetch
- 테마는 컨텍스트 또는 localStorage 영속

## Assets
- `assets/pboy-logo.jpg` — 사용자 제공 로고 (검정 + 빨강 MB 모노그램). production에서는 SVG로 트레이싱 권장
- 회원 사진/체육관 사진 등은 placeholder (`pb-placeholder` 클래스, 사선 줄무늬 패턴) — 실제 이미지로 교체 필요

## Files in this bundle
- `P-BOY MMA Check-in Design.html` — 메인 캔버스 뷰 (모든 화면 한눈에)
- `kiosk-prototype.html` — 키오스크 인터랙티브 프로토타입 (Idle → Done 전체 클릭 가능)
- `app.jsx` — 캔버스 뷰의 화면 배치
- `kiosk-screens-1.jsx` / `kiosk-screens-2.jsx` — 키오스크 화면 컴포넌트
- `admin-shell.jsx` / `admin-members.jsx` / `admin-plan-grant.jsx` / `admin-sales-login.jsx` — 관리자 화면 컴포넌트
- `logo.jsx` — 로고 래퍼
- `styles.css` — 디자인 토큰 + 테마 반전 클래스
- `assets/pboy-logo.jpg` — 로고 이미지

## Notes for Implementation
- 키오스크는 항상 가로 1280×800 풀스크린 가정. 다른 해상도는 letterbox.
- 관리자는 1440 데스크탑 우선, 모바일까지 반응형.
- 직각 모서리·강한 타입 위계·빨강 액센트는 **절제**해서 (CTA·브랜드 모먼트에만) 사용.
- 키오스크 다크/관리자 라이트의 비대칭은 의도된 것 — 매장 환경(어둡고 강렬)과 사무실 환경(밝고 정확)에 각각 맞춤.
