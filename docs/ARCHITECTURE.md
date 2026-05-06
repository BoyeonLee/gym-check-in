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
1. 태블릿에서 음성, 이름 텍스트(prefix·최소 2자), 전화 뒷자리 4자리, 또는 회원 번호 중 하나로 검색 요청 → `GET /api/members/search?q=...&branchId=...&mode=name|phone|memberId`
2. 핸들러 → 서비스에서 `branch_id` 필터 + **활성 회원권(`status='active'`) 존재 조건** 적용 → 리포지토리 질의 → 최근 체크인 순 정렬해 반환. 활성 회원권이 없는 회원은 검색 결과에서 제외.
3. 본인 선택 후 `POST /api/check-ins` → 활성 회원권 `SELECT ... FOR UPDATE` → 한 트랜잭션에서 `check_ins` 삽입. 횟수권이면 같은 회원·같은 날짜·같은 지점의 기존 `check_ins` 존재 여부를 잠금 조회 → 없을 때만 `memberships.remaining -= 1`(1일 1회 차감). 활성 회원권이 없으면 422 `NO_ACTIVE_MEMBERSHIP`.
4. 완료 응답 → 키오스크가 완료 화면 렌더 → 타임아웃 후 대기 화면 복귀

체크인 화면은 진입 시 `GET /api/check-ins/today-count?branchId=...`로 KST 기준 오늘 해당 지점 체크인 수를 조회해 헤더에 표시한다(쿼리: `(checked_in_at AT TIME ZONE 'Asia/Seoul')::date = (now() AT TIME ZONE 'Asia/Seoul')::date`). 자체 체크인 성공 후에는 React Query 캐시를 invalidate해 카운터를 즉시 갱신한다(폴링 없음, MVP 범위).

예: 회원권 부여 + 결제
1. 관리자가 회원 상세에서 "회원권 부여" → 유형·기간·결제(금액·수단·결제일) 입력 후 `POST /api/members/:id/memberships`
2. 서비스가 한 트랜잭션으로 `memberships` insert + `payments` insert + (지점 관리자면 자기 지점 회원인지 확인)
3. 매출 조회는 `GET /api/sales/summary?from=...&to=...&branchId=?` 로 `payments` 합계를 결제일·수단·지점으로 집계 (전역 관리자 전용). 환불 row의 `paid_at`은 회원이 환불 신청한 날짜로 기록되어 그 날짜 매출에 음수로 반영된다.

## 자정 배치 (회원권 상태 전환)
매일 KST **00:01**에 다음 트랜잭션들을 실행한다(00:00 자정 경계 데이터 일관성 안전 margin). 모든 SQL은 `(now() AT TIME ZONE 'Asia/Seoul')::date`로 KST 기준 날짜를 계산한다(`CURRENT_DATE`는 DB 세션 타임존 의존이라 사용 금지). DB 세션 timezone은 풀 연결 시 `SET TIME ZONE 'UTC'`로 강제하고, KST 변환은 명시적 `AT TIME ZONE 'Asia/Seoul'`로만 수행.
1. `UPDATE memberships SET status='expired' WHERE status='active' AND end_date < (now() AT TIME ZONE 'Asia/Seoul')::date`
2. `UPDATE memberships SET status='active' WHERE status='paused' AND pause_end_date < (now() AT TIME ZONE 'Asia/Seoul')::date`
3. `UPDATE memberships SET status='paused' WHERE status='active' AND pause_start_date = (now() AT TIME ZONE 'Asia/Seoul')::date` — 미래 예약된 정지가 도래하는 날.
4. `DELETE FROM idempotency_keys WHERE created_at < now() - interval '24 hours'` — bulk-extend 멱등성 키 정리.

구현은 백엔드 단일 바이너리에 별도 명령(`./bin/server batch run-expiry`)을 두고, MVP에는 인-프로세스 cron(`robfig/cron`)으로 매일 KST 00:00에 호출한다. 호스팅 결정 시(ADR-010 예정) 외부 스케줄러(Fly.io scheduled machines, Railway cron 등)로 분리할 수 있다. 횟수권 잔여가 0이 되는 순간의 expired 전환은 **체크인 트랜잭션 내에서 즉시** 처리하며 배치 대상이 아니다.

자정 배치는 추가로 정리 잡을 수행한다:
- `DELETE FROM idempotency_keys WHERE created_at < now() - interval '24 hours'` — Idempotency-Key 정리 (bulk-extend·회원권 부여·환불 공용)
- `DELETE FROM revoked_refresh_tokens WHERE revoked_at < now() - interval '15 hours'` — refresh JWT 만료 길이만큼만 보관
- `DELETE FROM admin_audit_logs WHERE created_at < now() - interval '1 year'` — 감사 로그 1년 보관

## 키오스크 idle 타임아웃
`InputSelect/VoiceSearch/TypingSearch/MemberPick` 화면에 진입한 뒤 **10초 동안 입력이 없으면 자동으로 Idle(대기 화면)로 복귀**한다. 음성 인식 중에도 결과가 도착하지 않은 채 10초가 지나면 동일하게 복귀. `CheckInDone`은 별도로 2~3초 후 Idle로 복귀(UI_GUIDE).

## 음성 인식 호환성·폴백
- Web Speech API는 Android Chrome/Edge에서 가장 안정적. iOS는 모든 브라우저(iPad Chrome 포함)가 WebKit 엔진을 강제 사용해 `SpeechRecognition` 미지원이거나 불안정.
- `useSpeechRecognition` 훅 진입 시 `'webkitSpeechRecognition' in window` 또는 `SpeechRecognition` 가용성을 확인 → 미지원이면 InputSelect에서 음성 버튼을 비활성/숨김.
- 마이크 권한 거부(`NotAllowedError`) 시 즉시 TypingSearch로 전환 + 안내 토스트.
- 키오스크 권장 환경: **Android 태블릿 + Chrome**.

## 키오스크 운영 (브라우저 UI 숨김)
회원 화면에 주소창·탭이 보이지 않도록 프론트엔드를 PWA로 빌드한다.
- `frontend/public/manifest.webmanifest`에 `"display": "fullscreen"`, 아이콘, `start_url` 지정.
- 태블릿에서 최초 1회 사이트 접속 → "홈 화면에 추가" → 아이콘으로 진입하면 브라우저 크롬 없이 풀스크린.
- iOS는 Safari, Android는 Chrome 기준. 태블릿 기종/OS 별 절차는 `docs/ROADMAP.md`의 키오스크 운영 항목에 정리.
- 추가 잠금이 필요하면(회원이 임의 종료·다른 앱 전환) Android Fully Kiosk Browser, iOS Guided Access를 도입 — MVP 범위 밖.

## 상태 관리
- **서버 상태**: React Query. 회원 검색·회원권 상태·체크인 이력은 쿼리/뮤테이션으로 관리.
- **클라이언트 상태**: `useState`/`useReducer`. 폼·토글·스텝 진행.
- **지속 상태**: `localStorage`에 태블릿의 `branchId`·관리자 access JWT·refresh JWT 저장. 지점 선택은 Context로 하위 컴포넌트에 전파.
- **권한 상태**: 관리자 역할(`global`/`branch`)은 로그인 응답에서 받아 Context로 보관, 라우트 가드·메뉴 표시에 사용.
- **토큰 갱신**: API fetch 래퍼가 401 시 자동으로 `POST /api/admin/refresh` 호출 → 새 access 토큰으로 원 요청 재시도. refresh도 401이면 강제 로그아웃.

## 인증 토큰 정책
- **access JWT** (만료 30분): 모든 인증 API 요청 헤더 `Authorization: Bearer ...`. 비밀키 `JWT_ACCESS_SECRET`.
- **refresh JWT** (만료 15시간): `POST /api/admin/refresh`에서만 사용. 비밀키 `JWT_REFRESH_SECRET`. claim에 고유 `jti`. 로그아웃·비번 변경·계정 soft delete·관리자 `branch_id` 변경 시 `revoked_refresh_tokens` 테이블에 jti를 INSERT해 무효화한다.
- 임시 비밀번호는 발급 시 `temp_password_expires_at = now() + 24h`. 만료 후 로그인 시 401 `TEMP_PASSWORD_EXPIRED`.

## 감사 로그(`admin_audit_logs`)
보안·운영 추적용. 미들웨어가 다음 액션을 자동 INSERT 한다 — 로그인 성공/실패, 로그아웃, 비번 변경, 비번 리셋, 관리자 CRUD, 지점 CRUD. 회원·회원권 관련 변경은 `membership_events` / `payments.performed_by`로 이미 추적되므로 본 테이블에 기록하지 않는다.

## 트랜잭션 retry
pgx 트랜잭션 헬퍼는 PostgreSQL 에러 코드 `40001`(serialization failure)·`40P01`(deadlock)을 만나면 최대 3회 자동 재시도(50ms·100ms·200ms backoff). 동시 체크인이 같은 회원권을 잠그거나, pause + bulk-extend가 같은 row를 만지는 등 짧은 충돌은 사용자에게 보이지 않게 자동 복구된다.

## 페이지네이션
- 목록 API(`/api/members`, `/api/check-ins?aggregate=raw`)는 cursor 기반.
- `?cursor=<base64>&limit=<int>` (기본 20, 최대 100). 응답에 `next_cursor`(다음 없음 = null).
- cursor는 JSON `{"t": "<RFC3339>", "id": <bigint>}`을 base64로 인코딩. 디코딩 실패 시 400 `INVALID_CURSOR`.
- 정렬 고정: `<timestamp> DESC, id DESC`. WHERE는 키셋 페이지네이션 `(timestamp, id) < (cursor.t, cursor.id)`.
- `aggregate=daily`는 페이지네이션 없음. `(from, to)` 간격 최대 92일.

## 멱등성(Idempotency-Key)
다음 엔드포인트는 모두 `Idempotency-Key` 헤더(클라이언트 발급 UUID) 필수 — 결제·연장 같은 위험 작업의 이중 호출 방지:
- `POST /api/members/:id/memberships` (회원권 부여 + 결제)
- `POST /api/memberships/:id/refund` (환불)
- `POST /api/memberships/bulk-extend` (대량 연장)

클라이언트는 폼 마운트 시 `crypto.randomUUID()`로 키 생성, 제출 시 헤더로 전송. 같은 키·같은 body 재호출은 첫 응답을 그대로 반환(처리는 한 번만). 같은 키인데 body가 다르면 409 `IDEMPOTENCY_KEY_CONFLICT`.

서버는 24시간 동안 `(key, admin_id, endpoint, request_hash, response_status, response_body)`를 `idempotency_keys` 테이블에 보관, 자정 배치가 정리.

## 회원권 미리 등록 (기간 겹침 차단)
회원의 다음 회원권을 만료 임박 시점에 미리 등록할 수 있다. 동시 활성을 막는 것은 "개수"가 아니라 "기간 겹침"이므로, `memberships`에 PostgreSQL EXCLUDE 제약(`daterange(start_date, end_date, '[]') WITH &&`)을 둬 active/paused 회원권의 기간이 겹치는 INSERT를 DB가 거부한다. 핸들러는 `23P01` 에러를 잡아 409 `MEMBERSHIP_PERIOD_OVERLAP`로 변환. 체크인은 `start_date <= 오늘 AND end_date >= 오늘 AND status='active'` 조건으로 잠그므로 미래 시작 회원권은 자동으로 체크인 후보에서 제외(시작 전 체크인 시 422 `MEMBERSHIP_NOT_STARTED`).

## 체크인 짧은 멱등성(이중 클릭 방지)
- `POST /api/check-ins`는 `(member_id, branch_id)`로 직전 5초 안의 성공 응답을 메모리 LRU(TTL 5초)에 보관, 같은 키 재호출은 새 row 생성 없이 캐시된 응답을 그대로 반환.
- 키오스크 디바운스는 1차 방어, 서버 캐시가 2차. 회원이 여러 명 빠르게 들어오는 정상 트래픽은 회원 ID가 다르므로 영향 없음.

## HTTPS / 환경 분리
- `APP_ENV=dev`(로컬): HTTP 허용. 프론트는 `http://localhost:5173`, API는 `http://localhost:8080`.
- `APP_ENV=prod`(운영): HTTPS 강제. 호스팅 플랫폼이 TLS 종료 후 백엔드로 전달. HSTS 헤더 추가. Web Speech API와 PWA 모두 HTTPS 필요.

## 인스턴스 토폴로지 (MVP)
MVP는 **백엔드 단일 인스턴스**를 가정한다. 다음 기능이 인스턴스 내 메모리·인-프로세스 상태에 의존한다:
1. **체크인 5초 LRU 멱등성 캐시** — 인스턴스별 메모리 LRU
2. **IP rate limit 토큰 버킷** — 인스턴스별 메모리
3. **인-프로세스 cron 자정 배치** — 인스턴스별 cron 스케줄러

다중 인스턴스로 확장 시 (1)(2)는 Redis로, (3)은 외부 스케줄러(Fly.io scheduled machines / Railway cron) 또는 PostgreSQL `pg_advisory_lock`으로 마이그레이션 필요. ADR-008 Redis 도입 트리거(refresh 검증 p99 > 100ms 또는 인스턴스 2개 이상)와 같이 검토.
