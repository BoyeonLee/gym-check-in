---
agent: frontend
---

# Step 6: 키오스크 검색 (InputSelect + VoiceSearch + TypingSearch)

## 목표

회원이 자기 자신을 찾는 화면을 만든다. 음성 인식 or 타이핑 분기 → 검색 결과로 다음 화면(MemberPick)에 데이터 전달.

이 step이 끝나면:
- `/kiosk/input`에서 "음성으로 찾기" / "직접 입력" 두 버튼. Web Speech API 미지원 환경에서는 음성 버튼이 비활성/숨김.
- `/kiosk/voice`는 마이크 권한 요청 → 인식 결과를 백엔드 검색(`mode=name`)으로 보냄. 3회 실패 누적 시 자동으로 `/kiosk/typing`. NotAllowedError(권한 거부) 시 즉시 typing 폴백 + 토스트.
- `/kiosk/typing`은 3탭(이름 / 전화 뒷자리 4자리 / 회원 번호)로 분기, 각 모드에 맞는 입력 UI.
- 검색 결과를 React Query 캐시에 두고, MemberPick에서 같은 키로 읽도록 한다(state 전달이 아닌 URL 쿼리 or query cache).
- 모든 검색 화면에 `useIdleTimeout(10000)` 적용 → 10초 무입력 시 `/kiosk/idle`로.

## 읽어야 할 파일

- `frontend/CLAUDE.md` — TypingSearch 3탭 분기, useSpeechRecognition 가용성 체크, 마이크 권한 거부, idle 10s, truncated 배너
- `docs/API.md` — `GET /api/members/search?q=...&mode=name|phone|memberId&branchId=...` 응답 (item: id, name, phone_masked, birth_md, member_id_display; truncated: bool)
- `docs/UI_GUIDE.md` — 터치 타겟 64px, NumberPad 색상
- `frontend/ui-design/kiosk-screens-1.jsx`·`kiosk-screens-2.jsx` — InputSelect/Voice/Typing 시안
- `frontend/CLAUDE.md` — 결과 0건이면 "활성 회원권이 없거나 회원이 등록되어 있지 않습니다" 안내 후 Idle 복귀(이 처리는 step 7 MemberPick에서)
- step 3 산출물 (apiFetch, useIdleTimeout), step 5 산출물 (KioskShell)

## 작업

### 1. `src/api/members.ts` — 검색

```ts
import { apiFetch } from './client'

export interface KioskMember {
  id: number
  name: string
  phone_masked: string       // "010-****-1234"
  birth_md: string           // "**-04-15"
  member_id_display: string  // "#1234"
}

export interface SearchResponse {
  items: KioskMember[]
  truncated: boolean
}

export async function searchMembers(opts: { q: string; mode: 'name' | 'phone' | 'memberId'; branchId: number }) {
  const qs = new URLSearchParams({ q: opts.q, mode: opts.mode, branchId: String(opts.branchId) })
  return apiFetch<SearchResponse>(`/api/members/search?${qs}`, { skipAuth: true })
}
```

(키오스크 검색은 인증 없이 호출. 백엔드가 branchId 필수 + 결과 마스킹.)

### 2. `src/hooks/useSpeechRecognition.ts`

```ts
import { useEffect, useRef, useState, useCallback } from 'react'

interface Opts { lang?: string; onResult: (transcript: string) => void; onError?: (err: string) => void }

interface SpeechAPI {
  start: () => void
  stop: () => void
  available: boolean
  permissionDenied: boolean
  listening: boolean
}

export function useSpeechRecognition({ lang = 'ko-KR', onResult, onError }: Opts): SpeechAPI {
  const SR = (window as any).SpeechRecognition || (window as any).webkitSpeechRecognition
  const available = !!SR
  const [listening, setListening] = useState(false)
  const [permissionDenied, setPermissionDenied] = useState(false)
  const recRef = useRef<any>(null)

  useEffect(() => {
    if (!available) return
    const rec = new SR()
    rec.lang = lang
    rec.continuous = false
    rec.interimResults = false
    rec.onresult = (e: any) => {
      const transcript = e.results[0]?.[0]?.transcript ?? ''
      onResult(transcript)
    }
    rec.onerror = (e: any) => {
      if (e.error === 'not-allowed' || e.error === 'service-not-allowed') setPermissionDenied(true)
      onError?.(e.error)
    }
    rec.onend = () => setListening(false)
    recRef.current = rec
  }, [available, lang, onResult, onError, SR])

  const start = useCallback(() => {
    if (!recRef.current || permissionDenied) return
    setListening(true)
    try { recRef.current.start() } catch { setListening(false) }
  }, [permissionDenied])

  const stop = useCallback(() => { recRef.current?.stop() }, [])

  return { start, stop, available, permissionDenied, listening }
}
```

`available`: 마운트 시 `window.SpeechRecognition || window.webkitSpeechRecognition` 검사.
`permissionDenied`: `NotAllowedError` 또는 `service-not-allowed` 발생 시 true.

### 3. `src/pages/kiosk/InputSelect.tsx`

```tsx
import { useNavigate } from 'react-router-dom'
import { useSpeechRecognition } from '@/hooks/useSpeechRecognition'
import { useIdleTimeout } from '@/hooks/useIdleTimeout'

export default function InputSelect() {
  const navigate = useNavigate()
  const { available } = useSpeechRecognition({ onResult: () => {} })
  useIdleTimeout(10_000, () => navigate('/kiosk/idle'))

  return (
    <div className="...">
      {available && (
        <button onClick={() => navigate('/kiosk/voice')} className="btn-primary-lg">
          음성으로 찾기
        </button>
      )}
      <button onClick={() => navigate('/kiosk/typing')} className="btn-secondary-lg">
        직접 입력
      </button>
    </div>
  )
}
```

### 4. `src/pages/kiosk/VoiceSearch.tsx`

상태:
- `failCount`: 3까지 누적 → typing 폴백.
- `lastTranscript`: 들린 텍스트(시각 피드백).

처리:
- 마운트 시 `start()` 자동 호출.
- `onResult` 콜백:
  - transcript 2글자 미만 → `failCount++` + 토스트("다시 말씀해주세요").
  - 2글자 이상 → `searchMembers({ q: transcript, mode: 'name', branchId })`. 결과 0건이면 `failCount++` + 다시 듣기. 1건 이상이면 결과를 query cache에 두고 `/kiosk/pick`로 navigate(또는 결과 1건이면 step 7에서 자동 체크인까지).
- `failCount >= 3` → 토스트("음성 인식에 실패했어요. 직접 입력으로 전환합니다") + `/kiosk/typing` navigate.
- `permissionDenied` true가 되면 즉시 토스트 + `/kiosk/typing` navigate.
- `useIdleTimeout(10_000, () => navigate('/kiosk/idle'))`.

UI:
- 큰 마이크 아이콘 + listening 상태 애니메이션.
- "지금 듣고 있어요" / "다시 듣겠습니다" 안내 텍스트.
- "직접 입력으로 변경" 버튼(언제든 typing으로 전환 가능).

### 5. `src/pages/kiosk/TypingSearch.tsx` + `NumberPad`

상태:
- `mode`: `'name' | 'phone' | 'memberId'` 탭.
- `query`: 입력 값.
- 검색 결과는 mutation으로 관리 (`useMutation`).

처리:
- **이름 탭**: 텍스트 입력 + 가상 한글 키보드(태블릿에선 OS 키보드). 2글자 이상 입력 시 "검색" 버튼 활성. 제출 → 검색.
- **전화 4자리 탭**: NumberPad. 4자리 입력 즉시 자동 검색 (debounce 없이 도달 시).
- **회원 번호 탭**: NumberPad. 자릿수 제한 없음, "확인" 버튼으로 제출. 비어있으면 비활성.
- 검색 결과를 query cache에 저장 (`queryClient.setQueryData(['search', mode, query], result)`) + `/kiosk/pick`로 navigate.
- 0건이면 step 7 MemberPick에서 안내 → Idle 복귀.
- `useIdleTimeout(10_000)`.

`src/components/NumberPad.tsx`: 0~9 + ⌫(backspace) + (옵션) 확인. 큰 버튼(최소 64px). props: `value`, `onChange`, `maxLength?`, `onSubmit?`.

### 6. 컴포넌트 테스트

- `useSpeechRecognition.test.tsx`: `window.SpeechRecognition` stub해서 available true/false 케이스. NotAllowedError → permissionDenied true.
- `InputSelect.test.tsx`: available=false면 음성 버튼 미렌더, available=true면 두 버튼 모두 렌더 + 클릭 시 navigate. idle 타임아웃 동작.
- `VoiceSearch.test.tsx`: 3회 실패 → typing redirect, permissionDenied → 즉시 redirect. (실제 SR은 stub.)
- `TypingSearch.test.tsx`: 이름 탭 2글자 미만 비활성, 전화 4자리 자동 검색, 회원번호 빈값 비활성. truncated 응답 시 다음 화면에 truncated 플래그 전달 (step 7).
- `NumberPad.test.tsx`: 숫자 입력·backspace·maxLength.

## 핵심 규칙

- **2글자 이상 + branchId 필수**: 이름 검색이 1글자 prefix면 결과가 폭발 → UX·서버 부하 모두 나쁨. 백엔드도 거절(400 QUERY_TOO_SHORT).
- **전화 4자리는 자동 검색**: 4자리 도달 즉시 호출. 사용자가 "확인"을 안 눌러도 됨.
- **음성 결과는 2글자 이상 통과**: 잡음으로 들린 한 글자는 무시.
- **useSpeechRecognition은 SSR-safe**: window 접근을 effect 안에서. 빌드 시 window 미참조.
- **idle 타임아웃은 모든 검색 화면에 적용**: InputSelect, VoiceSearch, TypingSearch.
- **PII 비노출**: transcript를 console.log·에러 메시지에 남기지 마라. 검색 결과의 phone_masked·birth_md는 그대로 표시 OK(이미 마스킹됨).

## Acceptance Criteria

```bash
cd frontend
pnpm lint && pnpm build && pnpm test
```

- 모든 컴포넌트 테스트 통과.
- 시안 `kiosk-screens-1.jsx`/`kiosk-screens-2.jsx`와 시각적으로 일치.

수동 확인:
- iPad Chrome(Web Speech 미지원) 환경 시뮬레이션 — 음성 버튼이 안 보임.
- Chrome 데스크탑 — 음성 클릭 시 마이크 권한 팝업.
- 권한 거부 → 자동으로 typing 화면.
- 10초 무입력 → Idle 복귀.

## 검증 절차

1. AC.
2. step6 status 갱신.

## 금지사항

- 외부 STT SDK 도입 금지 (Web Speech API만).
- transcript·검색 query를 localStorage·console에 저장 금지.
- 검색 결과를 sessionStorage에 직렬화 금지 (query cache로만 다음 화면에 전달).
- 음성 인식 자동 재시작을 무한 루프로 만들지 마라(`failCount` 상한 3).
- 이름 검색을 1글자에서 트리거하지 마라(서버 400, 사용자 경험 모두 나쁨).
