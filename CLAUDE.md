# 프로젝트: 체육관 체크인 시스템

회원이 체육관 입구 공용 태블릿에서 스스로 본인을 찾아 체크인하고, 관리자(사장님/코치)는 반응형 웹앱에서 회원·회원권·출석을 관리한다. **여러 지점을 한 시스템에서 중앙 관리**한다.

기획·설계 문서는 `docs/` 참고:
- `docs/PRD.md` — 목표·사용자·핵심 기능·MVP 제외 사항
- `docs/ARCHITECTURE.md` — 디렉토리 구조·계층 패턴·데이터 흐름
- `docs/ADR.md` — 핵심 아키텍처 결정과 트레이드오프
- `docs/UI_GUIDE.md` — 디자인 원칙·색상·컴포넌트·타이포

## 구성 (모노레포)
세부 스택·명령어·규칙은 각 하위 CLAUDE.md를 참조한다. Claude Code에서 `@` 참조로 자동 로드된다.

- Frontend: @frontend/CLAUDE.md
- Backend:  @backend/CLAUDE.md
- Database: @db/CLAUDE.md

## 공통 규칙 (CRITICAL)
- **CRITICAL**: 프론트엔드는 오직 Gin API만 호출한다. 클라이언트에서 DB 직결·외부 서비스 직결 금지.
- **CRITICAL**: 모든 API 응답과 요청은 JSON. 에러 응답은 `{ "error": { "code": "...", "message": "..." } }` 형식.
- **CRITICAL**: 비밀번호·JWT·회원 개인정보(전화번호·생년월일)는 로그·에러 메시지·터미널 출력에 포함하지 않는다.
- **CRITICAL**: 지점 관리자(`role='branch'`)의 읽기/쓰기는 서버 서비스 계층에서 `branch_id` 필터를 강제한다. 전역 관리자(`role='global'`)만 전체 조회 허용.
- **CRITICAL**: DB 스키마 변경은 `db/migrations/` goose 마이그레이션으로만. 서버가 런타임에 스키마를 바꾸지 않는다.

## 개발 프로세스
- 커밋 메시지는 conventional commits 형식 (`feat:`, `fix:`, `docs:`, `refactor:`).
- DB/API 변경은 마이그레이션 → 백엔드 → 프론트 순으로 반영한다.

## 배포
- API(Go/Gin) + DB(Postgres)는 클라우드에 중앙 배포(Fly.io / Railway / Render 중 택일, MVP에서 결정).
- 프론트엔드는 정적 빌드 결과를 같은 도메인 또는 CDN으로 서빙.
- 각 지점 태블릿은 같은 URL에 접속 → 최초 1회 관리자가 지점 선택 → `localStorage`에 저장.
