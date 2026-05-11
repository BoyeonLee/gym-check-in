---
agent: frontend
---

# Step 14: PWA 마무리 + Vitest 단위/컴포넌트 테스트 정합

## 목표

Phase 3의 모든 화면이 들어온 시점에서 (1) PWA 매니페스트·아이콘을 사용자 제공 자산으로 마무리, (2) 누락된 Vitest 단위/컴포넌트 테스트를 보강해 커버리지 기준선을 맞춘다.

이 step이 끝나면:
- `public/icons/pwa-192x192.png`, `pwa-512x512.png`, `pwa-maskable-512x512.png` 세 파일이 존재하고 `manifest.webmanifest`에 등록돼 있다.
- `pnpm build`로 생성된 `dist/manifest.webmanifest`가 다음 필드를 가진다: `name`, `short_name`, `display: fullscreen`, `start_url: /`, `theme_color`, `background_color`, `icons[]`(3개).
- `vite-plugin-pwa`가 service worker를 생성하고, `autoUpdate` 모드로 새 빌드 시 사용자 새로고침 없이 캐시 갱신.
- Vitest 단위/컴포넌트 테스트 커버리지가 핸들러 80% / 도메인 90%를 기준선으로(`docs/TESTING.md`).
- step 2~13에서 빠뜨린 테스트(특히 통합형 시나리오 — 로그아웃, 토큰 갱신 deduplication, KST 포맷 등)가 보강돼 있다.

## 읽어야 할 파일

- `frontend/CLAUDE.md` — PWA 매니페스트, 풀스크린 정책
- `docs/ADR.md` — ADR-005 PWA 결정 (step 1에서 라이브러리 화이트리스트도 갱신됨)
- `docs/TESTING.md` — 커버리지 기준선·결정성 규칙
- `frontend/ui-design/assets/pboy-logo.jpg` — 사용자 제공 로고 (PWA 아이콘 원본)
- step 2~13의 모든 산출물 (테스트 보강 대상 식별)

## 작업

### 1. PWA 아이콘 배치

사용자가 제공한 PWA 아이콘 파일을 `frontend/public/icons/`에 배치:
- `pwa-192x192.png` — 192×192 PNG, 홈 화면 작은 아이콘
- `pwa-512x512.png` — 512×512 PNG, 스플래시·큰 아이콘
- `pwa-maskable-512x512.png` — 512×512 PNG, Android 라운드 마스크 대응(안전 영역 = 중앙 80%)

**파일이 누락된 경우**: 즉시 `blocked` 처리하고 사용자에게 요청. 임의 생성·다른 이미지로 대체 금지.

(이미지 형식 검증: `file` 명령으로 PNG 확인. ImageMagick 같은 도구로 사이즈도 확인 가능 — 단, 외부 도구 의존은 최소화하고 파일 존재 여부만 자동 검증.)

### 2. `vite.config.ts`의 PWA 설정 갱신 (step 2의 placeholder 채움)

```ts
VitePWA({
  registerType: 'autoUpdate',
  includeAssets: ['favicon.ico'],
  manifest: {
    name: '체육관 체크인',
    short_name: '체크인',
    description: '체육관 회원 셀프 체크인 키오스크',
    theme_color: '#0a0a0a',  // 시안 토큰 일치
    background_color: '#0a0a0a',
    display: 'fullscreen',
    orientation: 'any',
    start_url: '/',
    scope: '/',
    icons: [
      { src: '/icons/pwa-192x192.png', sizes: '192x192', type: 'image/png' },
      { src: '/icons/pwa-512x512.png', sizes: '512x512', type: 'image/png' },
      { src: '/icons/pwa-maskable-512x512.png', sizes: '512x512', type: 'image/png', purpose: 'maskable' }
    ]
  },
  workbox: {
    globPatterns: ['**/*.{js,css,html,svg,png,jpg,jpeg,woff2}'],
    runtimeCaching: [
      {
        // API 호출은 캐시하지 않는다 (체크인·매출·회원 등 모두 신선해야 함)
        urlPattern: ({ url }) => url.pathname.startsWith('/api/'),
        handler: 'NetworkOnly'
      }
    ],
    navigateFallbackDenylist: [/^\/api\//]
  },
  devOptions: { enabled: false }
})
```

theme_color는 시안의 키오스크 배경(`--k-bg` 등)과 시각적으로 일관되게.

### 3. service worker 동작 점검

- 빌드 후 `dist/sw.js`가 생성되는지 확인.
- `dist/manifest.webmanifest`가 생성되고 위 필드를 포함하는지 확인.
- 빌드 산출물에 아이콘 파일 3개가 복사됐는지 (`dist/icons/`).

자동 검증 스크립트:
```bash
node -e "
  const m = require('./dist/manifest.webmanifest');
  // dist/manifest.webmanifest는 JSON이므로 fs.readFile로 읽어 JSON.parse
  // (require가 .webmanifest 확장자를 지원 안 할 수 있음 — fs로 변경)
"
```

또는 단순히:
```bash
test -f dist/manifest.webmanifest && grep -q '"display":"fullscreen"' dist/manifest.webmanifest
test -f dist/icons/pwa-192x192.png
test -f dist/icons/pwa-512x512.png
test -f dist/icons/pwa-maskable-512x512.png
test -f dist/sw.js
```

### 4. Vitest 커버리지 측정

`vitest.config.ts` 갱신:
```ts
export default defineConfig({
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test-setup.ts'],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'html'],
      thresholds: {
        lines: 75,
        functions: 75,
        branches: 70,
        statements: 75
      },
      exclude: [
        'src/main.tsx',
        'src/routes.tsx',
        'src/test-mocks/**',
        '**/*.test.*',
        'src/styles/**'
      ]
    }
  }
})
```

(70~80%대 임계값 — `docs/TESTING.md`의 핸들러 80%·도메인 90%에 정확히 맞추기 어렵지만 비슷한 수준. UI 컴포넌트는 시각 회귀 테스트가 없는 한 100% 달성이 비현실적이므로 70%대로.)

### 5. 누락 테스트 보강

step 2~13 작성 결과를 git diff로 훑고 다음 영역을 점검:

- **api/client.ts**: 동시 401 → refresh 1회만 호출 (중요), 204 응답, Idempotency-Key 헤더 첨부.
- **AuthContext**: 마운트 시 refresh 자동 호출, forceLogout 시 navigate, login 후 must_change_password 분기.
- **format.ts**: formatPhone(잘못된 입력 그대로), formatAmount(0/음수), formatDate, formatPaymentMethod.
- **dates.ts**: addMonths 월말 보정, 윤년, todayKST 자정 경계.
- **password.ts**: 강도 검증 4 케이스.
- **MemberPick**: race 시나리오(검색 응답 후 빠른 탭).
- **PauseDialog**: EXCLUDE 위반 → 인라인.
- **RefundDialog**: 같은 키 이중 호출.
- **BulkExtend**: first_conflict 표시.
- **ResetPasswordDialog**: localStorage spy로 임시 비번 미저장 검증.

각 영역에 빠진 테스트가 발견되면 추가. **이 step에서 새 화면을 만들지 마라** — 테스트와 PWA 자산만.

### 6. `package.json` scripts 보강

```json
{
  "scripts": {
    "test": "vitest run",
    "test:coverage": "vitest run --coverage",
    "test:watch": "vitest"
  }
}
```

### 7. CI 호환 (참고)

이 step에서 CI 파이프라인을 만들지는 않지만, 향후 Phase 4 CI 통합을 위해 다음 명령이 한 줄로 실행 가능해야 한다:

```bash
pnpm install --frozen-lockfile && pnpm lint && pnpm test:coverage && pnpm build
```

## 핵심 규칙

- **PWA 아이콘은 사용자 제공만**: 임의 생성·placeholder PNG로 대체 금지. 없으면 blocked.
- **service worker는 API 캐시 금지**: `/api/*`는 NetworkOnly. 캐시되면 회원·매출 데이터가 stale로 표시될 위험.
- **`display: fullscreen`**: ADR-005. `standalone`으로 바꾸지 마라.
- **theme_color는 시안 토큰과 일치**: 시각적 일관성.
- **커버리지 임계값은 점진적**: 너무 엄격하면 CI 깨짐. 70%대로 시작, Phase 4에서 80%로 상향 검토.
- **테스트 보강은 누락분만**: 이미 있는 테스트를 중복 작성하지 마라.

## Acceptance Criteria

```bash
cd frontend
pnpm install --frozen-lockfile
pnpm lint
pnpm test:coverage
pnpm build

# 빌드 산출물 검증
test -f dist/manifest.webmanifest
grep -q '"display":"fullscreen"' dist/manifest.webmanifest
grep -q 'pwa-512x512.png' dist/manifest.webmanifest
test -f dist/icons/pwa-192x192.png
test -f dist/icons/pwa-512x512.png
test -f dist/icons/pwa-maskable-512x512.png
test -f dist/sw.js

# 커버리지 임계값
# (test:coverage가 자동으로 임계값 미달 시 exit 1)
```

수동 (브라우저):
- `pnpm preview`로 빌드 산출물 미리보기.
- Chrome DevTools > Application > Manifest 탭에서 모든 필드·아이콘 표시.
- "홈 화면에 추가" 시뮬레이션 (DevTools > Application > Add to home screen) — 풀스크린 진입.
- 태블릿 실기기에서 홈 화면 추가 → 아이콘 탭 → 풀스크린 (Phase 4 OPERATIONS에서 다시 안내).

## 검증 절차

1. AC.
2. step14 status 갱신.

## 금지사항

- 임의 PWA 아이콘 생성 금지 — 사용자 제공만.
- `/api/*` 응답을 service worker가 캐시하게 두지 마라.
- `display`를 `standalone`/`browser`로 변경 금지(ADR-005 위반).
- 커버리지 임계값을 0으로 두지 마라(차라리 50%대라도 박아 회귀 방지).
- 테스트 추가 명목으로 새 화면·기능을 추가하지 마라(이 step은 보강 전용).
- 빌드 시 console.log·디버그 코드 잔존 금지.
