---
agent: frontend
---

# Step 15: Playwright e2e — 골든 패스 자동화

## 목표

Playwright를 셋업하고, 사용자가 수동으로 검증해야 하는 시나리오의 ~80%를 자동화한다. 골든 패스 12~15개. 음성·시각 디자인은 자동화 범위 밖으로 명시.

이 step이 끝나면:
- `pnpm test:e2e`가 backend + dev 서버를 자동 기동하고, 시드 데이터를 truncate + reseed한 뒤, 헤드리스 Chromium으로 12~15 시나리오를 실행해 모두 통과한다.
- 시나리오 1개당 5~30초, 전체 3~6분 안에 완료.
- 실패 시 스크린샷·trace 파일이 `test-results/`에 저장돼 디버깅 가능.
- 음성·실기기 터치·시각 디자인 품질은 자동화 범위 밖임이 README/playwright.config 주석에 명시.

## 읽어야 할 파일

- `frontend/CLAUDE.md` — 키오스크 플로우, 관리자 화면, idle 타임아웃
- `backend/CLAUDE.md` — 시드 데이터, JWT 만료, 5초 LRU 멱등성
- `db/CLAUDE.md` — TRUNCATE 전략, 시드 적용
- `docs/TESTING.md` — 결정성 규칙
- step 2의 playwright 스켈레톤, step 4~13의 모든 화면

## 작업

### 1. Playwright 셋업

```bash
pnpm dlx playwright install chromium  # 브라우저 다운로드 (~150MB)
# WSL2/Linux 의존 라이브러리:
sudo pnpm dlx playwright install-deps chromium  # 사용자 sudo 필요
```

위 의존 명령이 sudo 필요 시 hook이 차단할 수 있으므로, **사용자에게 1회 실행을 안내**하고 step.md AC에서는 이미 깔린 상태를 가정.

### 2. `playwright.config.ts`

```ts
import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  expect: { timeout: 5_000 },
  fullyParallel: false,  // DB 시드 공유, 직렬 실행
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL: process.env.E2E_BASE_URL ?? 'http://localhost:5174',  // dev 서버와 충돌 방지
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    viewport: { width: 1280, height: 800 }
  },
  webServer: [
    {
      command: 'go run ./backend/cmd/server',
      cwd: '..',
      port: 18081,
      env: {
        PORT: '18081',
        DATABASE_URL: process.env.E2E_DATABASE_URL ?? 'postgres://gym:changeme@localhost:5432/gym_e2e?sslmode=disable',
        APP_ENV: 'dev',
        JWT_ACCESS_SECRET: 'e2e-access-secret-32-chars-min-zzzz',
        JWT_REFRESH_SECRET: 'e2e-refresh-secret-32-chars-min-z'
      },
      reuseExistingServer: !process.env.CI,
      timeout: 30_000
    },
    {
      command: 'pnpm dev --port 5174',
      port: 5174,
      env: { VITE_API_URL: 'http://localhost:18081' },
      reuseExistingServer: !process.env.CI,
      timeout: 30_000
    }
  ],
  projects: [
    { name: 'chromium-desktop', use: { ...devices['Desktop Chrome'] } },
    { name: 'chromium-tablet', use: { ...devices['iPad Pro 11'] } }
  ]
})
```

핵심:
- backend는 `:18081`, frontend dev는 `:5174` — 본 dev 서버 `:8080`/`:5173`과 분리해 충돌 회피.
- 별도 DB `gym_e2e` 사용 — 운영/개발/단위 테스트 DB와 격리.
- `fullyParallel: false`, `workers: 1` — 시드를 공유하므로 직렬.
- 태블릿 viewport 별도 project로 키오스크 시나리오 검증.

### 3. 시드/리셋 헬퍼

`e2e/fixtures/db.ts`:
```ts
import { Pool } from 'pg'

const pool = new Pool({ connectionString: process.env.E2E_DATABASE_URL ?? '...' })

export async function resetDatabase() {
  await pool.query(`TRUNCATE TABLE
    check_ins, payments, membership_events, memberships, members,
    revoked_refresh_tokens, admin_audit_logs, idempotency_keys,
    admins, branches RESTART IDENTITY CASCADE`)
}

export async function seedBaseline() {
  // 시드: 1개 지점, 2명 관리자(global, branch), 일부 회원·회원권은 시나리오별 생성
  await pool.query(`INSERT INTO branches (id, name, address) VALUES (1, 'E2E Branch', 'E2E Address')`)
  // bcrypt 해시(평문 'TestPass1')는 미리 계산해 두거나 backend cmd/hashpw로 생성. 평문 노출 금지 정책에 따라, e2e 전용 해시는 별도 fixture 파일로 분리(테스트 한정 OK).
  const hash = process.env.E2E_GLOBAL_PASSWORD_HASH ?? '$2a$12$...'  // 사용자가 한 번 생성 후 fixture/env에 박음
  await pool.query(`INSERT INTO admins (id, username, password_hash, role, must_change_password)
    VALUES (1, 'globalAdmin', $1, 'global', false)`, [hash])
}

export async function setSystemTime(_iso: string) {
  // 백엔드는 자체 Clock 인터페이스를 사용하지만 e2e에선 wall-clock. 시간 조작이 필요한 시나리오는 DB 컬럼 직접 update로 우회(예: memberships.end_date를 과거로 설정 후 배치 시뮬).
}

export async function closeDb() { await pool.end() }
```

`e2e/fixtures/admin.ts`:
```ts
import { test as base } from '@playwright/test'
import { resetDatabase, seedBaseline, closeDb } from './db'

export const test = base.extend({
  reset: [async ({}, use) => {
    await resetDatabase()
    await seedBaseline()
    await use(undefined)
  }, { auto: true }]
})
```

각 테스트 실행 전 DB가 시드된 baseline으로 리셋.

### 4. e2e 시나리오 (12~15개)

`e2e/auth.spec.ts`:
1. **로그인 → 비번 변경 → 재로그인**: globalAdmin 시드는 must_change_password=false지만, 임시 비번 리셋 후 강제 변경 시나리오를 별도로.
2. **로그인 실패 5회 → 잠금**: 같은 username으로 잘못된 비번 5회 → 6번째 시도에 `ACCOUNT_LOCKED` + 카운트다운.
3. **JWT 30분 만료 → 자동 refresh**: localStorage의 access_token을 만료된 토큰으로 교체 후 API 호출 → 자동 refresh → 재시도 성공.
4. **비번 변경 후 stale access 401**: 두 탭 시뮬레이션은 어려우므로, page1에서 비번 변경 → page1의 access는 새 토큰이지만 직접 만든 stale access로 API 호출 → 401.

`e2e/members.spec.ts`:
5. **회원 등록 → 부여(결제) → 매출 1건**: 신규 회원 → 회원권 부여 monthly 3개월 + 카드 150000원 → 매출 페이지에서 +150000 확인.
6. **회원권 정지 등록 + cancel-pause**: 미래 정지 → 만료일 연장 확인 → cancel-pause → 원복.
7. **환불 → 음수 결제 + 매출 net 변동**: active 회원권 환불 → 매출에서 net -150000.
8. **BulkExtend + 충돌**: 미래 회원권 미리 등록 후 BulkExtend → 409 + first_conflict_membership_id 링크 → 클릭 → 회원권 상세.

`e2e/kiosk.spec.ts` (project: chromium-tablet):
9. **키오스크 검색 (이름) → 체크인 → 오늘 카운트 +1**: 시드 회원 1명 → /kiosk/typing 이름 입력 → MemberPick → 체크인 → /kiosk/idle 카운트 검증.
10. **검색 결과 21명 → truncated 배너**: 같은 이름 21명 시드 → 검색 → truncated 배너 + 결과 20개.
11. **MEMBERSHIP_NOT_STARTED**: 미래 시작 회원권만 가진 회원을 인위로 만들고 검색(active 필터 우회는 DB 직접 시드로) → 검색에서 제외됨 또는 race로 통과 시 422 분기.
12. **키오스크 5초 롱프레스 → BranchSetup**: 우상단 64px 영역을 `page.mouse.down() + page.waitForTimeout(5100) + page.mouse.up()` → /kiosk/setup 진입.
13. **idle 타임아웃**: /kiosk/input에서 10초 무입력 → /kiosk/idle 자동 복귀.

`e2e/security.spec.ts`:
14. **지점 관리자 다른 지점 회원 접근**: branchAdmin 로그인 → 다른 지점 회원 URL 직접 접근 → 404.
15. **임시 비번 발급 → 강제 변경**: globalAdmin이 branchAdmin 비번 리셋 → 임시비번 1회 표시(localStorage spy로 미저장 검증) → branchAdmin 로그인 → must_change_password 강제.

각 spec 파일 상단에 시나리오 번호·목적 주석.

### 5. 자동화 범위 외 (README + config 주석)

`e2e/README.md`:
```markdown
# E2E (Playwright)

## 자동화 범위
- 로그인·비번 변경·잠금·토큰 갱신
- 회원 등록·회원권 부여·환불·정지
- 키오스크 검색·체크인·idle 타임아웃·롱프레스
- 매출 분리 표시
- BulkExtend 충돌

## 자동화 제외
- **Web Speech API 음성 인식**: headless에서 실제 마이크 입력 불가. `window.SpeechRecognition` stub으로 결과 텍스트 분기만 검증(VoiceSearch.test.tsx Vitest 영역).
- **시각 디자인 품질**: 색·여백·폰트가 시안과 일치하는지는 사람이 봐야 함. 스크린샷은 trace에 남으므로 PR 리뷰에서 확인.
- **실기기 터치 감도**: Playwright 태블릿 emulation은 실제 갤럭시탭/iPad와 미묘하게 다를 수 있음. 마지막 검수는 실기기에서.
- **PWA 홈 화면 추가 + 풀스크린 진입**: manifest 필드는 자동 검증, 실제 추가는 OS 기능이라 자동화 불가.

## 실행
\`\`\`bash
pnpm test:e2e            # 모든 시나리오
pnpm test:e2e --ui       # UI 모드(개발 중 디버깅)
pnpm test:e2e --project=chromium-tablet  # 키오스크만
\`\`\`

## 시드
각 테스트 전 \`resetDatabase()\` + \`seedBaseline()\`로 격리.
시드 관리자 비번: 환경변수 \`E2E_GLOBAL_PASSWORD_HASH\`(bcrypt 해시).
평문은 \`E2E_GLOBAL_PASSWORD\`로 fixture에서만 참조, 코드/로그에 남기지 않음.
```

### 6. 환경변수 가이드

루트 `.env.example`에 e2e 전용 변수 라인 추가(스키마만):

```
E2E_DATABASE_URL=postgres://gym:changeme@localhost:5432/gym_e2e?sslmode=disable
E2E_GLOBAL_PASSWORD=          # 평문 (.env에만, 절대 커밋 금지)
E2E_GLOBAL_PASSWORD_HASH=     # 위 평문의 bcrypt 해시 (cmd/hashpw로 생성)
```

(이 step은 frontend 전용이지만 `.env.example`은 공유 파일이라 hook이 차단할 수 있음 → 그러면 별도 `frontend/.env.example.e2e`에 두고 OPERATIONS에 안내.)

### 7. DB 준비

사용자가 1회 실행:
```bash
docker compose exec db createdb -U gym gym_e2e
DATABASE_URL=postgres://gym:changeme@localhost:5432/gym_e2e?sslmode=disable \
  goose -dir db/migrations postgres "$DATABASE_URL" up
```

step.md AC에서는 위가 사전 조건. 시드 데이터(branches/admins)는 fixture가 매번 reset.

## 핵심 규칙

- **결정성**: time 관련 시나리오는 wall-clock에 의존하면 flaky. DB 컬럼을 직접 update해서 "이미 만료된 회원권" 상태를 만든 뒤 검증.
- **시드 격리**: `gym_e2e` DB 별도. 본 dev DB(`gym`) 절대 건드리지 않는다.
- **워커 1개**: 직렬 실행. 병렬 시 DB 시드 충돌.
- **음성·시각·실기기는 명시적 제외**: README + config 주석에 박는다. 사용자가 자동화 기대치를 정확히 알게.
- **PII 비노출**: 시나리오에서 회원 PII가 화면에 표시되지만, console.log·trace에 별도 출력하지 않는다(Playwright trace는 스크린샷에 들어가므로 PR 리뷰에서 외부 유출 주의).
- **시크릿**: E2E_*_SECRET은 e2e 전용 dummy 값. 운영 비밀키와 다르게.

## Acceptance Criteria

```bash
# DB 준비 (1회)
docker compose exec db createdb -U gym gym_e2e
DATABASE_URL=postgres://gym:changeme@localhost:5432/gym_e2e?sslmode=disable \
  goose -dir db/migrations postgres "$DATABASE_URL" up

# 의존 라이브러리 (1회, sudo)
sudo pnpm dlx playwright install-deps chromium

# e2e 실행
cd frontend
pnpm test:e2e
```

- 12~15 시나리오 모두 PASS.
- 전체 실행 시간 6분 이내.
- 실패 시 `test-results/`에 trace + 스크린샷 + video.

수동 확인:
- `pnpm test:e2e --ui`로 UI 모드 진입 → 각 시나리오 step별 시각 확인.
- 시안과 비교(스크린샷 첨부).

## 검증 절차

1. AC.
2. **`code-reviewer` 호출 (권장)**. 보안 시나리오(15) 자동화 범위 적절성 검토.
3. step15 status 갱신.
4. `phases/index.json`에서 phase3-frontend를 `completed`로 마크 (마지막 step).

## 금지사항

- e2e가 본 dev DB(`gym`)를 건드리지 마라 — `gym_e2e` 격리.
- 시나리오에서 평문 비밀번호를 spec 파일에 하드코딩 금지 — env 또는 fixture로 분리. **e2e 전용 평문은 fixture 파일에 둬도 OK지만 `.env`처럼 `.gitignore` 처리**.
- 시드 데이터에 실제 회원 PII(실명·실전화) 사용 금지 — `테스트`·`010-1234-XXXX` 더미.
- `fullyParallel: true`로 두지 마라(DB 시드 충돌).
- 음성 인식을 e2e에서 실제로 호출하려 시도 금지(headless 미지원, flaky).
- Playwright trace를 PR/리포지토리에 커밋 금지(`test-results/`는 `.gitignore`).
- 시각 회귀 테스트(visual regression)를 이번 step에 추가하지 마라(scope 초과, Phase 5+).
