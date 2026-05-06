# UI 디자인 가이드 — P-BOY MMA Check-in

이 문서는 **원칙**과 **하지 말 것**, **구현 계약**만 다룬다.
토큰 값·컴포넌트 픽셀 사양·화면 레이아웃은 `frontend/ui-design/`이 단일 원천이다.

## 단일 원천 (Source of Truth)
| 무엇 | 어디 |
|------|------|
| 색상 토큰(브랜드/키오스크/관리자/시맨틱), 폰트, 키프레임 | `frontend/ui-design/styles.css` |
| 화면 픽셀 사양·인터랙션·레이아웃 | `frontend/ui-design/*.jsx` (`kiosk-screens-1.jsx`, `kiosk-screens-2.jsx`, `admin-shell.jsx`, `admin-members.jsx`, `admin-plan-grant.jsx`, `admin-sales-login.jsx`) |
| 화면 목록·플로우·테마 반전 정책 | `frontend/ui-design/README.md` |
| 인터랙티브 검토 | `frontend/ui-design/kiosk-prototype.html`, `P-BOY MMA Check-in Design.html` |

`docs/`는 위 자산을 다시 적지 않는다. 같은 사실을 두 곳에 두면 drift만 생긴다.

## 디자인 원칙
1. **도구처럼 보일 것.** 매장 키오스크 + 운영 대시보드. 마케팅 페이지가 아니다.
2. **키오스크는 1초 만에 이해되는 큰 버튼.** 24px 이상 본문, 64px 이상 터치 타겟. 어느 고령 회원도 헤매지 않게.
3. **관리자는 정보 밀도 우선.** 테이블·폼 중심. 장식 최소화, 모바일에서는 카드 스택으로 재배치.
4. **각진 디자인.** 모서리는 기본 4px, 카드 6–8px. 균일한 둥근 모서리 금지.
5. **빨강은 절제해서 사용.** P-BOY 브랜드 빨강은 CTA·체크인 성공·체크인 카운터·로고 액센트에만. 본문 컬러로 쓰지 않는다.
6. **다크/라이트는 같은 컴포넌트, 토큰 swap.** 키오스크는 다크 기본, 관리자는 라이트 기본. 컴포넌트 포크 금지(`.theme-kiosk-light`, `.theme-admin-dark` 클래스로만 반전).
7. **체크인 성공 색은 빨강.** 일반적인 "성공=녹색" 컨벤션을 의도적으로 어긴다 — 체크인은 브랜드 모먼트라 빨강, `--s-success`(녹색)는 회원권 active 배지 같은 *상태* 표시에만.

## AI 슬롭 안티패턴 — 하지 마라
| 금지 | 이유 |
|-----------|------|
| `backdrop-filter: blur()` | glass morphism은 AI 템플릿의 가장 흔한 징후 |
| 그라데이션 텍스트 | AI가 만든 SaaS 랜딩의 1번 특징 |
| "Powered by AI" 배지 | 기능이 아니라 장식. 사용자에게 가치 없음 |
| `box-shadow` 글로우 애니메이션 | 네온 글로우 = AI 슬롭. 펄스(`pb-pulse-ring`)는 음성/완료 표시용으로만 허용 |
| 보라·인디고 액센트 | "AI = 보라색" 클리셰. 본 프로젝트는 **빨강** 단일 액센트 |
| 모든 카드에 동일한 `rounded-2xl`/`rounded-3xl` | 균일한 둥근 모서리는 템플릿 느낌. 4–8px만 |
| 배경 gradient orb (`blur-3xl` 원형) | 모든 AI 랜딩 페이지에 있는 장식 |
| 의미 없는 이모지 아이콘 | Lucide 아이콘만 사용. 결제 수단(💳/💵) 같은 의미 있는 한정 사용은 OK |
| `styles.css`에 없는 새 keyframe 추가 | 허용 애니메이션은 `pb-pulse` / `pb-pulse-ring` / `pb-glow` 셋뿐 |

## 구현 계약 (jsx 프로토타입 → React + Tailwind)
`frontend/ui-design/`는 **레퍼런스**다. 운영 코드는 React + TypeScript + Tailwind로 다시 짜되, 다음 계약을 지킨다.

1. **토큰은 CSS 변수 그대로.** `styles.css`의 `--pb-*`, `--k-*`, `--a-*`, `--s-*`, `--font-*`를 `src/styles/tokens.css`에 복사. Tailwind `theme.colors`/`fontFamily`는 이 변수를 참조(`colors: { pb: { red: 'var(--pb-red)' }, k: { bg: 'var(--k-bg)', ... } }`). 픽셀 값을 Tailwind 색에 직접 박지 않는다.
2. **테마 반전은 클래스 토글.** `.theme-kiosk-light`, `.theme-admin-dark`는 동일 컴포넌트 위에 wrapper로 씌운다. `dark:` variant 사용 금지(우리는 두 테마 시스템이 비대칭이라 Tailwind dark 전략과 맞지 않는다).
3. **픽셀·간격·radius는 4의 배수.** Tailwind 기본 spacing scale을 따르되 jsx에서 본 값(`padding: 64`, `height: 96`)은 그대로 옮긴다. 임의 반올림 금지.
4. **폰트 매핑.** `font-kr` = Pretendard(jsDelivr CDN), `font-display` = Space Grotesk, `font-mono` = JetBrains Mono. `index.html`에서 `<link>` 로드 후 Tailwind `fontFamily`에 매핑.
5. **라벨 컨벤션 고정.** 메타·섹션 라벨은 항상 `font-mono` + `text-[10–11px]` + `tracking-[0.1em]` + `uppercase` + muted 컬러. 이 패턴을 흩뜨리지 않는다.
6. **회원번호 포맷.** 항상 `#` prefix + 4자리 zero-pad + `font-mono` + `text-[--pb-red]` (예: `#0894`). 서버 응답의 `member_id_display`를 그대로 쓰고 클라에서 다시 포맷하지 않는다.
7. **풀 PII 마스킹.** 키오스크 `MemberPick`은 이름 `홍○동`, 전화 `010-****-5678`, 생일 `**-04-15`만 노출. 마스킹 헬퍼는 `kiosk-screens-2.jsx` 참고. 관리자 화면은 풀 PII(`frontend/CLAUDE.md` 참조).
8. **터치 타겟 최소치.** 키오스크 일반 64px / 메가 96px / 키패드 76–88px, 관리자 32–40px, 모바일 44px 절대.
9. **금지 keyframe 추가 안 함.** 새 애니메이션이 필요하면 `styles.css`를 직접 수정하고 이 문서의 안티패턴 표를 다시 검토한다.

## 변경 절차
- 토큰·컴포넌트 사양을 바꿔야 하면 **`frontend/ui-design/` 먼저 수정** → 운영 코드(`frontend/src/`)에 반영. 이 문서는 *원칙*이 바뀔 때만 수정한다.
- 새 화면이 필요하면 `ui-design/`에 jsx 프로토타입을 먼저 추가해 픽셀 단위로 합의한 뒤 React로 옮긴다.
