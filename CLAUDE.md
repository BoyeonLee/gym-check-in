# 프로젝트: 체육관 체크인 시스템

회원이 체육관 입구 공용 태블릿에서 스스로 본인을 찾아 체크인하고, 관리자(사장님/코치)는 반응형 웹앱에서 회원·회원권·출석을 관리한다. **여러 지점을 한 시스템에서 중앙 관리**한다.

기획·설계 문서는 `docs/` 참고:
- `docs/PRD.md` — 목표·사용자·핵심 기능·MVP 제외 사항
- `docs/ARCHITECTURE.md` — 디렉토리 구조·계층 패턴·데이터 흐름·인증/페이지네이션/멱등성 정책
- `docs/ADR.md` — 핵심 아키텍처 결정과 트레이드오프
- `docs/API.md` — 엔드포인트 명세·요청/응답·에러 코드 카탈로그
- `docs/UI_GUIDE.md` — 디자인 원칙·색상·컴포넌트·타이포
- `docs/ROADMAP.md` — Phase별 산출물·작업 항목·검증 기준
- `docs/DEV_SETUP.md` — 신규 개발자 / 새 세션 부트스트랩 한 페이지
- `docs/OPERATIONS.md` — 운영 절차(백업·비번 분실·HTTPS·태블릿)
- `docs/TESTING.md` — TDD 워크플로우·테스트 계층·에러/엣지 케이스 카탈로그·에러 핸들링 패턴

## 구성 (모노레포)
세부 스택·명령어·규칙은 각 하위 CLAUDE.md를 참조한다. Claude Code에서 `@` 참조로 자동 로드된다.

- Frontend: @frontend/CLAUDE.md
- Backend:  @backend/CLAUDE.md
- Database: @db/CLAUDE.md

## 공통 규칙 (CRITICAL)
- **CRITICAL**: 프론트엔드는 오직 Gin API만 호출한다. 클라이언트에서 DB 직결·외부 서비스 직결 금지.
- **CRITICAL**: 모든 API 응답과 요청은 JSON. 에러 응답은 `{ "error": { "code": "...", "message": "..." } }` 형식.
- **CRITICAL**: 비밀번호·JWT·회원 개인정보(전화번호·생년월일)는 로그·에러 메시지·터미널 출력에 포함하지 않는다.
- **CRITICAL**: 지점 관리자(`role='branch'`)의 읽기/쓰기는 서버 서비스 계층에서 `branch_id` 필터를 강제한다. 전역 관리자(`role='global'`)만 전체 조회 허용. 매출 조회(`/api/sales/*`), 대량 연장, 지점 CRUD는 전역 전용.
- **CRITICAL**: DB 스키마 변경은 `db/migrations/` goose 마이그레이션으로만. 서버가 런타임에 스키마를 바꾸지 않는다.
- **CRITICAL**: 결제 정보(금액·수단·결제일)는 매출 집계 등 관리 화면 외 응답에 노출하지 않는다. 키오스크 API 응답에는 절대 포함하지 않는다.
- **CRITICAL**: 시크릿(DB 비밀번호, JWT 비밀키, 시드 초기 관리자 비번 등)은 리포지토리에 커밋하지 않는다. 로컬은 루트 `.env` 파일(`.gitignore` 처리), 키 목록 공유는 `.env.example`만. 운영은 호스팅 플랫폼의 환경변수/시크릿 매니저(Fly.io secrets, Railway env, AWS Secrets Manager 등)를 사용한다.

## 개발 프로세스
- 커밋 메시지는 conventional commits 형식 (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`).
- DB/API 변경은 마이그레이션 → 백엔드 → 프론트 순으로 반영한다.
- **백엔드는 TDD로 진행한다**. 모든 핸들러·도메인 함수는 실패 테스트를 먼저 작성한 뒤 최소 구현으로 통과시킨다. 정상 1개당 에러/엣지 N개를 `docs/TESTING.md` 카탈로그에서 가져온다. 테스트 없는 PR은 머지 금지.

## 로컬 개발 환경
- PostgreSQL은 루트 `docker-compose.yml`로 띄운다(`docker compose up -d db`). 자격증명은 `.env`에서 주입.
- 백엔드/프론트엔드 명령어는 각 폴더의 CLAUDE.md 참조. `DATABASE_URL`은 루트 `.env`를 같이 사용한다.

## 배포
- API(Go/Gin) + DB(Postgres)는 클라우드에 중앙 배포(Fly.io / Railway / Render 중 택일, MVP에서 결정).
- 프론트엔드는 정적 빌드 결과를 같은 도메인 또는 CDN으로 서빙.
- 각 지점 태블릿은 같은 URL에 접속 → 최초 1회 관리자가 지점 선택 → `localStorage`에 저장.
- 회원에게 브라우저 UI(주소창·탭)가 보이지 않도록 프론트엔드는 PWA로 빌드하고 태블릿 홈 화면에 추가해 풀스크린으로 실행한다(ADR-005).
