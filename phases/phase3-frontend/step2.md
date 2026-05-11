---
agent: frontend
---

# Step 2: Frontend 스캐폴드 (Vite + React + TS + Tailwind + Router + Query + PWA)

## 목표

`frontend/` 폴더에 Vite 기반 React 18 + TypeScript(strict) 프로젝트를 초기화하고, 이후 모든 step이 의존하는 **토대**를 마련한다.

이 step이 끝나면:
- `cd frontend && pnpm install`로 의존성이 깔리고, `pnpm dev`가 `http://localhost:5173`에서 빈 페이지("gym-check-in")를 띄운다.
- `pnpm build`가 통과해 `frontend/dist/`에 정적 산출물이 생긴다.
- `pnpm lint`가 통과한다.
- React Router 라우트 트리 (`/`, `/kiosk/*`, `/admin/*`)가 placeholder 컴포넌트로 잡혀 있고, 빈 라우트라도 클릭/이동이 동작한다.
- `frontend/ui-design/styles.css`의 CSS 변수가 `src/styles/tokens.css`로 복사돼 있고, Tailwind config가 이 변수들을 참조한다.
- `vite-plugin-pwa` 설정이 끝나(개발 모드에서 비활성, 빌드 시 manifest 생성) `public/manifest.webmanifest`가 등록돼 있다(아이콘은 step 14에서 사용자 제공으로 연결).

## 읽어야 할 파일

- `frontend/CLAUDE.md` — 디렉토리 구조, 명령어, 규칙
- `docs/ADR.md` — step 1에서 추가된 라이브러리 화이트리스트 (이 step에서 install할 패키지 목록의 정본)
- `docs/UI_GUIDE.md` — 색상·간격·타이포 토큰
- `frontend/ui-design/README.md`, `frontend/ui-design/styles.css` — 토큰 정본 (CSS 변수)
- `frontend/ui-design/kiosk-screens-1.jsx`, `kiosk-screens-2.jsx`, `admin-shell.jsx` — placeholder를 만들 때 시안 구조 참고 (이 step에선 거의 빈 컴포넌트지만 className은 미리 박아둬도 좋음)
- `docs/PRD.md` — 화면 목록 전체 확인

## 작업

### 1. 프로젝트 초기화

`.worktrees/frontend/frontend/` (또는 main의 `frontend/`)에서:

```bash
pnpm create vite . --template react-ts
# 또는 수동으로 package.json/tsconfig.json/vite.config.ts 작성
```

생성 후 즉시 다음 처리:
- `package.json`의 `"name"`을 `"gym-check-in-frontend"`로
- `"private": true`, `"type": "module"` 확인
- Vite 기본 생성물 중 불필요한 파일(예: `src/App.css`, 기본 로고)은 삭제

### 2. 의존성 설치

ADR-008(또는 ADR-011)에 등재된 패키지만 설치:

```bash
pnpm add react@18 react-dom@18 react-router-dom@6 @tanstack/react-query@5 clsx
pnpm add -D typescript@5 vite @vitejs/plugin-react tailwindcss postcss autoprefixer
pnpm add -D vite-plugin-pwa workbox-window
pnpm add -D vitest @testing-library/react @testing-library/jest-dom @testing-library/user-event jsdom
pnpm add -D msw@2
pnpm add -D @playwright/test
pnpm add -D eslint @typescript-eslint/parser @typescript-eslint/eslint-plugin eslint-plugin-react-hooks eslint-plugin-react-refresh
```

- ADR에 없는 패키지가 발견되면 즉시 `blocked` 처리하고 사용자 개입 요청.
- 버전은 `^` 캐럿으로 두되, lock 파일(`pnpm-lock.yaml`)을 커밋한다.

### 3. tsconfig.json (strict)

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "lib": ["ES2022", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "jsx": "react-jsx",
    "strict": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "noFallthroughCasesInSwitch": true,
    "noImplicitOverride": true,
    "resolveJsonModule": true,
    "isolatedModules": true,
    "esModuleInterop": true,
    "allowSyntheticDefaultImports": true,
    "skipLibCheck": true,
    "baseUrl": ".",
    "paths": { "@/*": ["src/*"] }
  },
  "include": ["src", "vite.config.ts", "vitest.config.ts"]
}
```

- `tsconfig.node.json`도 필요하면 별도 분리 (Vite 기본 템플릿 따름).

### 4. vite.config.ts

```ts
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { VitePWA } from 'vite-plugin-pwa'
import path from 'path'

export default defineConfig({
  plugins: [
    react(),
    VitePWA({
      registerType: 'autoUpdate',
      includeAssets: ['favicon.ico'],
      manifest: {
        name: '체육관 체크인',
        short_name: '체크인',
        description: '체육관 회원 셀프 체크인 키오스크',
        theme_color: '#0a0a0a',
        background_color: '#0a0a0a',
        display: 'fullscreen',
        orientation: 'any',
        start_url: '/',
        icons: [
          // step 14에서 사용자 제공 아이콘으로 채움. 지금은 빈 배열 또는 placeholder.
        ]
      },
      devOptions: { enabled: false }
    })
  ],
  resolve: { alias: { '@': path.resolve(__dirname, './src') } },
  server: { port: 5173 }
})
```

- `display: 'fullscreen'`은 ADR-005 키오스크 풀스크린 결정의 핵심.
- `start_url: '/'`로 둬서 홈 화면 아이콘 탭 시 키오스크로 진입.

### 5. Tailwind 설정 + tokens.css

```bash
npx tailwindcss init -p
```

`tailwind.config.ts`:
- `content`: `['./index.html', './src/**/*.{ts,tsx}']`
- `theme.extend.colors`: `frontend/ui-design/styles.css`의 CSS 변수(`--pb-*`, `--k-*`, `--a-*`, `--s-*`)를 참조하는 형태. 예: `kiosk: { bg: 'var(--k-bg)', surface: 'var(--k-surface)', ... }`
- `theme.extend.fontFamily`: `pretendard` / `display`(Space Grotesk) / `mono`(JetBrains Mono)
- `theme.extend.spacing`, `theme.extend.borderRadius`도 시안 CSS 변수 참조

**중요**: `frontend/ui-design/styles.css`를 `frontend/src/styles/tokens.css`로 **그대로 복사**(주석 포함)한다. 이후 토큰 수정은 ui-design 폴더가 아닌 src/styles/tokens.css에서 하지만, 이 step에선 무손실 복사가 목적.

`src/index.css`:
```css
@import './styles/tokens.css';
@tailwind base;
@tailwind components;
@tailwind utilities;

html, body, #root { height: 100%; }
body { font-family: 'Pretendard', system-ui, sans-serif; }
```

### 6. 디렉토리 구조

`frontend/CLAUDE.md`의 디렉토리 구조 그대로 빈 폴더 + placeholder 파일 생성:

```
frontend/
├── src/
│   ├── pages/
│   │   ├── kiosk/
│   │   │   ├── BranchSetup.tsx
│   │   │   ├── Idle.tsx
│   │   │   ├── InputSelect.tsx
│   │   │   ├── VoiceSearch.tsx
│   │   │   ├── TypingSearch.tsx
│   │   │   ├── MemberPick.tsx
│   │   │   └── CheckInDone.tsx
│   │   └── admin/
│   │       ├── Login.tsx
│   │       ├── PasswordChange.tsx
│   │       ├── Members/index.tsx
│   │       ├── Memberships/index.tsx
│   │       ├── BulkExtend.tsx
│   │       ├── CheckIns/index.tsx
│   │       ├── Sales/index.tsx
│   │       ├── Admins/index.tsx
│   │       └── Branches/index.tsx
│   ├── components/        # 빈 폴더 (이후 step에서 채움)
│   ├── api/               # 빈 폴더
│   ├── hooks/             # 빈 폴더
│   ├── context/           # 빈 폴더
│   ├── types/             # 빈 폴더
│   ├── styles/
│   │   ├── tokens.css     # ui-design/styles.css 복사본
│   │   └── index.css
│   ├── App.tsx
│   ├── main.tsx
│   └── routes.tsx
├── index.html
├── public/
│   └── manifest.webmanifest   # vite-plugin-pwa가 생성하지만 빈 placeholder도 둠
├── vite.config.ts
├── tailwind.config.ts
├── tsconfig.json
├── tsconfig.node.json
├── vitest.config.ts
├── playwright.config.ts
├── .eslintrc.cjs
├── .env.example
├── package.json
└── pnpm-lock.yaml
```

각 placeholder 페이지는 다음 형태로 최소 본문 1개:

```tsx
// src/pages/kiosk/Idle.tsx
export default function Idle() {
  return <div className="p-8 text-2xl">Idle (TBD)</div>
}
```

### 7. 라우팅 (`src/routes.tsx`, `src/App.tsx`, `src/main.tsx`)

```tsx
// src/routes.tsx
import { createBrowserRouter, Navigate } from 'react-router-dom'
import Idle from '@/pages/kiosk/Idle'
import BranchSetup from '@/pages/kiosk/BranchSetup'
// ... (다른 placeholder import)
import AdminLogin from '@/pages/admin/Login'

export const router = createBrowserRouter([
  { path: '/', element: <Navigate to="/kiosk/idle" replace /> },
  { path: '/kiosk/setup', element: <BranchSetup /> },
  { path: '/kiosk/idle', element: <Idle /> },
  { path: '/kiosk/input', element: <InputSelect /> },
  { path: '/kiosk/voice', element: <VoiceSearch /> },
  { path: '/kiosk/typing', element: <TypingSearch /> },
  { path: '/kiosk/pick', element: <MemberPick /> },
  { path: '/kiosk/done', element: <CheckInDone /> },
  { path: '/admin/login', element: <AdminLogin /> },
  { path: '/admin/password', element: <PasswordChange /> },
  { path: '/admin/members/*', element: <Members /> },
  { path: '/admin/memberships/*', element: <Memberships /> },
  { path: '/admin/check-ins', element: <CheckIns /> },
  { path: '/admin/sales', element: <Sales /> },
  { path: '/admin/bulk-extend', element: <BulkExtend /> },
  { path: '/admin/branches', element: <Branches /> },
  { path: '/admin/admins', element: <Admins /> }
])
```

`src/App.tsx`는 RouterProvider만 감싸고, TanStack QueryClientProvider는 `src/main.tsx`에서.

```tsx
// src/main.tsx
import React from 'react'
import ReactDOM from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from 'react-router-dom'
import { router } from './routes'
import './styles/index.css'

const qc = new QueryClient({ defaultOptions: { queries: { retry: 1, refetchOnWindowFocus: false } } })
ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <QueryClientProvider client={qc}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </React.StrictMode>
)
```

이 step에선 AuthContext/BranchContext는 도입하지 않는다(step 3에서).

### 8. `.env.example`

```
# Backend API URL (개발은 http://localhost:8080, 운영은 https://...)
VITE_API_URL=http://localhost:8080
```

`.env`는 `.gitignore`에 이미 있어야 함(루트 `.gitignore`가 커버하는지 확인, 부족하면 frontend 단에 `.env` 라인 추가).

### 9. ESLint + Vitest 설정

`.eslintrc.cjs`: React 권장 + TypeScript + react-hooks + react-refresh.
`vitest.config.ts`: `environment: 'jsdom'`, `setupFiles: ['./src/test-setup.ts']` (`@testing-library/jest-dom` import만).
`playwright.config.ts`: 빈 스켈레톤 — 실제 셋업은 step 15. 단 `pnpm test:e2e` 명령이 명령어로 등록은 돼 있어야(빈 상태에선 `--list`만).

### 10. package.json scripts

```json
{
  "scripts": {
    "dev": "vite",
    "build": "tsc -b && vite build",
    "preview": "vite preview",
    "lint": "eslint . --max-warnings=0",
    "test": "vitest run",
    "test:watch": "vitest",
    "test:e2e": "playwright test"
  }
}
```

## 핵심 규칙

- **frontend agent 전용**: `backend/`·`db/`·`docs/` 변경 금지(hook이 차단). 이 step의 출력은 모두 `frontend/` 아래.
- **CSS 변수 직접 수정 금지**: `frontend/ui-design/styles.css`는 참조 정본. 복사본인 `src/styles/tokens.css`도 이 step에선 무손실 복사. 토큰 추가는 향후 step에서.
- **placeholder는 빈 div + "TBD" 정도로만**. 실제 화면 구현은 이후 step. 라우팅이 동작하는지 확인이 목적.
- **dev 서버 자동 실행 금지**: agent는 worktree에서 dev 서버를 백그라운드로 띄우지 않는다. 빌드·테스트 명령으로만 검증.
- 정적 자산(`public/`)의 favicon은 임시 빈 ICO 또는 vite 기본값 그대로. PWA 아이콘은 step 14에서 사용자 제공.
- **외부 STT SDK·UI 라이브러리·날짜 라이브러리는 추가 금지** (ADR-008/011).

## Acceptance Criteria

`.worktrees/frontend/frontend/`에서 실행:

```bash
pnpm install
pnpm lint
pnpm build
pnpm test        # 아직 테스트 파일 없으면 "no tests found"여도 OK (exit 0)
```

추가 검증:

1. `pnpm dev`를 직접 띄우지 않더라도, `pnpm build`로 생성된 `dist/index.html`을 열어보면 React 마운트 root 엘리먼트 + manifest 링크가 있음(curl/grep로 확인 가능).
2. `dist/manifest.webmanifest`에 `display: fullscreen`이 포함돼 있음(vite-plugin-pwa가 생성).
3. `tailwind.config.ts`가 `var(--k-bg)` 등 CSS 변수를 참조하는 형태로 etheme.extend.colors를 정의(grep로 확인).
4. `src/styles/tokens.css`가 `frontend/ui-design/styles.css`와 동일 (diff 0).
5. `tsc --noEmit`이 strict 모드에서 통과.
6. ESLint가 `--max-warnings=0`로 통과.

## 검증 절차

1. AC 모두 수행.
2. `code-reviewer` 호출 (선택). 입력: step 이름 + `git diff --stat`.
3. `phases/phase3-frontend/index.json`의 step2 status 갱신.

## 금지사항

- `backend/`, `db/`, `docs/`, 루트 `CLAUDE.md` 변경 금지.
- ADR-008/011에 없는 패키지 install 금지.
- placeholder 페이지에서 실제 API 호출·fetch·localStorage 사용 금지(아직 api-client가 없음).
- `frontend/ui-design/` 폴더 내용 수정 금지 (참조 전용).
- dev 서버를 백그라운드로 띄우는 명령 실행 금지 (포트 충돌·정리 문제).
