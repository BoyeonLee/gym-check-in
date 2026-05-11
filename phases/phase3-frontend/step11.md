---
agent: frontend
---

# Step 11: 관리자 — 체크인 이력 + 매출 요약 (전역 전용)

## 목표

`/admin/check-ins`(체크인 이력)와 `/admin/sales`(매출, 전역 전용) 두 화면.

이 step이 끝나면:
- 체크인 이력: cursor 페이지(20개 무한 스크롤) + 지점 필터(전역만) + 기간 선택 + raw/daily 모드 토글. daily 모드는 페이지네이션 없음(92일 제한). RANGE_TOO_LARGE 폴백.
- 매출 요약: 전역 전용. 카드 3개(gross_total / refund_total / net_total) + 일별 표 + 수단별 표(현금/카드). 같은 분리 적용.

## 읽어야 할 파일

- `frontend/CLAUDE.md` — 체크인 이력 cursor, daily 페이지네이션 없음·92일 제한, Sales 페이지 gross/refund/net 분리
- `backend/CLAUDE.md` — `/api/check-ins` raw/daily, `/api/sales/summary` 전역 전용
- `docs/API.md` — 두 엔드포인트 요청·응답
- step 3 (apiFetch), step 4 (AdminLayout 메뉴 가드)

## 작업

### 1. `src/api/checkins.ts` 확장

```ts
export interface CheckInRow {
  id: number
  member_id: number
  member_name: string
  branch_id: number
  branch_name: string
  membership_id: number
  checked_in_at: string  // KST +09:00
}

export interface DailyCheckInRow {
  member_id: number
  member_name: string
  branch_id: number
  branch_name: string
  date: string           // "2026-04-15"
  checkin_count: number
}

export async function listCheckInsRaw(opts: { from: string; to: string; branchId?: number; cursor?: string; limit?: number }) {
  const qs = new URLSearchParams({ from: opts.from, to: opts.to, aggregate: 'raw' })
  if (opts.branchId) qs.set('branchId', String(opts.branchId))
  if (opts.cursor) qs.set('cursor', opts.cursor)
  if (opts.limit) qs.set('limit', String(opts.limit))
  return apiFetch<{ items: CheckInRow[]; next_cursor: string | null }>(`/api/check-ins?${qs}`)
}

export async function listCheckInsDaily(opts: { from: string; to: string; branchId?: number }) {
  const qs = new URLSearchParams({ from: opts.from, to: opts.to, aggregate: 'daily' })
  if (opts.branchId) qs.set('branchId', String(opts.branchId))
  return apiFetch<{ items: DailyCheckInRow[] }>(`/api/check-ins?${qs}`)
}
```

### 2. `src/api/sales.ts`

```ts
export interface SalesSummary {
  gross_total: number
  refund_total: number  // 절대값
  net_total: number
  by_method: {
    cash: { gross: number; refund: number; net: number }
    card: { gross: number; refund: number; net: number }
  }
  by_day: Array<{ date: string; gross: number; refund: number; net: number }>
}

export async function getSalesSummary(opts: { from: string; to: string; branchId?: number }) {
  const qs = new URLSearchParams({ from: opts.from, to: opts.to })
  if (opts.branchId) qs.set('branchId', String(opts.branchId))
  return apiFetch<SalesSummary>(`/api/sales/summary?${qs}`)
}
```

### 3. `src/pages/admin/CheckIns/index.tsx`

상태:
- `from`/`to`: 기본 = 최근 7일 (KST).
- `mode`: `'raw' | 'daily'` 토글.
- `branchId`: 전역은 select, 지점은 자기 지점 강제(서버가 처리지만 클라 UI도 일관).

처리:
- 클라 검증: `to - from > 92일` → mode='daily'일 때 제출 비활성 + 인라인 "최대 92일까지 조회 가능". raw도 백엔드 정책에 따라 동일 제한 적용 여부 확인.
- raw 모드: `useInfiniteQuery` for cursor 페이지.
- daily 모드: `useQuery` (페이지네이션 없음).
- `RANGE_TOO_LARGE` 400 → 인라인 + 자동으로 to를 from+92로 보정 제안.
- `INVALID_AGGREGATE` 400 → 토스트(정상적으로는 발생 안 함).

UI:
- 상단 필터 바: 기간 picker(date input 2개) + mode 토글 + 지점 select.
- raw 표: 체크인 시각·회원 이름(클릭 → 회원 상세)·지점.
- daily 표: 날짜·회원 이름·지점·횟수.
- 모바일: 카드 스택.

### 4. `src/pages/admin/Sales/index.tsx`

라우트 가드: AdminLayout에서 role==='global'만 메뉴 노출. URL 직접 접근 시도 → 백엔드 403 → 토스트 + `/admin/members`로.

상태:
- `from`/`to`: 기본 = 이번 달.
- `branchId`: 선택(전체 / 특정 지점).

UI:
- 상단: 기간·지점 필터.
- 카드 3개: 총매출(`gross_total`) · 환불(`refund_total`) · 순매출(`net_total`). `formatAmount` 사용.
- 표 1: 수단별 (현금/카드 × gross/refund/net).
- 표 2: 일별 (`by_day` × gross/refund/net).
- 데이터가 없으면 "조회 기간에 매출 없음".

### 5. 컴포넌트 테스트

- `CheckIns.test.tsx`:
  - raw 모드 → cursor 페이지 무한 스크롤.
  - daily 모드 → 페이지네이션 없음 + 92일 초과 비활성.
  - RANGE_TOO_LARGE 응답 → 자동 보정 제안.
  - 지점 필터 변경 → query refetch.
- `Sales.test.tsx`:
  - 카드 3개 값 표시.
  - 수단별·일별 표 렌더.
  - 빈 응답 → "매출 없음".
  - 지점 관리자 → 메뉴 미노출 (AdminLayout 테스트).

## 핵심 규칙

- **daily는 페이지네이션 없음**: 92일 제한이 안전망. 클라가 함부로 longer range 보내지 마라.
- **매출 페이지는 전역 전용**: AdminLayout에서 role==='global'일 때만 메뉴 노출. URL 직접 접근 시 백엔드 403 처리.
- **금액 표시는 `formatAmount`**: `1,234,567원` 한국어 포맷.
- **환불은 절대값 표시**: `refund_total`은 백엔드가 절대값으로 내려줌(음수 아님). 표에서도 `-150,000원` 또는 `(환불) 150,000원` 등 명시.
- **지점 필터 UX**: 전역은 "전체 / 지점 1 / 지점 2" select. 지점은 자기 지점 고정.
- **invalidate**: 체크인 / 매출은 폴링 안 함(사용자 수동 새로고침). 단 회원·회원권 변경 후에는 invalidate 추천.

## Acceptance Criteria

```bash
cd frontend && pnpm lint && pnpm build && pnpm test
```

수동:
- 체크인 이력 raw → 무한 스크롤로 페이지 추가 로드.
- daily → 일자별 회원 카운트.
- 100일 → daily 모드 자동 비활성 + 안내.
- 매출 페이지(전역 로그인) → 3개 카드 + 표.
- 매출 페이지(지점 로그인) → 메뉴 미노출, URL 직접 접근 → 403 안내.

## 검증 절차

1. AC.
2. step11 status 갱신.

## 금지사항

- 매출 페이지를 지점 관리자에게 노출 금지 (메뉴 + 라우트 가드 둘 다).
- daily 모드에 페이지네이션 UI 추가 금지(서버가 한 번에 내려줌).
- 환불 row를 양수로 표시 금지(사용자가 매출로 오해할 위험).
- 92일 초과 요청을 그대로 보내 백엔드 400을 받도록 두지 마라(클라 단에서 차단).
- 응답 timestamp 표시 시 UTC `Z` 표기로 변환 금지(백엔드가 KST `+09:00`으로 직렬화).
