# Architecture Decision Records

## 철학
MVP 속도를 최우선으로 한다. 단일 바이너리 배포, 외부 의존 최소, 데이터는 중앙에서 한 벌만 보관. 작동하는 최소 구현부터 시작해 운영 피드백으로 확장한다.

---

### ADR-001: 백엔드는 Go + Gin
**결정**: HTTP API 서버를 Go로 작성하고 웹 프레임워크는 Gin을 사용한다. DB 드라이버는 `pgx`, 비밀번호 해시는 `bcrypt`, 인증 토큰은 JWT.
**이유**:
- 단일 정적 바이너리로 컨테이너/클라우드에 배포하기 쉬움.
- Go는 키오스크 체크인 API의 낮은 응답 지연·낮은 메모리에 유리.
- Gin은 Go 웹 생태계에서 가장 대중적이며 미들웨어·바인딩 등 MVP에 필요한 기능이 충분.
**트레이드오프**: Node/TypeScript 대비 생태계·써드파티 모듈이 적고, 프론트와 언어가 갈라져 타입 공유가 수동(OpenAPI 등으로 보완 가능).

### ADR-002: 클라우드 중앙 호스팅
**결정**: API 서버(Go/Gin)와 DB(PostgreSQL)를 클라우드(Fly.io / Railway / Render 등 중 택일)에 중앙 배포하고, 모든 지점 태블릿·관리자 기기가 같은 엔드포인트에 접속한다.
**이유**:
- 지점이 여러 개인 체인 운영 전제. 사장님(전역 관리자)은 전 지점 데이터를 한 곳에서 봐야 함.
- 지점별 PC 자체 호스팅은 각 지점마다 설치·업데이트·백업·동기화 부담이 곱해짐.
- 태블릿은 브라우저만 있으면 되므로 지점 추가 비용이 거의 0.
**트레이드오프**: 네트워크 단절 시 체크인 불가. 오프라인 큐잉은 MVP 범위 밖.

### ADR-003: 회원용 디바이스는 지점별 공용 태블릿(키오스크)
**결정**: 회원은 입구에 설치된 공용 태블릿 브라우저에서만 체크인한다. 개인 스마트폰 앱/로그인은 만들지 않는다.
**이유**:
- 앱 설치·회원가입·로그인 마찰 없이 회원이 즉시 사용 가능.
- 코치가 이름을 물어 체크해주던 기존 UX를 가장 적은 변화로 대체.
- 브라우저 기반이라 Android·iPad 등 태블릿 기종 선택이 자유.
**트레이드오프**: 회원 본인 인증 강도는 낮음(전화번호는 식별자일 뿐). 필요 시 현장 코치가 최종 확인한다.

### ADR-004: 결제 정보는 별도 `payments` 테이블로 분리
**결정**: 회원권 부여 시 입력하는 결제 금액·수단(현금/카드)·결제일을 `memberships` 컬럼이 아닌 별도 `payments` 테이블에 저장한다. `payments`는 `membership_id` FK를 갖고, 한 회원권에 다수 결제 row가 가능하다.
**이유**:
- 매출 집계(일/월·수단별·지점별)가 단일 테이블 `SUM(amount)`로 단순해진다.
- 향후 환불 시 음수 금액 row 추가, 분할 결제, 쿠폰 적용 등 확장이 자연스럽다.
- `memberships`는 회원권 사용 상태(기간·잔여·정지)에 집중하고 회계는 `payments`가 담당해 책임 분리.
**트레이드오프**: 단순 조회에 join 한 번 추가. MVP 단계에서 결제는 회원권 부여 트랜잭션 안에서 1:1로 생성되므로 비용은 무시할 수준.

### ADR-005: 키오스크 풀스크린은 PWA + 홈 화면 추가
**결정**: 태블릿 회원 화면에 브라우저 UI(주소창·탭)가 보이지 않도록 프론트엔드를 PWA로 배포하고, `manifest.webmanifest`에 `"display": "fullscreen"`을 지정해 태블릿 홈 화면 아이콘으로 실행하게 한다.
**이유**:
- 추가 라이선스·앱 설치 없이 무료. iOS/Android 둘 다 표준 지원.
- 회원이 보는 화면을 일반 웹페이지가 아닌 "전용 앱처럼" 보이게 해 신뢰감을 높이고 실수로 주소창을 건드릴 여지를 줄인다.
- 그대로 같은 URL로 관리자 화면도 서비스되므로 배포 일원화.
**트레이드오프**: 회원이 시스템 제스처로 앱을 닫거나 다른 사이트로 이동할 가능성은 남는다. 강한 잠금이 필요하면 Android Fully Kiosk Browser, iOS Guided Access 도입(운영 강화 단계).

### ADR-006: 관리자 비밀번호 변경 본인 인증은 "현재 비밀번호 재입력"
**결정**: 본인 비밀번호 변경 시 별도 이메일·SMS·2FA를 도입하지 않고 **현재 비밀번호 재입력**만으로 본인 인증한다. 비밀번호 분실은 (1) 전역 관리자가 지점 관리자에게 임시 비밀번호 발급(`must_change_password=true` 자동 세팅), (2) 전역 관리자 본인 분실은 운영자가 DB에서 직접 해시 갱신하는 절차로 처리한다.
**이유**:
- 운영 규모가 매우 작다(전역 관리자 1명 + 지점장 몇 명). 셀프서비스 리셋 인프라는 과투자.
- 무료 SMTP 옵션(Gmail App Password, AWS SES 샌드박스, Resend 무료 티어)도 발신 도메인·SPF/DKIM·전송 한도 등 운영 부담이 작지 않다.
- Google·GitHub 등 표준 패턴(이미 로그인된 세션에서 현재 비번 재입력)을 따른다.
**트레이드오프**: 전역 관리자가 비번을 잊으면 운영자가 DB에 접근할 수 있어야 한다. 관리자 수가 늘면 이메일 기반 셀프서비스 리셋을 추가한다.

### ADR-007: 모든 도메인 엔티티는 soft delete
**결정**: `branches`, `members`, `admins` 모두 hard delete 대신 `deleted_at timestamptz` 컬럼으로 soft delete 처리. `memberships`는 `status` 컬럼(`refunded`/`expired`)으로 종료 상태를 표현하므로 `deleted_at` 불필요. `check_ins`/`payments`/`membership_events`는 이력성으로 삭제 자체가 없다.
**이유**:
- 매출(`payments`)·체크인 이력은 회원·회원권을 FK로 참조한다. 회원을 hard delete하면 이력이 끊기거나 FK 위반.
- 실수 복구·법적 분쟁 시 기록 추적이 가능해야 한다.
- 모든 조회에 `deleted_at IS NULL` 필터 강제로 일관된 정책.
**트레이드오프**: 모든 unique 제약과 카운팅 쿼리는 `WHERE deleted_at IS NULL`을 같이 써야 한다. 부주의로 빠뜨리면 "삭제된 회원이 검색에 나타남" 같은 버그가 발생할 수 있어 코드 리뷰에서 점검 필요.

### ADR-008: 관리자 토큰은 access(30분) + refresh(15시간) 분리
**결정**: 관리자 세션은 짧은 access JWT(30분)로 모든 API 요청을 인증하고, 긴 refresh JWT(15시간)로 access 갱신만 담당한다. 두 토큰은 다른 비밀키(`JWT_ACCESS_SECRET`, `JWT_REFRESH_SECRET`)로 서명. 로그아웃·비번 변경·계정 삭제 시 refresh 토큰을 서버측에서 무효화한다.
**이유**:
- access만 단일 토큰으로 두면 만료를 길게 잡아야 하고, 그러면 키오스크/공용 기기에서 노출 시 피해가 크다.
- refresh로 쪼개면 access는 짧게(30분) 잡아도 UX가 안 깨지고, 의심 시 refresh만 무효화하면 자동 로그아웃된다.
- 매일 출근 후 1번 로그인하면 15시간으로 하루 영업이 커버된다.
**트레이드오프**: refresh 무효화 목록(`revoked_refresh_tokens`)을 DB 또는 in-memory로 운영해야 한다. MVP에선 DB 테이블 1개로 처리하고, 트래픽이 늘면 Redis로 옮긴다.
**Redis 도입 트리거**: refresh 검증 p99 latency가 100ms를 넘거나, 백엔드 인스턴스를 2개 이상 운영하기 시작하면(체크인 5초 멱등성 캐시도 같이 분산 필요) 검토.

### ADR-009: bulk-extend는 Idempotency-Key 헤더 필수
**결정**: `POST /api/memberships/bulk-extend`는 `Idempotency-Key` 헤더(클라이언트 발급 UUID)가 필수다. 서버는 24시간 동안 같은 키의 첫 응답을 저장(`idempotency_keys` 테이블)하고, 같은 키·같은 body 재호출 시 재실행 없이 첫 응답을 그대로 반환한다. 같은 키·다른 body는 409 `IDEMPOTENCY_KEY_CONFLICT`.
**이유**:
- 대량 연장은 잘못 적용하면 수백 명의 만료일을 추가로 밀어낸다(롤백이 쉽지 않음).
- 네트워크 재시도·이중 클릭으로 두 번 적용되는 경우를 클라이언트 confirm만으로 막기는 부족하다(Stripe·Square 등 결제 API의 표준 패턴).
- 클라이언트는 폼 마운트 시 `crypto.randomUUID()`로 키 발급, 폼 초기화 시 새 키 발급.
**트레이드오프**: 테이블 1개와 자정 정리 잡 추가. 다른 위험 작업(`POST /api/members/:id/memberships`, `POST /api/memberships/:id/refund`)도 같은 패턴으로 확장 가능하지만 MVP에선 bulk-extend에만 적용.

### ADR-010: 호스팅 플랫폼 결정 (TBD)
**결정**: Phase 4에서 Fly.io / Railway / Render 중 비교 후 결정.
**이유**: 단일 정적 바이너리 + Postgres 매니지드 옵션 + 자동 HTTPS + 자동 백업이 모두 제공되는 작은 플랫폼이 MVP에 적합.
**트레이드오프**: Phase 4 진입 후 확정. 본 항목은 placeholder로 둔다.

### ADR-011: 관리자 액션은 별도 audit log 테이블에 기록한다
**결정**: 보안·운영 추적을 위해 `admin_audit_logs` 단일 테이블을 두고, 다음 액션을 미들웨어가 자동 INSERT 한다 — `login_success`, `login_failure`, `logout`, `password_change`, `password_reset`, `admin_create`/`admin_update`/`admin_delete`, `branch_create`/`branch_update`/`branch_delete`. 회원·회원권 변경은 `membership_events` / `payments.performed_by`가 이미 추적하므로 본 테이블에 기록하지 않는다(중복).
**이유**:
- 누가 누구에게 임시 비번을 발급했는지, 누가 지점을 삭제했는지 등은 외부 감사·내부 사고 조사에서 가장 먼저 묻는 정보다.
- 미들웨어 1곳에서 자동 기록하면 핸들러마다 호출하는 부담이 없다.
- 회원·지점 빈도가 다르므로(회원 CRUD는 빈번, 지점 CRUD는 드묾) 관리자 액션 + 지점 CRUD만 audit, 회원 CRUD는 빠진다.
**트레이드오프**: 테이블 한 개 추가. 보존 기간은 MVP에서 무기한, 트래픽이 늘면 파티셔닝/아카이빙(Phase 5+).

### ADR-012: 트랜잭션은 serialization/deadlock 시 자동 retry
**결정**: pgx 트랜잭션 헬퍼는 PostgreSQL 에러 코드 `40001`(serialization failure)·`40P01`(deadlock)을 만나면 최대 3회 자동 재시도한다(50ms·100ms·200ms backoff). 그래도 실패하면 500.
**이유**:
- 동시 체크인이 같은 회원권을 잠그거나 pause + bulk-extend가 같은 row를 만지는 등 짧은 충돌은 사용자가 알아챌 수준이 아니어야 한다.
- 표준 패턴으로, 핸들러 코드를 더럽히지 않고 헬퍼에서 처리.
**트레이드오프**: 멱등하지 않은 사이드 이펙트(외부 호출 등)를 트랜잭션에 넣으면 retry로 두 번 실행될 위험. MVP는 외부 호출이 없어 안전.

### ADR-013: 체크인 이중 클릭 방지는 5초 메모리 멱등성 캐시
**결정**: `POST /api/check-ins`는 `(member_id, branch_id)` 기준으로 직전 5초 안의 성공 응답을 메모리 LRU 캐시(TTL 5초)에 보관, 같은 키 재호출은 새 row 생성 없이 캐시된 응답을 그대로 반환한다.
**이유**:
- 키오스크 회원이 빠르게 두 번 누르면 `check_ins` row가 2개 생기고 today-count가 +2로 표시되는 부작용. 횟수권 차감은 1번이지만 카운터 표기는 어색.
- bulk-extend처럼 24h 멱등성 키를 강제하기엔 회원 측이 키를 보낼 수 없고, 체크인은 빈도가 매우 높아 DB 테이블 멱등성은 부담.
- 5초는 디바운스 + 사용자 재시도 사이의 자연스러운 윈도우.
**트레이드오프**: 서버 재시작 시 캐시가 초기화되어 그 순간의 중복 호출은 막지 못한다(빈도 매우 낮음, 영향 무시 가능). 멀티 인스턴스 배포 시 인스턴스별 캐시라 같은 회원이 다른 인스턴스에 빠르게 두 번 들어갈 가능성도 있지만, 키오스크가 보통 IP 단위로 같은 인스턴스에 라우팅되어 실제 영향은 작다. 트래픽이 늘면 Redis로 옮긴다.

### ADR-014: 회원권은 기간 겹침이 없으면 미리 등록 가능
**결정**: 회원당 active/paused 회원권의 "개수 1개" 제약(부분 unique 인덱스)을 폐기하고, 대신 **기간 겹침 차단**(PostgreSQL EXCLUDE + `daterange WITH &&`)으로 바꾼다. 만료 임박한 회원이 다음 회원권을 미리 결제하는 케이스를 지원한다(예: 5/30 만료 active 보유 + 6/1~ 시작 회원권 미리 등록). 체크인 핸들러는 `start_date <= 오늘 AND end_date >= 오늘 AND status='active'` 조건으로 잠그므로 미래 시작 회원권은 자동으로 체크인 후보에서 제외, 시작 전 체크인 시 422 `MEMBERSHIP_NOT_STARTED`.
**이유**:
- 운영 현실: 회원이 만료 직전에 갱신 결제를 하는 경우가 많은데, "기존 만료 후에만 등록 가능"이면 만료일에 운영자가 즉시 응대해야 하는 부담.
- "동시에 활성인 회원권은 1개"가 진짜 의도이므로 개수가 아닌 기간으로 제약하는 게 정확하다.
- DB의 EXCLUDE 제약은 동시 부여 트랜잭션의 race condition까지 막는다.
**트레이드오프**: `btree_gist` extension 필요. 핸들러가 `23P01`(exclusion_violation) 에러 코드를 잡아 409 응답으로 변환해야 함. ON DELETE CASCADE 같은 일반 FK와 달리 EXCLUDE 위반 코드를 명시적으로 처리해야 한다.

### ADR-015: admin DELETE 시 access JWT도 즉시 무효화
**결정**: Auth 미들웨어가 access claim 검증 후 매 요청마다 `SELECT 1 FROM admins WHERE id=? AND deleted_at IS NULL`로 admin row 존재를 확인한다. soft-deleted admin의 access는 30분 자연 만료를 기다리지 않고 즉시 401.
**이유**:
- access만 단순 stateless로 두면 admin 삭제 후 30분간 권한 행사 가능 — 보안 사고 시 즉시 차단이 어려움.
- refresh는 `revoked_refresh_tokens`에 jti를 INSERT해 무효화하지만, access는 jti가 없고 서버측 무효화 통로가 없었다.
- 매 요청 1쿼리(PK 조회)는 관리자 트래픽 규모에서 무시 가능한 비용. 회원·체크인은 admin 검증 대상이 아니므로 영향 없다(체크인 라우트는 인증 자체가 없거나 별개 흐름).
**트레이드오프**: 관리자 액션마다 DB 1회 추가 조회. 성능 부담은 작지만 캐시(LRU TTL 30초)로 추가 최적화 가능 — MVP에서는 캐시 없이 직접 조회.

### ADR-016: MVP는 백엔드 단일 인스턴스 가정
**결정**: MVP는 백엔드를 단일 인스턴스로 운영한다. 체크인 5초 LRU 캐시·IP rate limit·인-프로세스 cron 자정 배치가 인스턴스별 메모리에 의존한다. 다중 인스턴스 확장은 Phase 5+에서 Redis 도입과 함께 진행.
**이유**:
- 사용자 ~20명, 체크인 트래픽 분당 수십 건 수준. 단일 Go 인스턴스(2 vCPU / 1GB)면 충분.
- 멀티 인스턴스 도입은 Redis·외부 스케줄러·세션 동기화 등 운영 복잡도가 곱해짐 — MVP 속도와 정면 충돌.
- ADR-008 Redis 트리거(p99 > 100ms 또는 인스턴스 2개 이상)와 함께 도입.
**트레이드오프**: 인스턴스 재시작 시 5초 캐시·rate limit 카운터 초기화. 자정 배치가 인스턴스 다운 시 누락 가능 — `./bin/server batch run-expiry` 수동 실행으로 복구(OPERATIONS.md). 다중 인스턴스 시 (1) 캐시·rate limit Redis로 이전, (2) 자정 배치는 외부 cron 또는 `pg_advisory_lock`으로 직렬화.
