---
agent: frontend
---

# Step 9: 관리자 — 회원권 상세 + 부여 폼

## 목표

회원권 상세 페이지(`/admin/memberships/:id`)와 부여 폼(`/admin/members/:memberId/memberships/new`)을 만든다. 부여 폼은 Idempotency-Key + 결제 입력 + 미래 시작 미리등록까지 포함해 step 6a에 해당하는 무거운 폼.

이 step이 끝나면:
- `/admin/memberships/:id` 상세 페이지가 `GET /api/memberships/:id`로 status·pause_used·pause_start_date·end_date + 결제 이력 + membership_events 이력을 한 화면에 그린다.
- 부여 폼이 monthly(`1/3/6/12` 프리셋 + 직접 입력) / pass10 분기, 결제(amount>0, 현금/카드), `start_date` 오늘/미래만, 만료일 미리보기, `Idempotency-Key` 헤더 첨부.
- 미래 회원권 미리 등록 가능 — 현재 active 있어도 기간 안 겹치면 OK.
- 에러 분기: `INVALID_START_DATE`, `MEMBERSHIP_PERIOD_OVERLAP`, `INVALID_AMOUNT`.

## 읽어야 할 파일

- `frontend/CLAUDE.md` — 부여 폼 정책, 미리등록·만료일 미리보기, paid_at 서버 자동
- `backend/CLAUDE.md` — 회원권 부여 트랜잭션, EXCLUDE 제약, INVALID_START_DATE
- `docs/API.md` — `POST /api/members/:id/memberships`, `GET /api/memberships/:id`
- `frontend/ui-design/admin-plan-grant.jsx` — 부여 폼 시안
- step 3 (useIdempotencyKey, apiFetch), step 8 (회원 상세에서 부여 진입)

## 작업

### 1. `src/api/memberships.ts`

```ts
export interface MembershipDetail {
  membership: MembershipHistory  // step 8에 정의
  member: { id: number; name: string; branch_id: number }
  payments: PaymentHistory[]
  events: MembershipEvent[]
}

export interface MembershipEvent {
  id: number
  action: 'pause' | 'unpause' | 'cancel_pause' | 'refund' | 'bulk_extend'
  reason: string
  pause_start_date?: string
  pause_end_date?: string
  actual_pause_end?: string
  extend_days?: number
  performed_by_username: string
  created_at: string
}

export async function getMembership(id: number) {
  return apiFetch<MembershipDetail>(`/api/memberships/${id}`)
}

export interface GrantBody {
  type: 'monthly' | 'pass10'
  months?: number  // monthly
  start_date: string
  amount: number
  method: 'cash' | 'card'
}

export async function grantMembership(memberId: number, body: GrantBody, idempotencyKey: string) {
  return apiFetch<MembershipDetail['membership']>(`/api/members/${memberId}/memberships`, {
    method: 'POST', body, idempotencyKey
  })
}
```

### 2. `src/lib/dates.ts` — 만료일 계산 미리보기

```ts
export function addMonths(yyyymmdd: string, months: number): string {
  const [y, m, d] = yyyymmdd.split('-').map(Number)
  const dt = new Date(Date.UTC(y, m - 1 + months, d))
  // 월말 보정(예: 1/31 + 1month = 2/28 등) — 백엔드 정책과 일치시키기. 백엔드는 PostgreSQL의 `+ months month`를 사용하므로 동일 의도.
  return dt.toISOString().slice(0, 10)
}

export function todayKST(): string {
  // KST 기준 오늘 날짜 (YYYY-MM-DD)
  const now = new Date()
  const kst = new Date(now.getTime() + 9 * 60 * 60 * 1000)
  return kst.toISOString().slice(0, 10)
}
```

단위 테스트: 월말 경계, 윤년.

### 3. `src/pages/admin/Memberships/Detail.tsx`

상태:
- `useQuery(['membership', id], () => getMembership(id))`.

UI:
- 헤더: 회원 이름(링크 → `/admin/members/:memberId`) + 회원권 type/기간.
- 상태 카드: status·pause_used·pause_start_date·end_date.
- 액션 버튼 (조건부 노출):
  - status='active' + pause_used=false → "정지 등록" (step 10)
  - status='paused' → "조기 활성화" (unpause, step 10)
  - status='active' + pause_used=true + pause_start_date > 오늘 → "미래 정지 취소" (cancel-pause, step 10)
  - status in ('active','paused') → "환불" (step 10)
  - status='expired'/'refunded' → 액션 없음 + 안내 텍스트
- 결제 이력 표 (부여 양수, 환불 음수).
- 이벤트 이력 표 (pause/unpause/cancel_pause/refund/bulk_extend + reason + performed_by).

### 4. `src/pages/admin/Memberships/Grant.tsx` — 부여 폼

라우트: `/admin/members/:memberId/memberships/new`.

상태:
- `type`: `'monthly' | 'pass10'`
- `months`: `1 | 3 | 6 | 12 | custom-int` (monthly만)
- `startDate`: 기본 = 회원의 현재 active 회원권 `end_date + 1일`, 없으면 오늘.
- `amount`: 양수 정수.
- `method`: `'cash' | 'card'`.
- `useIdempotencyKey()` — 마운트 시 발급, 성공 후 regenerate.

처리:
- 시작일 검증: `startDate < todayKST()` → 폼 비활성 + 인라인 "오늘 이후만 가능".
- amount 검증: `<= 0` 또는 비정수 → 비활성.
- 만료일 미리보기: type=monthly면 `addMonths(startDate, months)`, pass10이면 `addMonths(startDate, 2)`.
- 제출:
  - `grantMembership(memberId, { type, months, start_date, amount, method }, idempotencyKey)`.
  - 성공: regenerate idempotency key + `queryClient.invalidateQueries(['member', memberId])` + navigate to `/admin/members/:memberId`.
  - 실패 분기:
    - `INVALID_START_DATE` (400): "시작일은 오늘 또는 이후만 가능합니다" 인라인.
    - `MEMBERSHIP_PERIOD_OVERLAP` (409): "기간이 기존 회원권과 겹칩니다. 시작일을 조정하세요" 인라인.
    - `INVALID_AMOUNT` (400): "금액은 0보다 커야 합니다" 인라인.
    - `IDEMPOTENCY_KEY_CONFLICT` (409): "이미 다른 내용으로 제출된 키입니다" + regenerate.
    - 그 외: 토스트.

UI 시안: `admin-plan-grant.jsx` 참조 — 카드 분리(type 선택 / 결제 / 미리보기), 큰 버튼.

Confirm 모달:
- 제출 전 confirm 모달로 "회원: X / type: monthly N개월 / 결제 금액·수단 / 시작일 / 만료일 미리보기"를 보여주고 확인 → 실제 제출.
- 처리 중에는 버튼 disabled + 스피너.

### 5. 컴포넌트 테스트

- `dates.test.ts`: addMonths, todayKST.
- `Memberships/Detail.test.tsx`: status별 액션 버튼 노출 분기, 결제/이벤트 이력 렌더.
- `Memberships/Grant.test.tsx`:
  - monthly 프리셋 선택 → 만료일 미리보기.
  - pass10 → months 필드 숨김, 자동 2개월.
  - amount=0 비활성.
  - 시작일 어제 → 비활성 + 인라인.
  - 제출 성공 → idempotency key regenerate + navigate.
  - MEMBERSHIP_PERIOD_OVERLAP → 인라인.
  - 같은 키로 두 번 제출(이중 클릭 시뮬) → 첫 제출만 보냄.

## 핵심 규칙

- **Idempotency-Key 필수**: 마운트 시 발급, 성공 후 regenerate. 폼이 살아있는 동안 같은 key 재사용(이중 클릭 시 백엔드 첫 응답 반환).
- **paid_at 클라 미전송**: 서버 자동. body에 포함시키지 마라.
- **branch_id 클라 미전송**: 서버가 member의 branch_id로 자동.
- **confirm 모달 + 버튼 비활성**: 1차 방어. Idempotency-Key가 2차.
- **만료일은 미리보기만**: 백엔드가 실제 계산. 클라의 addMonths는 UX 보조.
- **무료/0원 결제 금지**: amount > 0 강제.

## Acceptance Criteria

```bash
cd frontend && pnpm lint && pnpm build && pnpm test
```

수동:
- 회원 상세 → "회원권 부여" → 폼 → monthly 3개월 + 카드 100000원 → 제출 → 회원 상세 + active 카드.
- 같은 회원에 다시 부여 → 미래 시작일로 → 성공.
- 시작일을 현재 active의 기간 안으로 → 409 → 인라인.

## 검증 절차

1. AC.
2. step9 status 갱신.

## 금지사항

- amount 0/음수 허용 금지.
- paid_at·branch_id를 클라에서 보내지 마라.
- Idempotency-Key 없이 grant 요청 보내지 마라(헤더 누락 시 400).
- 결제 정보(금액·수단)를 키오스크 응답·로그에 노출하지 마라(이 step에선 관리자 화면 한정, 키오스크는 무관).
- 폼 마운트마다 새 키를 발급하지 말고, **성공 후에만** regenerate(이중 클릭이 같은 키로 들어가야 안전망 동작).
