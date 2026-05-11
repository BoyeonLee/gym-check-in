---
agent: frontend
---

# Step 12: 관리자 — 대량 연장 + 지점 CRUD (전역 전용)

## 목표

전역 관리자만 사용하는 두 화면. 대량 연장은 회복 불가능한 작업이라 confirm 모달 + Idempotency-Key가 핵심.

이 step이 끝나면:
- `/admin/bulk-extend`: 전역 전용. body { branch_id?, type?, days, reason } + Idempotency-Key. days 1~90 정수만 허용. `MEMBERSHIP_PERIOD_OVERLAP` 시 `first_conflict_membership_id` 표시 + 전체 롤백 안내.
- `/admin/branches`: 전역 전용. 목록·생성·수정·삭제(soft). ADDRESS_DUPLICATE / BRANCH_IN_USE 분기.

## 읽어야 할 파일

- `frontend/CLAUDE.md` — BulkExtend 정책, Branches 전역 전용
- `backend/CLAUDE.md` — bulk-extend Idempotency, BRANCH_IN_USE
- `docs/API.md` — 두 엔드포인트
- step 5 (listBranches API), step 3 (useIdempotencyKey)

## 작업

### 1. `src/api/branches.ts` 확장

```ts
export async function createBranch(body: { name: string; address?: string }) {
  return apiFetch<Branch>('/api/branches', { method: 'POST', body })
}
export async function updateBranch(id: number, body: { name?: string; address?: string }) {
  return apiFetch<Branch>(`/api/branches/${id}`, { method: 'PATCH', body })
}
export async function deleteBranch(id: number) {
  return apiFetch(`/api/branches/${id}`, { method: 'DELETE' })
}
```

### 2. `src/api/memberships.ts` 확장 — bulk-extend

```ts
export interface BulkExtendBody {
  branch_id?: number
  type?: 'monthly' | 'pass10'
  days: number       // 1~90
  reason: string
}
export interface BulkExtendResponse {
  extended_count: number
  first_conflict_membership_id?: number  // 충돌 시
}

export async function bulkExtend(body: BulkExtendBody, idempotencyKey: string) {
  return apiFetch<BulkExtendResponse>('/api/memberships/bulk-extend', {
    method: 'POST', body, idempotencyKey
  })
}
```

### 3. `src/pages/admin/BulkExtend.tsx`

라우트 가드: `useAuth().role === 'global'`이 아니면 `/admin/members` redirect.

상태:
- `branchId?` (전체 / 특정 지점)
- `type?` ('monthly' | 'pass10' | 전체)
- `days`: int 1~90
- `reason`: textarea
- `useIdempotencyKey()`

클라 검증:
- `days < 1 || days > 90` → 비활성 + 인라인.
- 비정수 → 비활성.
- reason 1~500자.

처리:
- 제출 → confirm 모달 ("대상: 지점 X / type Y / +N일 / 사유 ...". "되돌릴 수 없는 작업입니다") → bulkExtend 호출.
- 성공: `extended_count` 표시 토스트("N건의 회원권이 +days일 연장되었습니다") + regenerate idempotency key + reason 초기화.
- 실패:
  - `INVALID_EXTEND_DAYS` 400: 인라인.
  - `MEMBERSHIP_PERIOD_OVERLAP` 409: 토스트 "연장 결과가 일부 회원의 미래 회원권과 겹칩니다 (회원권 ID #{first_conflict_membership_id}). 미래 회원권 시작일을 조정한 후 재시도하세요. 전체 롤백되었습니다." + 해당 회원권 상세로 가는 링크.
  - `IDEMPOTENCY_KEY_REQUIRED` 400 (헤더 누락 시): 절대 발생하면 안 됨. 토스트 + 키 강제 재발급.
  - `IDEMPOTENCY_KEY_CONFLICT` 409: 토스트 + regenerate.

UI:
- 폼: 지점 select + type select + days input(NumberPad 또는 number input) + reason textarea + 큰 "실행" 버튼.
- 미리보기 카드: "대상 회원권을 +N일 연장합니다" (대상 수는 백엔드가 알려주지 않으므로 표시 X).

### 4. `src/pages/admin/Branches/List.tsx`

라우트 가드: 전역 전용.

상태:
- `useQuery(['branches'], listBranches)`.

UI:
- 목록(이름·주소·생성일·액션).
- "지점 추가" 버튼 → 모달 또는 새 라우트.
- 행 클릭 → 수정 모달.
- 삭제 버튼 → confirm + delete.

### 5. `src/pages/admin/Branches/Edit.tsx` (등록·수정 겸용 모달/페이지)

상태:
- `name` (1~50자)
- `address` (선택, 비-NULL이면 unique 강제)

처리:
- 등록: POST → 성공 시 invalidate + 닫기. `ADDRESS_DUPLICATE` 409 인라인.
- 수정: PATCH → 성공 시 invalidate + 닫기. `ADDRESS_DUPLICATE` 인라인.
- 삭제: DELETE → 성공 시 invalidate. `BRANCH_IN_USE` 409 → "이 지점에 등록된 회원/관리자가 있어 삭제할 수 없습니다" 토스트.

### 6. 컴포넌트 테스트

- `BulkExtend.test.tsx`:
  - days=0/91 → 비활성 + 인라인.
  - 지점 관리자 접속 시도 → redirect.
  - 제출 성공 → extended_count 토스트 + regenerate key.
  - MEMBERSHIP_PERIOD_OVERLAP → first_conflict_membership_id 링크 표시.
  - 같은 key 이중 제출 → 한 번만 호출.
- `Branches/List.test.tsx`: 목록 렌더, 추가/수정/삭제 흐름.
- `Branches/Edit.test.tsx`: 빈 name 비활성, 1글자 비활성, ADDRESS_DUPLICATE 인라인, BRANCH_IN_USE 토스트.

## 핵심 규칙

- **두 화면 모두 전역 전용**: AdminLayout 메뉴 + 라우트 가드 + 백엔드 403 fallback.
- **BulkExtend는 Idempotency-Key 필수**: 헤더 누락 시 백엔드 400. 마운트 시 발급, 성공 후 regenerate.
- **days는 정수 1~90**: 음수·0·91 이상 클라에서 차단.
- **충돌 시 전체 롤백**: extended_count=0임을 사용자에게 명시.
- **지점 삭제는 자식 정리 후**: 회원·관리자 0인지 확인 후. BRANCH_IN_USE 폴백.
- **address NULL 허용**: 백엔드 스키마 그대로. 빈 문자열 ≠ NULL.
- **reason 1~500자**: 감사 추적용.

## Acceptance Criteria

```bash
cd frontend && pnpm lint && pnpm build && pnpm test
```

수동:
- BulkExtend → 지점 1, type monthly, +7일, reason="설 연휴 보상" → 성공.
- 다시 같은 key로 전송(개발자 도구로) → 첫 응답 반환(이중 처리 없음).
- 미래 회원권을 인위로 만들고 연장 → 409 + first_conflict_membership_id 링크.
- 지점 추가/수정.
- 회원이 있는 지점 삭제 시도 → BRANCH_IN_USE.

## 검증 절차

1. AC.
2. step12 status 갱신.

## 금지사항

- 지점 관리자에게 BulkExtend·Branches 메뉴 노출 금지.
- days를 음수/0/91 이상으로 보내지 마라(클라 차단).
- BulkExtend 응답을 캐시(`staleTime: Infinity`)에 저장하지 마라 — 호출 결과는 일회성.
- 지점 hard delete UI 만들지 마라(soft delete만).
- address를 빈 문자열로 보내지 마라(NULL 의도면 undefined 또는 미전송).
