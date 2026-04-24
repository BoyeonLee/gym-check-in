# Backend — 체육관 체크인 API

## 기술 스택
- Go 1.22+
- Gin (HTTP 라우팅·미들웨어)
- pgx v5 (PostgreSQL 드라이버·풀)
- bcrypt (관리자 비밀번호 해시)
- JWT (관리자 세션 토큰)
- goose (마이그레이션 — `db/` 폴더에서 관리)

## 디렉토리 구조
```
backend/
├── cmd/
│   └── server/
│       └── main.go         # 엔트리포인트 (설정 로드, 라우터·DB 풀 초기화)
├── internal/
│   ├── http/               # Gin 핸들러 + 라우팅
│   │   ├── router.go
│   │   ├── middleware/     # auth, logging, recovery
│   │   ├── members.go
│   │   ├── memberships.go
│   │   ├── checkins.go
│   │   ├── branches.go
│   │   └── admins.go
│   ├── domain/             # 엔티티·도메인 서비스·권한 체크
│   │   ├── member.go
│   │   ├── membership.go   # 부여·정지·환불·대량 연장 로직
│   │   ├── checkin.go
│   │   ├── branch.go
│   │   └── admin.go
│   ├── repo/               # pgx 리포지토리 (SQL은 여기에만)
│   │   ├── members_repo.go
│   │   ├── memberships_repo.go
│   │   ├── checkins_repo.go
│   │   ├── branches_repo.go
│   │   └── admins_repo.go
│   ├── auth/               # JWT 발급·검증, must_change_password 가드
│   └── config/             # 환경변수 로더
├── go.mod
└── go.sum
```

## 주요 엔드포인트 (초안)
```
POST   /api/admin/login              # 관리자 로그인 → JWT + must_change_password
POST   /api/admin/password           # 비밀번호 변경

GET    /api/branches                 # 지점 목록 (키오스크 초기화·관리자 공용)
POST   /api/branches                 # 전역 관리자만
PATCH  /api/branches/:id             # 전역 관리자만
DELETE /api/branches/:id             # 전역 관리자만

GET    /api/members/search           # q=이름/전화, branchId 필수(키오스크 체크인 검색)
GET    /api/members                  # 관리자 목록/필터
POST   /api/members                  # 관리자: 이름·전화·생년월일·지점
PATCH  /api/members/:id
DELETE /api/members/:id

POST   /api/members/:id/memberships         # 부여(monthly|pass10)
POST   /api/memberships/:id/pause           # { start_date, end_date, reason }
POST   /api/memberships/:id/refund          # { reason }
POST   /api/memberships/bulk-extend         # 전역 전용 { branch_id?, type?, days, reason }

POST   /api/check-ins                # 체크인 기록
GET    /api/check-ins                # 관리자: 오늘/기간별 조회, 1일 1회 집계 옵션
```

## 명령어
```
go mod tidy
go run ./cmd/server                           # 개발 실행
go test ./...                                 # 전체 테스트
go build -o bin/server ./cmd/server           # 빌드
DATABASE_URL=... JWT_SECRET=... ./bin/server  # 실행
```

환경변수: `DATABASE_URL`, `JWT_SECRET`, `PORT`(기본 8080), `CORS_ORIGIN`.

## 규칙
- **CRITICAL**: SQL은 오직 `internal/repo`에만 존재한다. 핸들러·도메인 서비스는 리포지토리 인터페이스를 통해서만 DB에 접근.
- **CRITICAL**: 비밀번호·JWT·개인정보는 로그·에러 메시지·응답에 포함하지 않는다.
- **CRITICAL**: 지점 관리자(`role='branch'`)의 모든 읽기/쓰기는 서비스 계층에서 `branch_id` 필터를 강제한다. 전역 관리자만 `branch_id` 미지정 허용.
- 요청 검증: Gin 바인딩 태그 + `validator` 태그 + 명시적 비즈니스 규칙 확인(회원권 날짜 범위·지점 일치 등).
- 에러는 구조화 로그(`slog`)로 남기고, 응답은 `{ "error": { "code": "...", "message": "..." } }` 형태로 통일.
- `must_change_password=true`인 관리자 JWT는 `/api/admin/password` 외 모든 라우트에서 403.
- 회원권 정지: `end_date += (pause_end_date - pause_start_date)` 갱신 + `membership_events`에 이벤트 기록을 한 트랜잭션으로 처리.
- 대량 연장: 필터 조건으로 뽑은 활성 `memberships` row들과 이벤트 기록을 한 트랜잭션으로 처리.
- 체크인은 같은 날 중복 허용 — `check_ins` 삽입에 유니크 제약 없음. 횟수권이면 `remaining -= 1`을 동일 트랜잭션에서.

## 테스트
- 리포지토리 테스트는 실제 Postgres(테스트 DB, goose로 스키마 적용)로 수행. 모킹 금지.
- 도메인 서비스 테스트는 리포지토리 인터페이스를 fake/in-memory로 대체 가능.
- 핸들러는 Gin `httptest`로 end-to-end.
