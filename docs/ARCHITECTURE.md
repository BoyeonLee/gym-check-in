# 아키텍처

## 디렉토리 구조
```
gym-check-in/
├── frontend/   # Vite + React + TypeScript + Tailwind (키오스크 + 관리자)
│   └── CLAUDE.md
├── backend/    # Go + Gin + pgx (HTTP JSON API)
│   └── CLAUDE.md
├── db/         # PostgreSQL 스키마·마이그레이션(goose)·시드
│   └── CLAUDE.md
├── docs/       # 기획·설계 문서
└── CLAUDE.md   # 프로젝트 개요 + 하위 CLAUDE.md 참조 허브
```

## 패턴
- **프론트엔드**: 라우트 분할로 두 앱을 한 번들에 담는다. `/` = 키오스크(회원용), `/admin/*` = 관리자(반응형 웹앱). 서버 상태는 React Query, 로컬 UI 상태는 `useState`/`useReducer`, 지점 선택은 `localStorage` + Context.
- **백엔드**: `handler → service → repo` 3계층. SQL은 `repo`에서만. 요청 검증은 Gin 바인딩 태그 + 명시적 유효성 검사. 관리자 세션은 JWT.
- **DB 접근**: 프론트는 반드시 Gin API를 통해서만 접근. 클라이언트에서 DB 직결 금지.
- **멀티테넌시**: `branches` 테이블 + 대부분 리소스가 `branch_id`를 가짐. 지점 관리자는 서비스 계층에서 `branch_id` 필터 강제, 전역 관리자는 전체 조회 허용.

## 데이터 흐름
```
[공용 태블릿 / 관리자 기기 브라우저]
        │ (HTTPS, JSON)
        ▼
  [Gin HTTP 핸들러]
        │
        ▼
  [서비스(도메인 로직·권한 체크)]
        │
        ▼
  [리포지토리(pgx)] ──► [PostgreSQL]
        ▲
        │ JSON 응답
        └── React Query 캐시 업데이트 → UI 렌더
```

예: 회원 체크인
1. 태블릿에서 이름·전화·음성 중 하나로 검색 요청 → `GET /api/members/search?q=...&branchId=...`
2. 핸들러 → 서비스에서 `branch_id` 필터 적용 → 리포지토리 질의 → 결과 반환
3. 본인 선택 후 `POST /api/check-ins` → 활성 회원권 조회 → `check_ins` 삽입 + 횟수권이면 `memberships.remaining -= 1`
4. 완료 응답 → 키오스크가 완료 화면 렌더 → 타임아웃 후 대기 화면 복귀

## 상태 관리
- **서버 상태**: React Query. 회원 검색·회원권 상태·체크인 이력은 쿼리/뮤테이션으로 관리.
- **클라이언트 상태**: `useState`/`useReducer`. 폼·토글·스텝 진행.
- **지속 상태**: `localStorage`에 태블릿의 `branchId`·관리자 JWT 저장. 지점 선택은 Context로 하위 컴포넌트에 전파.
- **권한 상태**: 관리자 역할(`global`/`branch`)은 로그인 응답에서 받아 Context로 보관, 라우트 가드·메뉴 표시에 사용.
