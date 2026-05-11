---
agent: frontend
---

# Step 4: 관리자 인증 화면 (Login + PasswordChange + 보호 라우트 + Shell)

## 목표

관리자 영역 진입을 가능하게 한다. 화면 3개 + 가드 + 레이아웃 셸.

이 step이 끝나면:
- `/admin/login`에서 username/password로 로그인 → access/refresh 토큰 저장 → 적절한 다음 화면으로 리다이렉트(`must_change_password=true`면 `/admin/password`, 아니면 `/admin/members`).
- `/admin/password`에서 현재 비번 + 새 비번 + 확인을 입력 → 204 응답 후 자동 로그아웃 → `/admin/login` 리다이렉트.
- 모든 `/admin/*` 라우트가 인증 가드를 통과해야 진입 가능. 미인증은 `/admin/login`, `must_change_password=true`는 `/admin/password`로 강제.
- 사이드바/헤더(AdminShell)에 username·role 표시, 전역 전용 메뉴(Sales, BulkExtend, Branches)는 `role==='global'`일 때만 노출.
- 백엔드 에러 코드(`ACCOUNT_LOCKED`, `TEMP_PASSWORD_EXPIRED`, `WRONG_CURRENT_PASSWORD`, `WEAK_PASSWORD`)별로 UI가 분기 처리.

## 읽어야 할 파일

- `backend/CLAUDE.md` — 인증·세션 정책, 로그인 방어(5회·15분), 임시 비번 24h, 강도 정책
- `frontend/CLAUDE.md` — Login/PasswordChange UX, 비번 변경 후 자동 로그아웃
- `docs/API.md` — `/api/admin/login`·`/refresh`·`/logout`·`/password` 응답·에러
- `frontend/ui-design/admin-sales-login.jsx` — Login 시안
- `frontend/ui-design/admin-shell.jsx` — 사이드바/헤더 시안
- step 3 산출물 (AuthContext, apiFetch)

## 작업

### 1. 비번 강도 검증 유틸 (`src/lib/password.ts`)

```ts
export interface PasswordValidation { ok: boolean; reasons: string[] }
export function validatePassword(pw: string): PasswordValidation {
  const reasons: string[] = []
  if (pw.length < 8) reasons.push('8자 이상')
  if (!/[A-Za-z]/.test(pw)) reasons.push('영문 1자 이상')
  if (!/[0-9]/.test(pw)) reasons.push('숫자 1자 이상')
  return { ok: reasons.length === 0, reasons }
}
```
단위 테스트: 8자 미만, 영문 없음, 숫자 없음, 정상 케이스.

### 2. `src/components/AdminLayout.tsx` — 보호 라우트 + 셸

```tsx
import { Navigate, Outlet, useLocation, NavLink } from 'react-router-dom'
import { useAuth } from '@/context/AuthContext'

export default function AdminLayout() {
  const { isAuthenticated, mustChangePassword, role, username, logout } = useAuth()
  const location = useLocation()

  if (!isAuthenticated) return <Navigate to="/admin/login" replace state={{ from: location.pathname }} />
  if (mustChangePassword && location.pathname !== '/admin/password') return <Navigate to="/admin/password" replace />

  return (
    <div className="flex min-h-screen">
      <aside className="w-60 ...">
        <NavLink to="/admin/members">회원</NavLink>
        <NavLink to="/admin/memberships">회원권</NavLink>
        <NavLink to="/admin/check-ins">체크인 이력</NavLink>
        {role === 'global' && (
          <>
            <NavLink to="/admin/sales">매출</NavLink>
            <NavLink to="/admin/bulk-extend">대량 연장</NavLink>
            <NavLink to="/admin/branches">지점</NavLink>
            <NavLink to="/admin/admins">관리자</NavLink>
          </>
        )}
        <button onClick={logout}>로그아웃</button>
      </aside>
      <main className="flex-1 ..."><Outlet /></main>
    </div>
  )
}
```

라우트 구조 (`src/routes.tsx` 갱신):
```tsx
{
  path: '/admin',
  children: [
    { path: 'login', element: <Login /> },
    {
      element: <AdminLayout />,
      children: [
        { path: 'password', element: <PasswordChange /> },
        { path: 'members/*', element: <Members /> },
        // ... (이후 step에서 채움)
      ]
    }
  ]
}
```

### 3. `src/pages/admin/Login.tsx`

상태:
- `username`, `password`
- `error`: 에러 코드별 메시지
- `lockedUntil`: ACCOUNT_LOCKED 응답의 `locked_until` (timestamp string)
- `tempExpired`: TEMP_PASSWORD_EXPIRED 플래그

처리:
- 폼 제출 → `auth.login(username, password)`.
- 성공: `must_change_password`면 `/admin/password`, 아니면 location.state.from || `/admin/members`로 navigate.
- 실패 `ApiException` 분기:
  - `ACCOUNT_LOCKED`: `lockedUntil`을 응답 body의 metadata에서 추출(API.md 명세 확인). 카운트다운 컴포넌트로 표시, 카운트다운 동안 폼 비활성. 0초 도달 시 폼 재활성.
  - `TEMP_PASSWORD_EXPIRED`: "임시 비밀번호가 만료되었습니다. 전역 관리자에게 재발급을 요청하세요" 표시, password 입력 비활성.
  - 일반 `UNAUTHORIZED`/`WRONG_CREDENTIALS`: "아이디 또는 비밀번호가 일치하지 않습니다" 인라인.
  - 그 외: 토스트로 e.message.

카운트다운: `useEffect`로 1초마다 남은 시간 계산. `lockedUntil - Date.now() <= 0`면 reset.

시각 시안: `frontend/ui-design/admin-sales-login.jsx`의 로그인 카드 그대로. 로고는 `frontend/ui-design/assets/pboy-logo.jpg` 또는 `Logo` 컴포넌트.

### 4. `src/pages/admin/PasswordChange.tsx`

상태:
- `currentPassword`, `newPassword`, `confirmPassword`
- `clientErrors`: validatePassword + confirm 일치 검증
- 마운트 시 `useAuth().mustChangePassword` 읽어 상단 안내 분기

처리:
- 클라이언트 검증 실패 시 제출 버튼 비활성 + 인라인 가이드(`8자 이상, 영문 1자 이상, 숫자 1자 이상`, `확인 비밀번호가 일치하지 않습니다`).
- 제출 → `apiFetch('/api/admin/password', { method: 'POST', body: { current_password, new_password } })`. 응답 204.
- 성공 후: 토큰 폐기(`clearTokens()`) + AuthContext state 초기화 + 토스트("비밀번호가 변경되었습니다. 새 비밀번호로 다시 로그인하세요") + `/admin/login` navigate.
- 실패 분기:
  - `WRONG_CURRENT_PASSWORD` (백엔드 step3의 7b6376d 보정): "현재 비밀번호가 일치하지 않습니다" 인라인.
  - `WEAK_PASSWORD`: 클라 검증을 통과했는데 백엔드가 거부하면 fallback 토스트.
  - 그 외: 토스트.

### 5. 단위/컴포넌트 테스트

- `src/lib/password.test.ts`: 강도 검증 4 케이스.
- `src/pages/admin/Login.test.tsx`: 로그인 성공 → navigate, ACCOUNT_LOCKED → 카운트다운 표시 + 폼 비활성, TEMP_PASSWORD_EXPIRED → 안내 + 입력 비활성, 잘못된 비번 → 인라인. MSW로 mock.
- `src/pages/admin/PasswordChange.test.tsx`: 클라 검증 실패 → 제출 비활성, 204 응답 → 토큰 폐기 + navigate, WRONG_CURRENT_PASSWORD → 인라인.
- `src/components/AdminLayout.test.tsx`: 미인증 → `/admin/login` redirect, mustChangePassword → `/admin/password` redirect, role==='branch' → 전역 메뉴 미노출.

## 핵심 규칙

- **must_change_password 가드는 라우트 단**에서. PasswordChange 자체는 가드 우회.
- **로그아웃 액션**: AdminLayout의 버튼 → `auth.logout()` (POST /api/admin/logout + 토큰 폐기 + navigate). 401이어도 무시(이미 만료된 토큰이라도 토큰 폐기는 진행).
- **PII 비노출**: 로그인 실패 메시지에 username을 그대로 echo하지 말 것 (e.g., "boyeon은 없는 계정입니다"는 피하기. 일반 메시지).
- **카운트다운 표시**: 분/초만 표시 — KST 타임존 표시 불필요.
- **에러 메시지는 한국어**.

## Acceptance Criteria

```bash
cd frontend
pnpm lint
pnpm build
pnpm test
```

- Login·PasswordChange·AdminLayout 모든 컴포넌트 테스트 통과.
- `tsc -b` strict 통과.
- 빈 라우트라도 `/admin/members`로 직접 접속 시 미인증이면 `/admin/login`으로 리다이렉트.

수동 확인(사용자):
- 시드 관리자(`SEED_ADMIN_USERNAME`)로 로그인 → 비번 변경 화면 → 새 비번 입력 → 로그인 화면으로 리다이렉트 → 새 비번으로 로그인 → 빈 회원 페이지(이후 step에서 채움).

## 검증 절차

1. AC.
2. `code-reviewer` 호출 (권장 — 인증은 보안 영역).
3. step4 status 갱신.

## 금지사항

- 토큰을 console.log·에러 메시지에 포함 금지.
- 비밀번호 입력값을 localStorage·메모리 외 저장 금지.
- 401 받았을 때 사용자에게 토큰 만료 사유를 그대로 노출하지 않는다(일반 메시지).
- 로그아웃 실패(네트워크 에러)로 인해 클라이언트 토큰을 남기지 않는다(반드시 clearTokens).
