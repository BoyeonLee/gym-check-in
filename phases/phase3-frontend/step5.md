---
agent: frontend
---

# Step 5: 키오스크 셸 (BranchSetup + Idle + 풀스크린 + 5초 롱프레스)

## 목표

키오스크 진입과 대기 화면을 만든다. 회원이 태블릿 앞에 서면 보는 첫 화면(`Idle`) + 관리자가 5초 롱프레스로 진입하는 지점 재설정 화면(`BranchSetup`).

이 step이 끝나면:
- `/` 진입 시 `BranchContext.branchId`가 없으면 `/kiosk/setup`, 있으면 `/kiosk/idle`로 리다이렉트.
- `BranchSetup`은 관리자 인증을 요구하고(이미 로그인된 상태에서만 진입), 지점 목록(`GET /api/branches`)을 보여줘 선택 → localStorage 저장 → `/kiosk/idle`.
- `Idle`은 헤더에 **오늘 해당 지점 체크인 N명**을 표시(`GET /api/check-ins/today-count`), 큰 "체크인 시작" 버튼이 `/kiosk/input`으로 이동.
- 모든 `/kiosk/*` 화면 우상단에 5초 롱프레스 감지 영역(보이지 않는 투명 영역)을 두어 5초 누르면 `/kiosk/setup`으로 진입.
- `touch-action: manipulation` + 풀스크린 CSS 적용.

## 읽어야 할 파일

- `frontend/CLAUDE.md` — 키오스크 풀스크린, 5초 롱프레스, idle 타임아웃, today-count invalidate
- `docs/UI_GUIDE.md` — 최소 터치 타겟 64px, 색상 토큰
- `frontend/ui-design/kiosk-screens-1.jsx` — BranchSetup/Idle 시안
- `docs/API.md` — `GET /api/branches`, `GET /api/check-ins/today-count`
- step 3 산출물 (BranchContext, AuthContext, apiFetch)

## 작업

### 1. `src/api/branches.ts`

```ts
import { apiFetch } from './client'

export interface Branch {
  id: number
  name: string
  address: string | null
  created_at: string
}

export async function listBranches() {
  return apiFetch<{ items: Branch[] }>('/api/branches')
}
```

(`GET /api/branches`는 키오스크 공개 + 관리자 공용 — `frontend/CLAUDE.md`. 키오스크 부팅 시에는 인증 없이도 호출 가능해야 한다. 백엔드 step5의 49dffe7에서 공개 그룹으로 이동됨.)

### 2. `src/api/checkins.ts` (오늘 카운트만)

```ts
export async function getTodayCount(branchId: number) {
  return apiFetch<{ count: number }>(`/api/check-ins/today-count?branchId=${branchId}`)
}
```

### 3. `src/components/LongPressArea.tsx`

```tsx
import { useRef, ReactNode } from 'react'

interface Props { onLongPress: () => void; duration?: number; children?: ReactNode; className?: string }
export default function LongPressArea({ onLongPress, duration = 5000, children, className }: Props) {
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const start = () => { timer.current = setTimeout(onLongPress, duration) }
  const cancel = () => { if (timer.current) clearTimeout(timer.current); timer.current = null }
  return (
    <div
      className={className}
      onPointerDown={start}
      onPointerUp={cancel}
      onPointerLeave={cancel}
      onPointerCancel={cancel}
    >
      {children}
    </div>
  )
}
```

키오스크 셸(`src/components/KioskShell.tsx`) 우상단에 64×64 투명 영역으로 배치 + onLongPress → `/kiosk/setup` navigate.

### 4. `src/components/KioskShell.tsx`

```tsx
import { Outlet, useNavigate } from 'react-router-dom'
import { useBranch } from '@/context/BranchContext'
import LongPressArea from './LongPressArea'

export default function KioskShell() {
  const navigate = useNavigate()
  const { branchId } = useBranch()

  // /kiosk/setup이 아닐 때 branchId 없으면 리다이렉트
  // (라우터 단에서 보장하지만 안전망)

  return (
    <div className="theme-kiosk-light min-h-screen" style={{ touchAction: 'manipulation' }}>
      <LongPressArea
        onLongPress={() => navigate('/kiosk/setup')}
        className="fixed top-0 right-0 w-16 h-16 z-50"
      />
      <Outlet />
    </div>
  )
}
```

### 5. `src/pages/kiosk/BranchSetup.tsx`

상태:
- 인증 가드: `useAuth().isAuthenticated`가 false면 즉시 `/admin/login` navigate (관리자 권한이 있어야 지점 설정 가능). state.from = '/kiosk/setup'로 로그인 후 다시 돌아옴.
- 지점 목록: TanStack Query `useQuery({ queryKey: ['branches'], queryFn: listBranches })`.
- 선택 핸들러: `setBranchId(id)` → `/kiosk/idle` navigate.

UI:
- 카드 그리드(반응형, 한 줄에 2~3개), 각 카드는 지점 이름·주소.
- 큰 버튼(최소 64px 높이).

### 6. `src/pages/kiosk/Idle.tsx`

상태:
- `branchId`가 없으면 즉시 `/kiosk/setup` navigate (안전망).
- TanStack Query: `useQuery({ queryKey: ['todayCount', branchId], queryFn: () => getTodayCount(branchId), refetchInterval: 30_000 })`. 30초마다 polling으로 다른 키오스크에서 체크인한 카운트 반영.

UI (시안 `kiosk-screens-1.jsx` 참조):
- 큰 헤더에 지점 이름 + "오늘 N명 체크인" 카운터.
- 중앙에 큰 "체크인 시작" 버튼 (`onClick` → `/kiosk/input`).
- 시간(현재 시각)도 표시(시안에 있으면).

### 7. 라우트 갱신

```tsx
{
  element: <KioskShell />,
  children: [
    { path: '/', element: <Navigate to="/kiosk/idle" replace /> },
    { path: '/kiosk/setup', element: <BranchSetup /> },
    { path: '/kiosk/idle', element: <Idle /> },
    { path: '/kiosk/input', element: <InputSelect /> },  // step 6
    { path: '/kiosk/voice', element: <VoiceSearch /> },
    { path: '/kiosk/typing', element: <TypingSearch /> },
    { path: '/kiosk/pick', element: <MemberPick /> },     // step 7
    { path: '/kiosk/done', element: <CheckInDone /> }
  ]
}
```

`/kiosk/idle` 외 경로에서도 LongPressArea가 동작하도록 KioskShell이 모두 감싼다.

### 8. 풀스크린 보조

`src/lib/fullscreen.ts`:
```ts
export function requestFullscreen() {
  const el = document.documentElement
  if (el.requestFullscreen) el.requestFullscreen().catch(() => { /* PWA가 1차, Fullscreen API는 보조 */ })
}
```

`Idle`에서 사용자 첫 터치 시 한 번 호출 (브라우저 정책 — gesture 없이 호출 불가).

### 9. 컴포넌트 테스트

- `LongPressArea.test.tsx`: 5초 누르면 onLongPress 호출, 5초 전에 떼면 미호출. `vi.useFakeTimers()`.
- `Idle.test.tsx`: today-count 응답 표시, branchId 없으면 setup으로 redirect. MSW.
- `BranchSetup.test.tsx`: 미인증 → /admin/login redirect, 인증 후 목록 표시, 선택 시 branchId 저장 + navigate.

## 핵심 규칙

- **`GET /api/branches`는 인증 없이 호출 가능** (백엔드 공개 그룹). `apiFetch`는 access 토큰이 없어도 보내야 하므로 `skipAuth: true` 옵션 사용.
- **`GET /api/check-ins/today-count`도 키오스크용 — 인증 정책은 백엔드 확인**. API.md에 따라 `skipAuth` 적용 여부 결정.
- **5초 롱프레스 영역은 시각적으로 보이지 않게** (투명, 사용자가 인식 못 함). 의도된 관리자 진입.
- **idle 타임아웃은 이 step에서 적용 안 함** — `Idle` 자체가 진입 상태이므로 타임아웃 불필요. step 6의 InputSelect/Voice/Typing/Pick에서 적용.
- **풀스크린은 PWA 매니페스트가 1차**, Fullscreen API는 보조. 강제 호출은 금지(브라우저 정책 위반).
- **today-count는 30초 polling** + 체크인 성공 시 invalidate(step 7에서 추가).

## Acceptance Criteria

```bash
cd frontend
pnpm lint && pnpm build && pnpm test
```

- BranchSetup·Idle·LongPressArea 테스트 통과.
- `/`로 직접 접속 시 branchId 없으면 `/kiosk/setup`, 있으면 `/kiosk/idle`.
- 시안과 시각적으로 일치(색상 토큰·간격).

수동 확인:
- 시안(`kiosk-screens-1.jsx`)와 픽셀 단위로 비교.
- 우상단을 5초 누르면 BranchSetup으로 이동.
- 다른 키오스크에서 체크인 시 30초 안에 카운트 갱신.

## 검증 절차

1. AC.
2. step5 status 갱신.

## 금지사항

- `branchId`를 sessionStorage·URL 쿼리에 두는 변경 금지(localStorage만).
- 5초 롱프레스 외 다른 트리거(예: 비밀 키 조합)로 BranchSetup에 진입하게 만들지 마라.
- today-count를 1초 간격 등 짧은 polling으로 두지 마라(서버 부하).
- 풀스크린 API를 사용자 gesture 없이 호출하지 마라(브라우저가 차단).
