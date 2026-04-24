# UI 디자인 가이드

## 디자인 원칙
1. **도구처럼 보일 것.** 마케팅 페이지가 아니라 매일 쓰는 대시보드·키오스크다.
2. **키오스크는 1초 만에 이해되는 큰 버튼.** 복잡한 메뉴 없이, 어느 고령 회원도 헤매지 않게.
3. **관리자는 정보 밀도 우선.** 테이블·폼 중심. 장식 최소화, 반응형으로 모바일에서도 읽히게.

## AI 슬롭 안티패턴 — 하지 마라
| 금지 사항 | 이유 |
|-----------|------|
| backdrop-filter: blur() | glass morphism은 AI 템플릿의 가장 흔한 징후 |
| gradient-text (배경 그라데이션 텍스트) | AI가 만든 SaaS 랜딩의 1번 특징 |
| "Powered by AI" 배지 | 기능이 아니라 장식. 사용자에게 가치 없음 |
| box-shadow 글로우 애니메이션 | 네온 글로우 = AI 슬롭 |
| 보라/인디고 브랜드 색상 | "AI = 보라색" 클리셰 |
| 모든 카드에 동일한 rounded-2xl | 균일한 둥근 모서리는 템플릿 느낌 |
| 배경 gradient orb (blur-3xl 원형) | 모든 AI 랜딩 페이지에 있는 장식 |

## 색상
### 배경
| 용도 | 값 |
|------|------|
| 페이지 | `#0a0a0a` |
| 카드 | `#141414` |
| 입력 | `#1a1a1a` |

### 텍스트
| 용도 | 값 |
|------|------|
| 주 텍스트 | `text-white` |
| 본문 | `text-neutral-300` |
| 보조 | `text-neutral-400` |
| 비활성 | `text-neutral-500` |

### 데이터/시맨틱 색상
| 용도 | 값 |
|------|------|
| 성공 / 체크인 완료 | `#22c55e` |
| 경고 / 만료 임박 | `#f59e0b` |
| 에러 / 만료 · 잔여 0 | `#ef4444` |
| 중립 / 기본 | `#525252` |

포인트는 녹색(체크인 성공 흐름)을 기준으로 사용한다. 보라·인디고 계열은 금지.

## 컴포넌트
### 카드
```
rounded-lg bg-[#141414] border border-neutral-800 p-6
```

### 버튼
```
Primary:       rounded-lg bg-white text-black hover:bg-neutral-200 font-medium
Success:       rounded-lg bg-[#22c55e] text-black hover:bg-[#16a34a]
Danger:        rounded-lg bg-[#ef4444] text-white hover:bg-[#dc2626]
Text:          text-neutral-500 hover:text-neutral-300
KioskPrimary:  rounded-xl bg-white text-black text-2xl py-6 px-8 font-semibold
               (키오스크 전용 — 최소 터치 타겟 64px 이상)
```

### 입력 필드
```
기본:   rounded-lg bg-[#1a1a1a] border border-neutral-800 px-4 py-3 text-white
키오스크: rounded-xl bg-[#1a1a1a] border border-neutral-700 px-6 py-5 text-3xl
```

## 레이아웃
- **관리자 데스크톱**: `max-w-6xl`, 좌측 정렬, 사이드 네비게이션 + 메인.
- **관리자 모바일(반응형)**: 상단 탑바 + 스택 레이아웃. 테이블은 카드형으로 재배치.
- **키오스크 태블릿**: `h-screen w-screen`, 중앙 정렬 콘텐츠, 여백 넉넉히(`p-8~12`).
- 간격: 기본 `gap-3~4`, 섹션 간 `space-y-8`. 키오스크는 `gap-6` 이상.

## 타이포그래피
| 용도 | 스타일 |
|------|--------|
| 페이지 제목(관리자) | `text-3xl font-semibold text-white` |
| 섹션 제목 | `text-sm font-medium uppercase tracking-wide text-neutral-400` |
| 본문 | `text-sm text-neutral-300 leading-relaxed` |
| 테이블 셀 | `text-sm text-neutral-200` |
| 키오스크 헤드라인 | `text-5xl font-bold text-white` |
| 키오스크 본문 | `text-2xl text-neutral-200` |

## 애니메이션
- 허용: `fade-in`(0.2~0.3s), `slide-up`(0.25s), 버튼 `hover` 색상 트랜지션(150ms).
- 그 외 모든 장식적 애니메이션(글로우·플로트·펄스 등) 금지.
- 키오스크 체크인 완료 화면은 큰 체크 아이콘 페이드인 + 2~3초 후 자동 복귀.

## 아이콘
- Lucide React 아이콘 사용. `strokeWidth={1.75}`.
- 아이콘 컨테이너(둥근 배경 박스)로 감싸지 않는다.
- 키오스크 아이콘은 `w-16 h-16` 이상으로 확대.
