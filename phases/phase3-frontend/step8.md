---
agent: frontend
---

# Step 8: 관리자 — 회원 (목록·등록·수정·상세)

## 목표

관리자 회원 관리 화면 4종. 백엔드의 `/api/members` CRUD + `/api/members/:id` 단건(active 회원권 + 이력)을 모두 노출.

이 step이 끝나면:
- `/admin/members` 목록: cursor 페이지네이션(20개씩 무한 스크롤), 검색(이름/전화), 지점 필터(전역 관리자만), 신규 등록 버튼.
- `/admin/members/new` 등록 폼: 이름·전화 11자리·생년월일·지점(전역 관리자는 선택, 지점 관리자는 자기 지점 강제).
- `/admin/members/:id` 회원 상세: 헤더(이름·전화·생년월일·지점) + active 회원권 카드 + 회원권 이력 표(최근 20개) + 결제 이력 표. 페이지네이션 없음(서버가 한 번에 내려줌).
- `/admin/members/:id/edit` 수정 폼: 이름·전화·생년월일만(`branch_id` 비활성).
- 삭제 버튼(soft delete) 확인 모달.

## 읽어야 할 파일

- `frontend/CLAUDE.md` — 풀 PII 표시, 전화 11자리·`010-1234-5678` 포맷, 회원 수정 폼에서 branch_id 비활성, cursor 페이지네이션, branch_name 노출
- `docs/API.md` — `/api/members` CRUD 응답·요청·에러 코드 (`PHONE_DUPLICATE` 등)
- `backend/CLAUDE.md` — 페이지네이션 cursor 정책, 지점 관리자 branch_id 강제
- `frontend/ui-design/admin-members.jsx` — 회원 화면 시안
- step 3 산출물 (apiFetch), step 4 산출물 (AdminLayout)

## 작업

### 1. `src/api/members.ts` 확장

```ts
export interface Member {
  id: number
  branch_id: number
  branch_name: string
  name: string
  phone: string         // "01012345678" 11자리
  birth_date: string    // "1990-04-15"
  created_at: string
  updated_at: string
}

export interface MembershipHistory {
  id: number
  type: 'monthly' | 'pass10'
  start_date: string
  end_date: string
  status: 'active' | 'paused' | 'refunded' | 'expired'
  remaining?: number
  months?: number
  pause_used: boolean
  pause_start_date?: string | null
  pause_end_date?: string | null
}

export interface PaymentHistory {
  id: number
  membership_id: number
  amount: number       // 양수=부여, 음수=환불
  method: 'cash' | 'card'
  paid_at: string
}

export interface MemberDetail {
  member: Member
  active_membership: MembershipHistory | null
  memberships: MembershipHistory[]
  payments: PaymentHistory[]
}

export async function listMembers(opts: { cursor?: string; limit?: number; branchId?: number; q?: string }) {
  const qs = new URLSearchParams()
  if (opts.cursor) qs.set('cursor', opts.cursor)
  if (opts.limit) qs.set('limit', String(opts.limit))
  if (opts.branchId) qs.set('branchId', String(opts.branchId))
  if (opts.q) qs.set('q', opts.q)
  return apiFetch<{ items: Member[]; next_cursor: string | null }>(`/api/members?${qs}`)
}

export async function getMember(id: number) {
  return apiFetch<MemberDetail>(`/api/members/${id}`)
}

export async function createMember(body: { name: string; phone: string; birth_date: string; branch_id: number }) {
  return apiFetch<Member>('/api/members', { method: 'POST', body })
}

export async function updateMember(id: number, body: { name?: string; phone?: string; birth_date?: string }) {
  return apiFetch<Member>(`/api/members/${id}`, { method: 'PATCH', body })
}

export async function deleteMember(id: number) {
  return apiFetch(`/api/members/${id}`, { method: 'DELETE' })
}
```

### 2. `src/lib/format.ts` 확장 — 포맷 헬퍼

```ts
export function formatPhone(raw: string): string {
  // "01012345678" → "010-1234-5678". 11자리 미만은 그대로.
  if (!/^[0-9]{11}$/.test(raw)) return raw
  return `${raw.slice(0,3)}-${raw.slice(3,7)}-${raw.slice(7)}`
}

export function formatDate(iso: string): string {
  return iso.slice(0, 10)  // "2026-04-15"
}

export function formatAmount(won: number): string {
  return new Intl.NumberFormat('ko-KR').format(won) + '원'
}

export function formatPaymentMethod(m: 'cash' | 'card'): string {
  return m === 'cash' ? '현금' : '카드'
}
```

단위 테스트 추가.

### 3. `src/pages/admin/Members/List.tsx` (목록)

상태:
- TanStack Query `useInfiniteQuery` for `listMembers` — `getNextPageParam: (last) => last.next_cursor`.
- 검색 input (debounce 300ms), 지점 select(전역만), 신규 등록 버튼.
- 무한 스크롤: `IntersectionObserver`로 마지막 행 도달 시 `fetchNextPage()`.

UI (시안 `admin-members.jsx`):
- 테이블(데스크탑) / 카드 스택(모바일). Tailwind `md:` 분기.
- 컬럼: 이름, 전화(`formatPhone`), 생년월일, 지점(branch_name — 전역만), 액션(상세/수정/삭제).
- INVALID_CURSOR / INVALID_LIMIT 응답 → 토스트 후 첫 페이지 리셋.

### 4. `src/pages/admin/Members/Edit.tsx` (등록·수정 겸용)

```tsx
const { id } = useParams()
const isNew = !id

// 폼 상태: name, phone, birth_date, branch_id (등록만)
// 클라 검증: name 1~100자, phone /^[0-9]{11}$/, birth_date 유효 날짜
```

처리:
- 등록: POST → 성공 시 `/admin/members/:newId`로 navigate. `PHONE_DUPLICATE` 409 → "같은 지점에 이미 등록된 번호입니다" 인라인.
- 수정: PATCH → 성공 시 `/admin/members/:id` invalidate + navigate. `branch_id` 필드는 비활성/숨김.
- 지점 선택: 전역 관리자는 `useQuery(['branches'])` → select. 지점 관리자는 자기 지점 고정 표시 + 수정 불가.

NumberPad 또는 숫자 모드 input으로 phone 11자리 강제 (`inputMode="numeric" pattern="[0-9]*" maxLength={11}`).

### 5. `src/pages/admin/Members/Detail.tsx`

상태:
- `useQuery(['member', id], () => getMember(id))`.
- 결과 = MemberDetail.

UI 섹션:
- **헤더**: 이름·전화(풀 PII)·생년월일·지점.
- **Active 회원권 카드**: 없으면 "활성 회원권 없음" + "회원권 부여" 버튼(`/admin/memberships/grant?member_id=:id` — step 9). 있으면 type·기간·잔여(pass10) + "정지/환불" 버튼들(step 10).
- **회원권 이력**: 표(상태별 색상). 행 클릭 → `/admin/memberships/:id` (step 9 회원권 상세).
- **결제 이력**: 표(부여 amount>0 / 환불 amount<0 / `formatAmount`로 표시 / method).

### 6. 컴포넌트 테스트

- `Members/List.test.tsx`: 첫 페이지 렌더, next_cursor로 추가 페이지 로드, INVALID_CURSOR fallback, 검색 debounce.
- `Members/Edit.test.tsx`: 11자리 미만 비활성, 등록 성공 navigate, PHONE_DUPLICATE 인라인, 수정 모드는 branch_id 필드 비활성.
- `Members/Detail.test.tsx`: 한 화면에 헤더 + active 카드 + 이력 + 결제 모두 렌더, active 없으면 "부여" 버튼.

### 7. 라우트

```tsx
{ path: 'members', element: <MembersList /> },
{ path: 'members/new', element: <MemberEdit /> },
{ path: 'members/:id', element: <MemberDetail /> },
{ path: 'members/:id/edit', element: <MemberEdit /> }
```

(AdminLayout children 안에 추가.)

## 핵심 규칙

- **풀 PII**: 관리자 화면은 마스킹 X. `formatPhone`은 단순 포맷.
- **수정 폼은 name/phone/birth_date만**: branch_id·created_at 등 무시 (백엔드도 무시하지만 클라에서도 미전송).
- **cursor invalidation**: 회원 등록/수정/삭제 후 list query invalidate.
- **지점 관리자**: 다른 지점 회원 URL 직접 접근 → 백엔드가 404. 토스트 + `/admin/members`로.
- **transfer 금지**: 회원의 다른 지점 이동은 "새 회원 등록"으로 안내. 수정 폼에 branch_id 변경 UI 없음.

## Acceptance Criteria

```bash
cd frontend && pnpm lint && pnpm build && pnpm test
```

- 모든 테스트 통과.
- 수동: 시드 회원으로 목록·상세 확인. 신규 등록 → 동일 번호 등록 시 409 처리. 수정 시 phone 변경 가능, branch_id는 비활성.

## 검증 절차

1. AC.
2. step8 status 갱신.

## 금지사항

- 회원의 `branch_id`를 PATCH 본문에 포함시키지 마라(백엔드가 무시하지만 클라도 미전송).
- 검색을 1글자에서 트리거하지 마라(서버 부하 + UX).
- soft-deleted 회원을 목록에 표시하지 마라(백엔드 `deleted_at IS NULL` 필터로 이미 제외).
- 회원 hard delete 시도 금지(백엔드가 거부, 클라도 UI 없음).
