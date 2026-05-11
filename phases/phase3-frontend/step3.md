---
agent: frontend
---

# Step 3: API Client + Auth/Branch Context + 공용 훅 + MSW 셋업

## 목표

Phase 3 모든 화면이 의존하는 **API fetch 래퍼**(`src/api/client.ts`)와 **두 개의 Context**(`AuthContext`, `BranchContext`), **공용 훅**(`useIdempotencyKey`, `useIdleTimeout`)을 만든다. 또한 단위/컴포넌트 테스트의 API mock용 **MSW**(Mock Service Worker)를 셋업한다.

이 step이 끝나면:
- `src/api/client.ts`가 access JWT를 자동으로 Authorization 헤더에 첨부하고, 401 응답 시 자동으로 `POST /api/admin/refresh` → 새 access로 원 요청 재시도한다.
- refresh 자체가 401이면 access/refresh를 localStorage에서 제거하고 `/admin/login`으로 리다이렉트한다.
- 에러 응답이 통일된 형태(`{ error: { code, message } }`)로 throw되어 React Query의 onError에서 분기 가능하다.
- `AuthContext`가 로그인 상태(`access_token`, `refresh_token`, `username`, `role`, `branch_id`, `must_change_password`)를 관리한다.
- `BranchContext`가 `localStorage.branchId`를 로드/저장하고, 미설정 시 `useBranchGuard`로 `/kiosk/setup` 리다이렉트를 도울 수 있다.
- `useIdempotencyKey()`는 마운트 시 `crypto.randomUUID()`로 키를 발급하고, `regenerate()`를 부르면 새 UUID를 발급한다.
- `useIdleTimeout(10000, onIdle)`은 마운트 후 10초간 입력·터치·키보드 이벤트가 없으면 `onIdle()`을 호출한다. 매 이벤트마다 reset.
- MSW가 셋업돼 있어 Vitest 테스트에서 API 응답을 mock할 수 있다.

## 읽어야 할 파일

- `backend/CLAUDE.md` — JWT access 30분/refresh 15시간, 401 재시도, `must_change_password` 가드, `Idempotency-Key` UUIDv4 검증
- `frontend/CLAUDE.md` — fetch 래퍼·토큰 갱신·Idempotency-Key 정책·idle 타임아웃 10초·localStorage 사용
- `docs/API.md` — 응답 형식·에러 코드 카탈로그 (특히 `ACCOUNT_LOCKED`, `TEMP_PASSWORD_EXPIRED`, `WRONG_CURRENT_PASSWORD`, `MUST_CHANGE_PASSWORD`)
- `docs/ARCHITECTURE.md` — 인증/페이지네이션/멱등성 정책
- step 2의 산출물 (디렉토리 구조, 라우터)

## 작업

### 1. `src/types/api.ts` — API 응답·에러 타입 정의

```ts
export interface ApiError {
  code: string
  message: string
}

export interface ApiErrorResponse {
  error: ApiError
}

export class ApiException extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public requestId?: string
  ) {
    super(message)
    this.name = 'ApiException'
  }
}

// 로그인 응답
export interface LoginResponse {
  access_token: string
  refresh_token: string
  expires_in: number
  username: string
  role: 'global' | 'branch'
  branch_id: number | null
  must_change_password: boolean
}

// refresh 응답
export interface RefreshResponse {
  access_token: string
  expires_in: number
  username: string
  role: 'global' | 'branch'
  branch_id: number | null
  must_change_password: boolean
}

// 도메인 타입 (이후 step에서 추가). 여기선 위 두 개만 정의해도 충분.
```

도메인 타입(Member, Membership, Payment, CheckIn, Branch, Admin 등)은 각 화면 step에서 추가한다(이 step에선 의존 라이브러리·인증 타입만).

### 2. `src/api/client.ts` — fetch 래퍼

```ts
import { ApiException } from '@/types/api'

const BASE_URL = import.meta.env.VITE_API_URL || 'http://localhost:8080'

interface FetchOpts {
  method?: 'GET' | 'POST' | 'PATCH' | 'DELETE'
  body?: unknown
  idempotencyKey?: string
  signal?: AbortSignal
  skipAuth?: boolean   // /api/admin/login, /api/admin/refresh 등에서 사용
}

let onForceLogout: (() => void) | null = null
export function setForceLogoutHandler(fn: () => void) { onForceLogout = fn }

export function getAccessToken(): string | null {
  return localStorage.getItem('access_token')
}

export function getRefreshToken(): string | null {
  return localStorage.getItem('refresh_token')
}

export function setTokens(access: string, refresh?: string) {
  localStorage.setItem('access_token', access)
  if (refresh) localStorage.setItem('refresh_token', refresh)
}

export function clearTokens() {
  localStorage.removeItem('access_token')
  localStorage.removeItem('refresh_token')
}

async function rawFetch<T>(path: string, opts: FetchOpts, accessToken: string | null): Promise<T> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (accessToken && !opts.skipAuth) headers.Authorization = `Bearer ${accessToken}`
  if (opts.idempotencyKey) headers['Idempotency-Key'] = opts.idempotencyKey

  const res = await fetch(`${BASE_URL}${path}`, {
    method: opts.method || 'GET',
    headers,
    body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
    signal: opts.signal
  })

  const requestId = res.headers.get('X-Request-ID') ?? undefined

  if (res.status === 204) return undefined as T

  const text = await res.text()
  let parsed: unknown = null
  try { parsed = text ? JSON.parse(text) : null } catch { /* 빈 응답 등 */ }

  if (!res.ok) {
    const body = parsed as { error?: { code?: string; message?: string } } | null
    throw new ApiException(
      res.status,
      body?.error?.code ?? 'UNKNOWN',
      body?.error?.message ?? `Request failed: ${res.status}`,
      requestId
    )
  }

  return parsed as T
}

let refreshInFlight: Promise<string | null> | null = null

async function refreshAccess(): Promise<string | null> {
  if (refreshInFlight) return refreshInFlight
  const refresh = getRefreshToken()
  if (!refresh) return null
  refreshInFlight = (async () => {
    try {
      const data = await rawFetch<import('@/types/api').RefreshResponse>(
        '/api/admin/refresh',
        { method: 'POST', body: { refresh_token: refresh }, skipAuth: true },
        null
      )
      setTokens(data.access_token)
      // refresh 응답의 must_change_password/role/branch_id는 AuthContext가 별도로 받아간다.
      return data.access_token
    } catch {
      return null
    } finally {
      refreshInFlight = null
    }
  })()
  return refreshInFlight
}

export async function apiFetch<T>(path: string, opts: FetchOpts = {}): Promise<T> {
  let access = getAccessToken()
  try {
    return await rawFetch<T>(path, opts, access)
  } catch (e) {
    if (e instanceof ApiException && e.status === 401 && !opts.skipAuth) {
      const newAccess = await refreshAccess()
      if (newAccess) {
        return await rawFetch<T>(path, opts, newAccess)
      }
      clearTokens()
      onForceLogout?.()
    }
    throw e
  }
}
```

핵심 동작:
- 401 + skipAuth=false → refresh 시도. 동시 다발 요청도 한 번의 refresh만 발생(`refreshInFlight` 공유).
- refresh 성공 → 새 access로 원 요청 재시도.
- refresh 실패(401·refresh 토큰 없음) → 토큰 폐기 + `onForceLogout` 콜백(AuthContext에서 등록한 라우터 navigate).
- 응답이 ok지만 status=204(예: 비번 변경 성공) → undefined 반환.

### 3. `src/context/AuthContext.tsx`

```tsx
import { createContext, useContext, useEffect, useState, ReactNode, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { apiFetch, setForceLogoutHandler, setTokens, clearTokens, getAccessToken, getRefreshToken } from '@/api/client'
import { LoginResponse } from '@/types/api'

interface AuthState {
  username: string | null
  role: 'global' | 'branch' | null
  branchId: number | null
  mustChangePassword: boolean
  isAuthenticated: boolean
}

interface AuthContextValue extends AuthState {
  login: (username: string, password: string) => Promise<LoginResponse>
  logout: () => Promise<void>
  refresh: () => Promise<void>
}

const Ctx = createContext<AuthContextValue | null>(null)
export function useAuth() {
  const v = useContext(Ctx)
  if (!v) throw new Error('useAuth must be used within AuthProvider')
  return v
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const navigate = useNavigate()
  const [state, setState] = useState<AuthState>(() => {
    const access = getAccessToken()
    return {
      username: null,
      role: null,
      branchId: null,
      mustChangePassword: false,
      isAuthenticated: !!access
    }
  })

  const forceLogout = useCallback(() => {
    setState({ username: null, role: null, branchId: null, mustChangePassword: false, isAuthenticated: false })
    navigate('/admin/login', { replace: true })
  }, [navigate])

  useEffect(() => {
    setForceLogoutHandler(forceLogout)
  }, [forceLogout])

  // 마운트 시 access가 있으면 refresh 호출해서 최신 role/branch_id/must_change_password 가져오기
  useEffect(() => {
    if (!getAccessToken() || !getRefreshToken()) return
    apiFetch<import('@/types/api').RefreshResponse>(
      '/api/admin/refresh',
      { method: 'POST', body: { refresh_token: getRefreshToken() }, skipAuth: true }
    ).then(data => {
      setTokens(data.access_token)
      setState({
        username: data.username,
        role: data.role,
        branchId: data.branch_id,
        mustChangePassword: data.must_change_password,
        isAuthenticated: true
      })
    }).catch(() => forceLogout())
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const login = useCallback(async (username: string, password: string) => {
    const data = await apiFetch<LoginResponse>(
      '/api/admin/login',
      { method: 'POST', body: { username, password }, skipAuth: true }
    )
    setTokens(data.access_token, data.refresh_token)
    setState({
      username: data.username,
      role: data.role,
      branchId: data.branch_id,
      mustChangePassword: data.must_change_password,
      isAuthenticated: true
    })
    return data
  }, [])

  const logout = useCallback(async () => {
    const refresh = getRefreshToken()
    try {
      if (refresh) await apiFetch('/api/admin/logout', { method: 'POST', body: { refresh_token: refresh } })
    } catch { /* 토큰 만료 등 무시 */ }
    clearTokens()
    setState({ username: null, role: null, branchId: null, mustChangePassword: false, isAuthenticated: false })
    navigate('/admin/login', { replace: true })
  }, [navigate])

  const refresh = useCallback(async () => { /* 외부에서 권한 변경 후 호출용 — 위 useEffect 로직 재사용 */ }, [])

  return <Ctx.Provider value={{ ...state, login, logout, refresh }}>{children}</Ctx.Provider>
}
```

### 4. `src/context/BranchContext.tsx`

```tsx
import { createContext, useContext, useState, ReactNode } from 'react'

interface BranchContextValue {
  branchId: number | null
  setBranchId: (id: number | null) => void
}

const Ctx = createContext<BranchContextValue | null>(null)
export function useBranch() {
  const v = useContext(Ctx)
  if (!v) throw new Error('useBranch must be used within BranchProvider')
  return v
}

export function BranchProvider({ children }: { children: ReactNode }) {
  const [branchId, setBranchIdState] = useState<number | null>(() => {
    const raw = localStorage.getItem('branchId')
    return raw ? parseInt(raw, 10) : null
  })

  const setBranchId = (id: number | null) => {
    if (id == null) localStorage.removeItem('branchId')
    else localStorage.setItem('branchId', String(id))
    setBranchIdState(id)
  }

  return <Ctx.Provider value={{ branchId, setBranchId }}>{children}</Ctx.Provider>
}
```

### 5. 공용 훅

`src/hooks/useIdempotencyKey.ts`:
```ts
import { useCallback, useState } from 'react'

export function useIdempotencyKey() {
  const [key, setKey] = useState(() => crypto.randomUUID())
  const regenerate = useCallback(() => setKey(crypto.randomUUID()), [])
  return { key, regenerate }
}
```

`src/hooks/useIdleTimeout.ts`:
```ts
import { useEffect, useRef } from 'react'

const EVENTS = ['mousemove', 'mousedown', 'keydown', 'touchstart', 'touchmove']

export function useIdleTimeout(ms: number, onIdle: () => void) {
  const cbRef = useRef(onIdle)
  cbRef.current = onIdle

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout>
    const reset = () => {
      clearTimeout(timer)
      timer = setTimeout(() => cbRef.current(), ms)
    }
    EVENTS.forEach(e => window.addEventListener(e, reset, { passive: true }))
    reset()
    return () => {
      clearTimeout(timer)
      EVENTS.forEach(e => window.removeEventListener(e, reset))
    }
  }, [ms])
}
```

### 6. MSW 셋업

`src/test-mocks/handlers.ts`:
```ts
import { http, HttpResponse } from 'msw'

export const handlers = [
  // step별 테스트에서 override하므로 여기선 catch-all만
  http.all('*', () => HttpResponse.json({ error: { code: 'NO_HANDLER', message: 'no msw handler' } }, { status: 500 }))
]
```

`src/test-mocks/server.ts`:
```ts
import { setupServer } from 'msw/node'
import { handlers } from './handlers'
export const server = setupServer(...handlers)
```

`src/test-setup.ts`:
```ts
import '@testing-library/jest-dom'
import { server } from './test-mocks/server'

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(() => server.resetHandlers())
afterAll(() => server.close())
```

### 7. App.tsx에 Provider 연결

```tsx
// src/App.tsx
import { Outlet } from 'react-router-dom'
import { AuthProvider } from '@/context/AuthContext'
import { BranchProvider } from '@/context/BranchContext'

export default function App() {
  return (
    <AuthProvider>
      <BranchProvider>
        <Outlet />
      </BranchProvider>
    </AuthProvider>
  )
}
```

`src/routes.tsx`를 App을 root로 감싸도록 수정 (`children: [...]` 형태).

### 8. 단위 테스트 (Vitest)

각 작성 항목별로 테스트 파일을 만든다(TDD — 최소 1개 정상 + N개 엣지).

- `src/api/client.test.ts`: 200 응답 파싱, 401 → refresh → 재시도, refresh 실패 → onForceLogout 호출, 동시 401 두 건이 refresh를 한 번만 호출, Idempotency-Key 헤더 첨부, 204 응답 처리. MSW로 mock.
- `src/hooks/useIdempotencyKey.test.tsx`: 마운트 시 UUID 발급, regenerate 호출 시 새 UUID, UUIDv4 정규식 매치.
- `src/hooks/useIdleTimeout.test.tsx`: timer 만료 시 onIdle 호출, 이벤트 발생 시 reset. `vi.useFakeTimers()`로 시간 조작.
- `src/context/AuthContext.test.tsx`: login 성공 → state 업데이트, logout 호출 → 토큰 폐기 + navigate, forceLogout 콜백 등록.
- `src/context/BranchContext.test.tsx`: localStorage 로드/저장, null 설정 시 remove.

## 핵심 규칙

- **frontend 전용 영역**: `frontend/src/` 외 변경 금지.
- **토큰은 localStorage**: `frontend/CLAUDE.md` 정책. 쿠키 미사용. XSS 위협을 인지하되 MVP는 이대로(다른 도메인에서 토큰 사용 안 하므로 CSRF는 무관).
- **`onUnhandledRequest: 'error'`**: 테스트에서 mock하지 않은 호출이 발생하면 즉시 실패(handler 누락 조기 발견).
- **`crypto.randomUUID`**: 브라우저 표준. 폴리필 추가 금지.
- **401 재시도는 1회만**: 재시도 후에도 401이면 강제 로그아웃. 무한 루프 방지.
- **PII 비노출**: API 에러 메시지를 그대로 토스트에 띄울 때 회원 PII가 포함되면 안 됨(에러 코드 기반 분기로 직접 메시지 작성). 백엔드가 이미 PII 비포함이지만 프론트도 방어.

## Acceptance Criteria

```bash
cd frontend
pnpm install     # step 2에서 했지만 무손실
pnpm lint
pnpm build
pnpm test
```

- `pnpm test`가 위 단위 테스트들을 통과한다.
- TypeScript strict가 통과한다(`tsc -b`).
- `src/api/client.ts`, `src/context/AuthContext.tsx`, `src/context/BranchContext.tsx`, `src/hooks/useIdempotencyKey.ts`, `src/hooks/useIdleTimeout.ts`, `src/types/api.ts`, `src/test-mocks/`, `src/test-setup.ts`가 모두 존재한다.
- 라우터에 AuthProvider/BranchProvider가 마운트돼 있어 페이지가 빈 라우트라도 Context를 사용할 수 있다.

## 검증 절차

1. AC 수행.
2. `code-reviewer` 호출 (권장). 입력: step 이름 + `git diff --stat`.
3. step3 status 갱신.

## 금지사항

- `backend/`, `db/`, `docs/`, `frontend/ui-design/`, `frontend/public/` 변경 금지.
- 토큰을 sessionStorage·쿠키·메모리 외 다른 저장소에 두는 변경 금지.
- 401 재시도를 2회 이상으로 늘리거나, refresh 동시 호출 deduplication을 제거하는 변경 금지.
- 외부 HTTP 라이브러리(axios, ky, ofetch 등) 추가 금지 — 표준 fetch만.
- 시간 의존 코드(`Date.now()` 직접 호출)를 훅 내부에서 사용 금지 — 테스트의 fake timer가 동작하도록 setTimeout만 사용.
- 비밀번호·토큰을 console.log·에러 메시지에 포함 금지.
