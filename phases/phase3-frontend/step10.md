---
agent: frontend
---

# Step 10: 관리자 — 회원권 정지·조기활성화·미래취소·환불

## 목표

회원권 상세 페이지에서 호출되는 4종 액션 폼. 모두 회원권 한 건의 라이프사이클을 바꾸는 무거운 작업이므로 confirm 모달 + 에러 분기를 꼼꼼히.

이 step이 끝나면:
- 정지(`POST /api/memberships/:id/pause`): 시작일·종료일·사유 입력. pause_used=true면 폼 비활성. 미래 시작 회원권의 시작 전 정지 차단.
- 조기 활성화(`POST /api/memberships/:id/unpause`): status='paused'에서만. 사유 입력. confirm 모달로 "오늘 활성화하면 만료일이 N일 앞당겨집니다" 미리 계산 표시.
- 미래 정지 취소(`POST /api/memberships/:id/cancel-pause`): status='active' + pause_used + pause_start_date > 오늘. 사유 입력. confirm 모달.
- 환불(`POST /api/memberships/:id/refund`): Idempotency-Key 필수. 사유만 입력. confirm 모달에 자동 채움 미리보기(원본 결제 → 음수 결제 row + method·amount). 422 `MEMBERSHIP_ALREADY_EXPIRED` 분기.

## 읽어야 할 파일

- `frontend/CLAUDE.md` — 4종 폼 정책, confirm 모달, Idempotency-Key는 환불만(부여는 step 9)
- `backend/CLAUDE.md` — 4종 트랜잭션 동작, EXCLUDE 위반 시 OVERLAP, NOT_PAUSED, PAUSE_NOT_SCHEDULED, MEMBERSHIP_ALREADY_EXPIRED
- `docs/API.md` — 4종 엔드포인트 요청·응답
- step 9 (회원권 상세 페이지)

## 작업

### 1. `src/api/memberships.ts` 확장

```ts
export async function pauseMembership(id: number, body: { start_date: string; end_date: string; reason: string }) {
  return apiFetch(`/api/memberships/${id}/pause`, { method: 'POST', body })
}
export async function unpauseMembership(id: number, body: { reason: string }) {
  return apiFetch(`/api/memberships/${id}/unpause`, { method: 'POST', body })
}
export async function cancelPause(id: number, body: { reason: string }) {
  return apiFetch(`/api/memberships/${id}/cancel-pause`, { method: 'POST', body })
}
export async function refundMembership(id: number, body: { reason: string }, idempotencyKey: string) {
  return apiFetch(`/api/memberships/${id}/refund`, { method: 'POST', body, idempotencyKey })
}
```

### 2. `src/pages/admin/Memberships/PauseDialog.tsx`

폼 (모달 또는 페이지):
- `start_date`(date input): default 오늘.
- `end_date`(date input).
- `reason`(textarea, 1~500자).

클라 검증:
- `start_date <= end_date`.
- `start_date >= todayKST()`.
- `start_date >= membership.start_date` (회원권 상세에서 받은 값).
- `end_date <= membership.end_date`.
- 위반 시 인라인.

처리:
- 마운트 시 `pause_used=true`면 폼 자체 비활성 + 안내 "이 회원권은 이미 정지를 사용했습니다".
- 제출 → confirm 모달("정지 등록: YYYY-MM-DD ~ YYYY-MM-DD, 만료일이 N일 연장됩니다") → 실제 호출.
- 실패:
  - `PAUSE_ALREADY_USED` 409: 폼 비활성 + 새 상태 반영.
  - `INVALID_PAUSE_RANGE` 400: 인라인.
  - `MEMBERSHIP_PERIOD_OVERLAP` 409: "정지 연장 결과가 미래 회원권과 겹칩니다. 미래 회원권 시작일을 조정하세요" 인라인.
- 성공: `invalidateQueries(['membership', id])` + 닫기 + 토스트.

### 3. `src/pages/admin/Memberships/UnpauseDialog.tsx`

조건: 상세 페이지에서 status='paused'인 경우만 진입 가능.

상태:
- `reason`(textarea).

UI:
- confirm 모달: "오늘({todayKST()}) 활성화하면 만료일이 {pause_end_date - todayKST()}일 앞당겨집니다 → 새 만료일: {newEndDate}" 미리 계산 표시.

처리:
- 제출 → `unpauseMembership(id, { reason })`.
- 실패:
  - `NOT_PAUSED` 409: "이미 활성 상태입니다" + invalidate.
- 성공: invalidate + 닫기.

### 4. `src/pages/admin/Memberships/CancelPauseDialog.tsx`

조건: status='active' + pause_used=true + pause_start_date > 오늘.

상태: `reason`.

UI:
- confirm 모달: "예약된 정지({pause_start_date} ~ {pause_end_date})를 취소하면 만료일이 원래({originalEndDate})로 되돌아가고 다시 정지를 등록할 수 있습니다".

처리:
- 제출 → `cancelPause(id, { reason })`.
- 실패: `PAUSE_NOT_SCHEDULED` 409 → "취소 가능한 예약 정지가 없습니다" + invalidate.
- 성공: invalidate + 닫기.

### 5. `src/pages/admin/Memberships/RefundDialog.tsx`

조건: status in ('active', 'paused', 'active+미래시작'). expired/refunded → 진입 차단(상세 페이지에서 버튼 미노출).

상태:
- `reason`(textarea).
- `useIdempotencyKey()` — 마운트 시 발급, 성공 후 regenerate.

UI:
- confirm 모달에 자동 채움 미리보기 (회원권 상세에서 받은 payments에서 amount>0인 row를 찾아):
  - "원본 결제: 150,000원 카드 → 환불: -150,000원 카드, 처리일: {todayKST()}"
- 폼 입력은 reason만.

처리:
- 제출 → `refundMembership(id, { reason }, idempotencyKey)`.
- 실패:
  - `MEMBERSHIP_ALREADY_EXPIRED` 409: "만료된 회원권은 환불할 수 없습니다" 토스트 + 닫기.
  - `MEMBERSHIP_REFUND_CONFLICT` (재환불) 409: invalidate + 닫기.
- 성공: regenerate key + invalidate + 토스트("환불 처리되었습니다, -150,000원").

### 6. 회원권 상세 페이지(step 9의 Detail.tsx)에 다이얼로그 연결

```tsx
const [openPause, setOpenPause] = useState(false)
// ... 같은 패턴으로 4종
{canPause && <Button onClick={() => setOpenPause(true)}>정지 등록</Button>}
{openPause && <PauseDialog id={id} membership={data.membership} onClose={() => setOpenPause(false)} />}
```

조건 계산 (step 9에서 박은 분기 재확인):
```ts
const canPause = m.status === 'active' && !m.pause_used
const canUnpause = m.status === 'paused'
const canCancelPause = m.status === 'active' && m.pause_used && m.pause_start_date && m.pause_start_date > todayKST()
const canRefund = m.status === 'active' || m.status === 'paused'  // expired/refunded는 false
```

### 7. 컴포넌트 테스트

- `PauseDialog.test.tsx`:
  - pause_used=true → 폼 비활성.
  - start_date < today → 인라인.
  - end_date > membership.end_date → 인라인.
  - confirm 모달 후 제출 → 성공 시 invalidate.
  - MEMBERSHIP_PERIOD_OVERLAP → 인라인.
- `UnpauseDialog.test.tsx`:
  - 만료일 단축 미리보기 정확.
  - NOT_PAUSED → 안내 + invalidate.
- `CancelPauseDialog.test.tsx`:
  - 원래 만료일 복원 미리보기.
  - PAUSE_NOT_SCHEDULED → 안내.
- `RefundDialog.test.tsx`:
  - 자동 채움 미리보기 (원본 결제 → 음수).
  - 제출 성공 후 regenerate key (state 변경 검증).
  - MEMBERSHIP_ALREADY_EXPIRED → 토스트 + 닫기.
  - 같은 키 이중 제출 → 한 번만 호출.

## 핵심 규칙

- **환불만 Idempotency-Key**: pause/unpause/cancel-pause는 idempotent하지 않은 작업이지만 백엔드가 헤더를 요구하지 않음(API.md 확인). 환불은 음수 결제 row 중복 방지로 필수.
- **모든 폼에 confirm 모달**: 라이프사이클 변경은 되돌리기 어려우므로 1차 방어.
- **처리 중 버튼 비활성**: 2차 방어. mutation `isPending`으로 disabled.
- **자동 채움은 미리보기만**: 환불의 method·amount는 백엔드가 결정. 클라는 표시만.
- **invalidate**: 4종 모두 성공 후 `['membership', id]` + 회원 상세를 위해 `['member', memberId]`도 함께.
- **reason 1~500자**: 너무 짧으면 감사 추적이 무의미, 너무 길면 부담. 클라 검증으로 강제.
- **PII 비노출**: 환불 미리보기에 회원 PII는 회원권 상세 정보 한정(이름은 OK, 전화·생년월일 표시 불필요).

## Acceptance Criteria

```bash
cd frontend && pnpm lint && pnpm build && pnpm test
```

수동:
- active 회원권 → 정지 등록(미래 날짜) → 정지 이력 추가, 만료일 연장. 다시 정지 시도 → 비활성.
- 위에서 미래 정지를 cancel-pause → 만료일 원복.
- 정지 도달(테스트 DB로 status='paused' 강제) → unpause → 만료일 단축.
- active 회원권 → refund → status='refunded' + 음수 결제 row + 매출(step 11) 반영.

## 검증 절차

1. AC.
2. step10 status 갱신.

## 금지사항

- 환불에서 amount·method·paid_at·branch_id를 클라가 보내지 마라(서버 자동).
- pause/unpause/cancel-pause/refund 모두 처리 중에는 폼·버튼 비활성.
- 자동 invalidate를 빠뜨리지 마라(다음 호출이 stale 상태로 분기 판단 오류 야기).
- expired/refunded 상태에서 액션 버튼을 표시하지 마라(상세 페이지에서 false → 버튼 미렌더).
