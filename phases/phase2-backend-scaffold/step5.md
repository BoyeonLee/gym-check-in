---
agent: backend
depends_on: [admins-branches]
---

# Step 5: 회원 CRUD + 키오스크 검색 + 오늘 체크인 카운트 + 공용 cursor 헬퍼

## 목표

회원 도메인의 관리자 CRUD와 키오스크의 검색·헤더 카운트를 한 step에 끝낸다. 또한 이후 `GET /api/check-ins`(step7)도 재사용할 **opaque cursor 인코딩 공용 헬퍼**를 여기서 한 번만 만든다.

산출물:
- `internal/http/cursor.go` — `EncodeCursor(t time.Time, id int64)` / `DecodeCursor(s string)` (공용)
- `internal/repo/members_repo.go` — CRUD + 검색 + cursor 페이지네이션
- `internal/http/members.go` — `GET/POST/PATCH/DELETE /api/members`, `GET /api/members/:id`
- `internal/http/kiosk.go` — `GET /api/members/search`, `GET /api/check-ins/today-count`
- 키오스크용 공개 `GET /api/branches` 분기 결정 (이 step에서 확정 — 인증 면제 별도 경로 또는 step4의 인증 라우트를 그대로 사용)

## 읽어야 할 파일

- `CLAUDE.md`, `backend/CLAUDE.md` — 회원·검색 섹션, 페이지네이션 규칙(`INVALID_LIMIT`/`INVALID_CURSOR`), 마스킹 정책, 키오스크 PII 노출 금지
- `db/CLAUDE.md` — members 테이블, `phone_last4`, `(branch_id, phone) where deleted_at is null` 부분 유니크
- `docs/API.md` — `/api/members*`, `/api/members/search`, `/api/check-ins/today-count` 명세, 에러 코드(`PHONE_DUPLICATE`, `QUERY_TOO_SHORT`, `INVALID_INPUT`, `INVALID_CURSOR`, `INVALID_LIMIT`, `RANGE_TOO_LARGE`)
- `docs/UI_GUIDE.md`, `docs/PRD.md` — 마스킹 표시 형식(`010-****-1234`, `**-04-15`, `#1234`)
- `docs/TESTING.md` — 회원/검색 테스트 카탈로그
- step1·2·3·4 산출물

## 작업

### 1. `internal/http/cursor.go` — 공용

```go
type Cursor struct {
    T  time.Time `json:"t"`   // RFC3339 (KST 또는 UTC — 인코딩 시점에 통일, 비교는 UTC)
    ID int64     `json:"id"`
}

func EncodeCursor(c Cursor) string                // base64url(json)
func DecodeCursor(s string) (Cursor, error)       // 형식 오류·필드 누락·타입 오류 시 apperr 400 INVALID_CURSOR

func ParseLimit(qs string, def, max int) (int, error)  // 빈 문자열 → def. 음수·0·max 초과 → 400 INVALID_LIMIT.
```

단위 테스트: round-trip, 잘못된 base64, JSON 누락 필드, 타입 오류, limit 경계값(-1/0/1/20/100/101).

### 2. `internal/repo/members_repo.go`

```go
type MemberRow struct {
    ID         int64
    BranchID   int64
    BranchName string  // JOIN
    Name       string
    Phone      string  // 11자리 — 관리자 응답용 풀 노출
    PhoneLast4 string
    BirthDate  time.Time  // date — 관리자 응답용
    DeletedAt  *time.Time
    CreatedAt  time.Time
    UpdatedAt  time.Time
}

// 관리자 목록. 지점 관리자는 자기 지점만(branchID 강제). 전역은 nil 가능.
type ListMembersInput struct {
    ScopeBranchID *int64       // RequireBranch 가드에서 채움. nil이면 전역.
    Cursor        *Cursor
    Limit         int
    BranchFilter  *int64        // 전역이 특정 지점만 보고싶을 때
    Q             *string        // 이름 LIKE '%q%' 또는 phone_last4 — 관리자 검색은 별도 라우트 X, 일단 미적용
}
func ListMembers(ctx, q Querier, in ListMembersInput) (rows []MemberRow, nextCursor *Cursor, err error)
// 정렬: created_at DESC, id DESC. WHERE (created_at, id) < (cursor.t, cursor.id) 키셋.
// limit+1 조회 → 마지막을 잘라 next cursor 결정.

func GetMember(ctx, q Querier, id int64, scopeBranchID *int64) (*MemberRow, error)
// 미존재·soft-deleted·다른 지점 → 모두 404로 통일(caller가 nil 받으면 404 반환).

type CreateMemberInput struct {
    BranchID  int64    // 지점 관리자는 자기 지점 강제(핸들러가 ScopeBranchID로 덮어씀)
    Name      string   // 1~100자
    Phone     string   // 정확히 11자리 숫자 (정규식)
    BirthDate time.Time
}
func InsertMember(ctx, q Querier, in CreateMemberInput) (int64, error)
// `(branch_id, phone) where deleted_at is null` 위반 → 409 PHONE_DUPLICATE.

type UpdateMemberInput struct {
    Name      *string
    Phone     *string
    BirthDate *time.Time
}
func UpdateMember(ctx, q Querier, id int64, in UpdateMemberInput, scopeBranchID *int64) error

func SoftDeleteMember(ctx, q Querier, id int64, scopeBranchID *int64, now time.Time) error

// 회원 단건 + active 회원권 + 회원권 이력(20개) + 결제 이력(부여+환불) — 한 번에 조회.
// SQL은 several round-trip 또는 한 트랜잭션 안에서 3 query. (LATERAL JOIN으로 합치는 것도 옵션.)
type MemberDetail struct {
    Member       MemberRow
    ActiveMembership *MembershipSummary
    Memberships  []MembershipSummary    // 최근 20개
    Payments     []PaymentRow            // 회원권 이력에 묶인 결제
}
func GetMemberDetail(ctx, q Querier, id int64, scopeBranchID *int64) (*MemberDetail, error)

// 키오스크 search: mode=name|phone|memberId. 활성 회원권이 있는 회원만.
type SearchInput struct {
    BranchID  int64
    Mode      string  // "name" | "phone" | "memberId"
    Q         string
    Today     time.Time   // KST 오늘 (SELECT FOR 활성 회원권 비교)
}
type SearchHit struct {
    ID              int64
    Name            string
    PhoneMasked     string  // 010-****-1234
    BirthMD         string  // **-04-15
    MemberIDDisplay string  // #1234
    LastCheckedInAt *time.Time   // 정렬 키
}
func SearchMembers(ctx, q Querier, in SearchInput) (hits []SearchHit, truncated bool, err error)
// limit 21 조회 → 21번째 있으면 truncated=true, 결과는 20개 자르기.
// 정렬: 마지막 check_ins.checked_in_at DESC NULLS LAST, id ASC (안정 정렬).
// 활성 회원권 조건: status='active' AND start_date <= today AND end_date >= today.
```

키오스크 검색 SQL 예시(개략):

```sql
SELECT m.id, m.name, m.phone, m.phone_last4, m.birth_date,
       (SELECT MAX(checked_in_at) FROM check_ins ci WHERE ci.member_id = m.id) AS last_ci
FROM members m
WHERE m.branch_id = $1
  AND m.deleted_at IS NULL
  AND ${MODE_CONDITION}
  AND EXISTS (
    SELECT 1 FROM memberships ms
    WHERE ms.member_id = m.id
      AND ms.status = 'active'
      AND ms.start_date <= $TODAY
      AND ms.end_date >= $TODAY
  )
ORDER BY last_ci DESC NULLS LAST, m.id ASC
LIMIT 21;
```

`MODE_CONDITION`:
- `name`: `m.name LIKE $q || '%'` (prefix 일치, 입력은 핸들러가 sanitize → `%`/`_` 이스케이프)
- `phone`: `m.phone_last4 = $q` (핸들러가 4자리 검증)
- `memberId`: `m.id = $q::bigint` (핸들러가 숫자 파싱)

### 3. `internal/http/members.go` — 5개 라우트

#### `GET /api/members`

- 인증 + must_change_password 가드 통과.
- 지점 관리자: `ScopeBranchID = c.GetInt64("branch_id")`. 전역: nil.
- query: `cursor`, `limit` (default 20, max 100), `branchId?` (전역만)
- 응답: `{ items: [{id, branch_id, branch_name, name, phone, phone_last4, birth_date, created_at, updated_at}], next_cursor }`
- **관리자 응답이라 phone/birth_date는 풀 노출** (마스킹 없음). 키오스크 search와 다름.

#### `GET /api/members/:id`

- 지점 스코프 강제. 다른 지점 → 404.
- `GetMemberDetail` 호출 → header(member) + active membership card + memberships(20개) + payments.

#### `POST /api/members`

- 지점 관리자: body의 `branch_id`를 자기 지점으로 덮어씀.
- 검증: name 1~100자, phone 정확 11자리(`^[0-9]{11}$`), birth_date NOT NULL.
- 23505 위반 → 409 `PHONE_DUPLICATE`.
- branch_id 미존재/soft-deleted → 400 `INVALID_INPUT`.
- 응답 201 + 생성 row.

#### `PATCH /api/members/:id`

- 지점 스코프. 다른 지점 → 404.
- body 화이트리스트: `{name?, phone?, birth_date?}`. **`branch_id`는 무시**(이전 불가).
- phone 변경 시 부분 유니크 위반 → 409 `PHONE_DUPLICATE`.
- 응답 200 + 갱신 row.

#### `DELETE /api/members/:id`

- 지점 스코프. 다른 지점 → 404.
- soft delete (`deleted_at = now()`).
- 응답 204.
- 활성 회원권이 있어도 삭제 가능(이후 검색·체크인에서 자동 제외).
- 회원 hard delete 금지(payments/check_ins FK 보호).

### 4. `internal/http/kiosk.go` — 2개 라우트

#### `GET /api/members/search`

- **인증 면제**(키오스크 라우트). `branchId` 쿼리 필수 — 누락 시 400 `INVALID_INPUT`.
- query: `branchId`, `mode=name|phone|memberId`, `q`.
- 검증:
  - `mode=name`: `len(q) >= 2` (UTF-8 rune 기준) — 미달 시 400 `QUERY_TOO_SHORT`.
  - `mode=phone`: `q` 정확 4자리 숫자 → 미달 시 400 `INVALID_INPUT`.
  - `mode=memberId`: `q`가 양의 정수 파싱 → 실패 시 400 `INVALID_INPUT`.
  - `mode` 외 값 → 400 `INVALID_INPUT`.
- 응답: `{ results: [{id, name, phone_masked, birth_md, member_id_display}], truncated: bool }`
- **마스킹 헬퍼** (`internal/util/mask.go` 새로 추가):
  - `MaskPhone(phone)`: 입력은 11자리 숫자 문자열. 출력은 `010-****-1234` 형식(앞 3자리 + 가운데 4자리 마스킹 + 뒤 4자리 그대로). 길이 11 미만이면 에러 반환.
  - `MaskBirthMD(date)`: 입력 `time.Time`. 출력은 `**-MM-DD` 형식(연도 마스킹).
  - `MemberIDDisplay(id int64)`: 출력 `#1234` 형식.
- 정렬은 서버에서 마지막 체크인 DESC NULLS LAST. 클라 재정렬 금지(주석으로 명시).
- 활성 회원권 없는 회원은 응답에서 제외.

#### `GET /api/check-ins/today-count`

- **인증 면제**(키오스크). query: `branchId`.
- SQL: `SELECT count(*) FROM check_ins WHERE branch_id = $1 AND (checked_in_at AT TIME ZONE 'Asia/Seoul')::date = (now() AT TIME ZONE 'Asia/Seoul')::date`
- 응답: `{ "count": 42 }`.

### 5. 공개 라우트 분리 — 결정 사항

**키오스크용 공개 라우트** = `GET /api/members/search`, `GET /api/check-ins/today-count`. 이 둘만 인증 면제.

`GET /api/branches`도 키오스크 초기 지점 선택에 필요 → 같은 공개 그룹에 둔다(이 step에서 결정). step4에서 만든 인증 그룹의 `GET /api/branches`는 그대로 두고, **공개 그룹에 같은 핸들러를 한 번 더 등록하지 않고**, 라우트를 공개 그룹으로 옮긴다(중복 라우트는 gin 기동 시 panic).

라우트 그룹 재구성:

```go
public := r.Group("/api")
public.GET("/branches", branches.List)              // 키오스크·관리자 공용
public.GET("/members/search", kiosk.SearchMembers)
public.GET("/check-ins/today-count", kiosk.TodayCount)
public.GET("/healthz", health.Get)

protected := r.Group("/api")
protected.Use(middleware.RequireAuth(...), middleware.MustChangePasswordGuard())
{
    // members CRUD, admins/branches CRU/D, memberships, check-ins POST/GET, sales (다음 step)
}
```

`GET /api/branches`가 공개로 옮겨졌다면 step4의 라우트 등록을 정리. 중복 라우트가 남으면 안 됨. **이 step에서 step4의 `GET /api/branches`를 공개 그룹으로 이동**(코드 정리).

### 6. 핸들러 테스트 — 정상 + 카탈로그 에러

각 라우트별:
- members: 지점 관리자 자기 지점만 보임, 다른 지점 자원 → 404, branch_name 포함, cursor 페이지(20개 + next_cursor + 마지막 페이지 next_cursor=null), limit 100 통과/101 → 400, 잘못된 cursor → 400, PATCH로 branch_id 보내도 무시(다른 필드만 갱신), phone 11자리 미만 → 400, phone 중복 → 409
- search: name 1자 → 400, name 2자 prefix 일치, phone 4자리 정확, memberId 숫자, 활성 회원권 없는 회원 제외, 21명 결과 → 20개 + truncated=true, 응답에 phone/birth 풀 미노출(마스킹 형식만)
- today-count: 정상, branchId 누락 → 400, KST 자정 경계는 통합 테스트(시계 주입)
- 공개 라우트는 인증 헤더 없이 200, 인증 헤더 무시(잘못된 토큰이라도 통과)
- 검색 응답에 phone(11자리 풀)/birth_date(YYYY-MM-DD) 풀 노출이 **없는지** 검증(정규식으로 grep)

## 핵심 규칙 (반드시 박는다)

- **키오스크 응답 PII 마스킹**: `MaskPhone`/`MaskBirthMD`/`MemberIDDisplay`만 노출. 풀 phone/birth_date는 응답·로그·에러 메시지에 절대 미포함.
- **관리자 응답은 풀 노출**: members 목록·상세·PATCH 응답은 phone(11자리 숫자 문자열, 하이픈·마스킹 없이 DB 원본) 그대로. 마스킹 X.
- **PATCH 화이트리스트**: members PATCH는 `name/phone/birth_date`만. branch_id 보내도 무시 — 핸들러에서 명시적 drop.
- **다른 지점 자원 404**: 존재 자체를 노출하지 마라(403 아닌 404).
- **soft-deleted = 미존재**: 모든 조회 `deleted_at IS NULL`.
- **search 정렬은 서버에서**: 클라가 재정렬 못 하게 응답에 정렬 키(last_checked_in_at) 노출 안 함. 결과 순서만 의미.
- **활성 회원권 없는 회원 제외**: search SQL의 EXISTS 조건. 회원 자체 풀 결과에서 모두 제외.
- **prefix LIKE 인젝션 방어**: `q`의 `%`/`_`/`\\`을 escape. parameterized query만 사용.
- **branchId 누락 거부**: search/today-count는 둘 다 branchId 필수.
- **공개 라우트도 rate limit 통합**: step2의 IP 토큰 버킷이 공개 라우트도 보호하도록 라우트 그룹 미들웨어 점검.
- **search 응답에 활성 회원권 정보 노출 금지**: `MEMBERSHIP_NOT_STARTED` 분기는 체크인 핸들러(step7)의 일.

## Acceptance Criteria

```bash
set -a; source ../../.env; set +a
export TEST_DATABASE_URL="${TEST_DATABASE_URL:-$DATABASE_URL}"

cd backend
go vet ./...
go build ./...
go test -short -race ./...
go test -race -tags=integration ./...

# 공개 라우트 인증 면제 확인
go build -o bin/server ./cmd/server
PORT=18080 APP_ENV=dev ./bin/server &
SERVER_PID=$!
sleep 1

curl -fsS "http://localhost:18080/api/branches" >/dev/null
curl -fsS "http://localhost:18080/api/members/search?branchId=1&mode=phone&q=1234" >/dev/null
curl -fsS "http://localhost:18080/api/check-ins/today-count?branchId=1" >/dev/null

# 인증 필요 라우트가 인증 없이 401인지
test "$(curl -s -o /dev/null -w '%{http_code}' http://localhost:18080/api/members)" = "401"

kill $SERVER_PID
wait $SERVER_PID 2>/dev/null || true
```

자가 점검:
- members 페이지네이션: 25명 시드 → cursor=null로 첫 호출 → 20개+next_cursor → 두 번째 호출 → 5개+next_cursor=null
- members PATCH로 branch_id 변경 시도 → 무시(원래 branch_id 유지)
- search 결과의 phone에 `-`가 정확히 2개 포함(`010-****-1234` 형식), birth가 `**-`로 시작
- 활성 회원권 없는 회원 5명, 있는 회원 3명 → search 결과 3명만
- search 21명 결과 → truncated=true, items 20개

## 검증 절차

1. AC 명령 직접 실행.
2. `code-reviewer` 서브에이전트 호출. 입력: 단계 이름(`phase2-backend-scaffold/members-kiosk`), `git diff HEAD --stat`. PASS 응답 필요.
3. step5 status 업데이트:
   - PASS → `"status": "completed"` + `"summary": "공용 cursor 헬퍼(EncodeCursor/DecodeCursor/ParseLimit) + members CRUD(cursor 페이지·branch_name·PATCH 화이트리스트) + members 단건(active+이력+결제) + 키오스크 검색(mode=name/phone/memberId·활성 회원권 필터·마스킹·truncated) + today-count + GET /api/branches 공개 그룹으로 이동."`

## 금지사항

- `frontend/`·공유 파일 변경 금지.
- 키오스크 응답에 풀 phone/birth_date 노출 금지.
- members PATCH 화이트리스트에 `branch_id` 추가 금지.
- search 결과를 클라가 재정렬할 수 있도록 정렬 키를 응답에 노출 금지.
- 다른 지점 자원에 403 반환 금지(404로 통일).
- 검색 prefix LIKE에 사용자 입력의 `%`/`_` 이스케이프 누락 금지(인젝션·DoS 방어).
- ADR 외 라이브러리 추가 금지.
- step6의 회원권 라우트, step7의 체크인/매출 라우트 만들지 마라.
- 공개 라우트에서 rate limit 우회 금지(공개 라우트도 IP 한도 적용).
- 중복 라우트 등록 금지(step4의 `GET /api/branches`를 정리해 한 곳에서만 등록).
