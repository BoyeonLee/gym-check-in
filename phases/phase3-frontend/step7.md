---
agent: frontend
---

# Step 7: 키오스크 체크인 마무리 (MemberPick + CheckInDone)

## 목표

검색 결과에서 회원 선택 → 체크인 API 호출 → 완료 화면. 키오스크 플로우의 마지막 두 화면.

이 step이 끝나면:
- `/kiosk/pick`은 step 6의 검색 결과(query cache)를 읽어 보여준다. 결과 0건: 안내 후 Idle 복귀. 결과 1건: 자동으로 다음 단계(체크인). 결과 2건 이상: 카드 리스트 표시. `truncated: true`면 상단에 안내 배너.
- 회원 카드를 탭하면 `POST /api/check-ins`로 체크인 → 성공 시 `/kiosk/done`. 실패 422 `MEMBERSHIP_NOT_STARTED`는 "회원권 시작일이 아직 되지 않았습니다 (시작일: YYYY-MM-DD)" 안내 후 Idle 복귀. 422 `NO_ACTIVE_MEMBERSHIP`은 "활성 회원권이 없습니다" 안내.
- `/kiosk/done`은 환영 메시지 + 회원 이름(마스킹 안 한 first name까지는 OK — 시안에 따라 결정) + 3초 후 자동으로 Idle 복귀.
- 체크인 성공 시 `todayCount` query를 invalidate해서 Idle의 카운터가 즉시 +1.

## 읽어야 할 파일

- `frontend/CLAUDE.md` — MemberPick 마스킹, 결과 0건/1건/2명+ 분기, 정렬은 서버 정본, MEMBERSHIP_NOT_STARTED 분기, today-count invalidate
- `docs/API.md` — `POST /api/check-ins` 요청·응답·에러 코드
- `frontend/ui-design/kiosk-screens-2.jsx` — MemberPick·CheckInDone 시안
- step 6 산출물 (검색 결과 query cache 키)

## 작업

### 1. `src/api/checkins.ts` 확장

```ts
import { apiFetch } from './client'

export interface CheckInResponse {
  id: number
  member_id: number
  member_name: string
  membership_id: number
  checked_in_at: string  // KST +09:00
  remaining_after?: number  // pass10 차감 후 잔여 (없으면 monthly)
}

export async function postCheckIn(opts: { member_id: number; branch_id: number }) {
  return apiFetch<CheckInResponse>('/api/check-ins', {
    method: 'POST',
    body: opts,
    skipAuth: true  // 키오스크 공개
  })
}
```

(인증 정책은 API.md 확인 — 키오스크 체크인은 인증 미요구일 가능성. 정확한 인증 요구 사항은 백엔드 step7 확인.)

### 2. `src/pages/kiosk/MemberPick.tsx`

상태:
- query cache에서 검색 결과 읽기: `useQueryClient().getQueryData(['search', mode, query])`.
- 검색 결과가 없으면 (cache miss) `/kiosk/idle` redirect.
- `useMutation` for `postCheckIn`.

처리:
- 결과 `items.length === 0`: "활성 회원권이 없거나 회원이 등록되어 있지 않습니다" 안내 + 3초 후 Idle navigate.
- 결과 `items.length === 1`: 자동으로 체크인 mutation 호출 → 성공 시 `/kiosk/done` + 회원 정보 전달.
- 결과 `items.length >= 2`: 카드 리스트 렌더(서버 정렬 그대로). 각 카드 = 이름 + member_id_display(#1234) + phone_masked + birth_md. 탭 시 체크인.
- `truncated === true`: 상단에 "결과가 너무 많습니다. 회원 번호 또는 전화 4자리로 검색해주세요" 배너 + 결과 그대로 렌더.
- 체크인 성공 후: `queryClient.invalidateQueries({ queryKey: ['todayCount', branchId] })` + `navigate('/kiosk/done', { state: { member } })`.
- 체크인 실패 분기:
  - `MEMBERSHIP_NOT_STARTED` (422): API.md에 따라 응답에 start_date가 들어오면 "회원권 시작일이 아직 되지 않았습니다 (시작일: YYYY-MM-DD)". 들어오지 않으면 단순 메시지. 안내 후 3초 → Idle.
  - `NO_ACTIVE_MEMBERSHIP` (422): "활성 회원권이 없습니다. 카운터에 문의해주세요." 3초 → Idle.
  - 그 외: 토스트 + Idle.
- `useIdleTimeout(10_000, () => navigate('/kiosk/idle'))`.

UI (시안 `kiosk-screens-2.jsx`):
- 카드 그리드 또는 리스트. 큰 터치 타겟(최소 96px 높이 — 시안 따름).
- 회원 이름은 그대로(마스킹 X — 검색하러 온 본인이라는 전제).
- 식별 보조 정보(phone_masked, birth_md, #member_id)는 작게 그레이.

### 3. `src/pages/kiosk/CheckInDone.tsx`

상태:
- `location.state.member`: 직전 화면에서 전달된 회원 정보.
- 상태 없으면(직접 URL 접근 등) Idle redirect.

처리:
- 마운트 시 3초 타이머 → Idle navigate.
- "안녕하세요, {first_name}님!" 또는 "체크인 완료" + 시각 표시.
- pass10이면 "남은 횟수: N회" (`remaining_after`).

UI:
- 큰 체크 아이콘, 환영 메시지, 시간.
- "확인" 버튼도 둠 — 사용자가 누르면 즉시 Idle 복귀(3초 기다리지 않게).

### 4. `src/lib/format.ts` (마스킹·포맷 헬퍼)

기존 마스킹은 백엔드가 처리하지만, 관리자 화면용 포맷 헬퍼는 다음 step에서 추가. 이 step에선 다음 정도만:

```ts
export function formatCheckedInAt(iso: string): string {
  // "2026-04-27T18:23:00+09:00" → "오후 6:23" 또는 "18:23"
  const d = new Date(iso)
  return d.toLocaleTimeString('ko-KR', { hour: '2-digit', minute: '2-digit', hour12: false })
}
```

### 5. 컴포넌트 테스트

- `MemberPick.test.tsx`:
  - 0건 → 안내 + 3초 후 Idle.
  - 1건 → 자동 체크인 mutation → 성공 시 done navigate.
  - 2건+ → 카드 N개 렌더, 탭 시 체크인.
  - truncated → 배너 표시.
  - 422 `MEMBERSHIP_NOT_STARTED` → 안내 메시지에 시작일 포함, 3초 후 Idle.
  - 422 `NO_ACTIVE_MEMBERSHIP` → 안내, 3초 후 Idle.
  - 체크인 성공 후 todayCount invalidate (queryClient mock 확인).
- `CheckInDone.test.tsx`:
  - state.member 없으면 Idle redirect.
  - 3초 후 자동 Idle navigate.
  - pass10이면 remaining_after 표시.

## 핵심 규칙

- **회원 이름은 마스킹하지 않는다** (검색해서 자기를 찾는 본인). 식별 보조 정보(phone_masked, birth_md, #id)는 백엔드가 마스킹해서 내려준 값을 그대로 표시.
- **클라 재정렬 금지**: 검색 결과는 서버가 최근 체크인 순으로 정렬. items.sort() 호출 금지.
- **체크인 mutation은 한 회원당 1번**: 클릭 중복 방지 위해 mutation pending 시 카드 클릭 비활성. 백엔드 5초 LRU가 안전망(같은 응답 반환).
- **today-count invalidate**: 체크인 성공 후 즉시. polling 30초를 기다리지 않게.
- **CheckInDone의 3초 자동 복귀**: 너무 짧지도 길지도 않게. 사용자가 보고 확인할 시간.
- **MEMBERSHIP_NOT_STARTED는 race 보호용 분기**: 정상적으로는 search 결과에서 제외돼 여기까지 오지 않지만 자정 직후 등 짧은 race로 들어올 수 있음.
- **PII 비노출**: 422 에러 메시지에 회원 PII가 들어와도 그대로 노출하지 않는다 — 에러 코드 기반 직접 메시지 작성.

## Acceptance Criteria

```bash
cd frontend
pnpm lint && pnpm build && pnpm test
```

- 모든 컴포넌트 테스트 통과.
- 시안 일치.

수동 확인 (시드 데이터로):
- 검색 → 1명 결과 → 자동 체크인 → done → Idle 카운터 +1.
- 검색 → 2명 결과 → 카드 탭 → done.
- 검색 → 0건 → 안내 → Idle.
- 백엔드에서 미래 시작 회원권만 가진 회원을 인위로 만들고 검색 — `MEMBERSHIP_NOT_STARTED` 메시지 확인.

## 검증 절차

1. AC.
2. step7 status 갱신. **이 step에서 키오스크 플로우 전체가 동작해야 함.**

## 금지사항

- 회원 이름·식별 정보를 sessionStorage·query string에 직렬화 금지(state로만 전달).
- 검색 결과를 클라이언트에서 재정렬·필터링 금지.
- 422 에러 메시지를 그대로 토스트로 띄우지 마라(에러 코드 기반 한국어 메시지 직접 작성).
- 체크인 성공 후 todayCount invalidate를 빠뜨리지 마라(UX 회귀).
- pass10 잔여 횟수가 0이 됐을 때 음수로 표시되지 않게 (`remaining_after >= 0`).
