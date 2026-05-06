// Kiosk screens — 1280×800 landscape tablet PWA fullscreen
// All screens dark (#0A0A0A), large touch targets (min 64px), 24px+ body

const KIOSK_W = 1280;
const KIOSK_H = 800;

const kStyles = {
  shell: {
    width: KIOSK_W,
    height: KIOSK_H,
    backgroundColor: 'var(--k-bg)',
    color: 'var(--k-text)',
    fontFamily: 'var(--font-kr)',
    position: 'relative',
    overflow: 'hidden',
    display: 'flex',
    flexDirection: 'column',
  },
  header: {
    height: 72,
    padding: '0 32px',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    borderBottom: '1px solid var(--k-border)',
    flexShrink: 0,
  },
  headerLeft: {
    display: 'flex',
    alignItems: 'center',
    gap: 16,
  },
  headerRight: {
    display: 'flex',
    alignItems: 'center',
    gap: 24,
    fontFamily: 'var(--font-mono)',
    fontSize: 14,
    color: 'var(--k-text-dim)',
    letterSpacing: '0.05em',
  },
  counterPill: {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 10,
    padding: '8px 14px',
    border: '1px solid var(--k-border)',
    borderRadius: 4,
    fontFamily: 'var(--font-mono)',
    fontSize: 13,
    color: 'var(--k-text-dim)',
    letterSpacing: '0.05em',
    textTransform: 'uppercase',
    whiteSpace: 'nowrap',
    flexShrink: 0,
  },
  redDot: {
    width: 8, height: 8, backgroundColor: 'var(--pb-red)', borderRadius: '50%',
  },
  bigBtn: {
    minHeight: 96,
    padding: '24px 32px',
    border: '1px solid var(--k-border-strong)',
    backgroundColor: 'var(--k-surface)',
    color: 'var(--k-text)',
    fontFamily: 'var(--font-kr)',
    fontWeight: 600,
    fontSize: 28,
    cursor: 'pointer',
    transition: 'all 0.15s',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    gap: 16,
    borderRadius: 4,
    whiteSpace: 'nowrap',
  },
  bigBtnPrimary: {
    backgroundColor: 'var(--pb-red)',
    borderColor: 'var(--pb-red)',
    color: '#fff',
  },
};

// =============== A. IDLE — energetic ===============
const KioskIdle = ({ onTap, checkInCount = 47, variant = 'energetic', live = true }) => {
  const [now, setNow] = React.useState(new Date());
  React.useEffect(() => {
    if (!live) return;
    const t = setInterval(() => setNow(new Date()), 1000);
    return () => clearInterval(t);
  }, [live]);

  const dateStr = `${now.getFullYear()}.${String(now.getMonth()+1).padStart(2,'0')}.${String(now.getDate()).padStart(2,'0')}`;
  const dayKr = ['일','월','화','수','목','금','토'][now.getDay()];
  const timeStr = `${String(now.getHours()).padStart(2,'0')}:${String(now.getMinutes()).padStart(2,'0')}`;

  return (
    <div style={kStyles.shell} onClick={onTap}>
      {/* Header */}
      <div style={kStyles.header}>
        <div style={kStyles.headerLeft}>
          <PBoyLogo size={44}/>
          <div style={{ display: 'flex', flexDirection: 'column', lineHeight: 1.1 }}>
            <div style={{ fontFamily: 'var(--font-display)', fontWeight: 700, fontSize: 16, letterSpacing: '0.04em' }}>P-BOY MMA</div>
            <div style={{ fontSize: 12, color: 'var(--k-text-dim)' }}>강남점 · GANGNAM</div>
          </div>
        </div>
        <div style={kStyles.headerRight}>
          <span>{dateStr} {dayKr}</span>
          <span style={{ width: 1, height: 16, backgroundColor: 'var(--k-border)' }}/>
          <span style={{ color: 'var(--k-text)', fontVariantNumeric: 'tabular-nums' }}>{timeStr}</span>
          <span style={kStyles.counterPill}>
            <span style={kStyles.redDot}/>
            오늘 <span style={{ color: 'var(--k-text)', fontWeight: 600, marginLeft: 4 }}>{checkInCount}</span> 명 체크인
          </span>
        </div>
      </div>

      {/* Main */}
      <div style={{ flex: 1, position: 'relative', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center' }}>
        {/* Background type — TRAIN HARD */}
        <div className="pb-glow" style={{
          position: 'absolute',
          inset: 0,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          pointerEvents: 'none',
          overflow: 'hidden',
        }}>
          <div style={{
            fontFamily: 'var(--font-display)',
            fontWeight: 700,
            fontSize: 380,
            letterSpacing: '-0.04em',
            lineHeight: 0.85,
            color: 'transparent',
            WebkitTextStroke: '1px rgba(255,255,255,0.04)',
            textAlign: 'center',
            whiteSpace: 'nowrap',
          }}>
            TRAIN<br/>HARD
          </div>
        </div>

        {/* Center content */}
        <div style={{ position: 'relative', textAlign: 'center', zIndex: 2 }}>
          <div style={{
            fontFamily: 'var(--font-display)',
            fontSize: 13,
            letterSpacing: '0.4em',
            color: 'var(--pb-red)',
            fontWeight: 600,
            marginBottom: 24,
          }}>
            ─── CHECK IN ───
          </div>
          <div style={{
            fontSize: 80,
            fontWeight: 800,
            letterSpacing: '-0.03em',
            lineHeight: 1,
            marginBottom: 12,
            whiteSpace: 'nowrap',
          }}>
            터치하여 시작
          </div>
          <div style={{
            fontFamily: 'var(--font-display)',
            fontSize: 22,
            color: 'var(--k-text-dim)',
            fontWeight: 500,
            letterSpacing: '0.02em',
          }}>
            Touch anywhere to check in
          </div>

          {/* Pulse indicator */}
          <div style={{ marginTop: 64, display: 'flex', justifyContent: 'center' }}>
            <div style={{ position: 'relative', width: 80, height: 80 }}>
              <div className="pb-pulse-ring" style={{
                position: 'absolute', inset: 0, border: '2px solid var(--pb-red)', borderRadius: '50%',
              }}/>
              <div className="pb-pulse" style={{
                position: 'absolute', inset: 12, border: '2px solid var(--pb-red)', borderRadius: '50%',
                display: 'flex', alignItems: 'center', justifyContent: 'center',
              }}>
                <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="#E10600" strokeWidth="2.5">
                  <path d="M12 2l3.09 6.26L22 9.27l-5 4.87 1.18 6.88L12 17.77l-6.18 3.25L7 14.14 2 9.27l6.91-1.01L12 2z" fill="none"/>
                </svg>
              </div>
            </div>
          </div>
        </div>

        {/* Footer ticker */}
        <div style={{
          position: 'absolute', bottom: 0, left: 0, right: 0,
          padding: '20px 32px',
          borderTop: '1px solid var(--k-border)',
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          fontFamily: 'var(--font-mono)',
          fontSize: 12,
          color: 'var(--k-text-muted)',
          letterSpacing: '0.08em',
          textTransform: 'uppercase',
        }}>
          <span>WIFI · PBOY-GUEST · PW: pboy2024</span>
          <span style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <span style={{ width: 6, height: 6, backgroundColor: '#16A34A', borderRadius: '50%' }}/>
            SYSTEM ONLINE
          </span>
        </div>
      </div>
    </div>
  );
};

// =============== B. INPUT SELECT ===============
const KioskInputSelect = ({ onVoice, onType, onBack }) => (
  <div style={kStyles.shell}>
    <KioskHeader title="체크인" subtitle="Step 1 of 2" onBack={onBack}/>
    <div style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 64 }}>
      <div style={{ width: '100%', maxWidth: 1000 }}>
        <div style={{ textAlign: 'center', marginBottom: 56 }}>
          <div style={{ fontSize: 48, fontWeight: 700, letterSpacing: '-0.02em', marginBottom: 12 }}>
            어떻게 찾을까요?
          </div>
          <div style={{ fontSize: 22, color: 'var(--k-text-dim)' }}>
            How would you like to identify yourself?
          </div>
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 24 }}>
          <button onClick={onVoice} style={{
            ...kStyles.bigBtn,
            flexDirection: 'column',
            minHeight: 320,
            gap: 24,
          }}>
            <svg width="80" height="80" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8">
              <path d="M12 2a3 3 0 0 0-3 3v7a3 3 0 0 0 6 0V5a3 3 0 0 0-3-3z"/>
              <path d="M19 10v2a7 7 0 0 1-14 0v-2M12 19v3"/>
            </svg>
            <div style={{ fontSize: 36, fontWeight: 700 }}>음성으로 찾기</div>
            <div style={{ fontSize: 18, color: 'var(--k-text-dim)', fontWeight: 400 }}>
              "홍길동"이라고 말씀해 주세요
            </div>
          </button>
          <button onClick={onType} style={{
            ...kStyles.bigBtn,
            ...kStyles.bigBtnPrimary,
            flexDirection: 'column',
            minHeight: 320,
            gap: 24,
          }}>
            <svg width="80" height="80" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8">
              <rect x="2" y="6" width="20" height="12" rx="2"/>
              <path d="M6 10h.01M10 10h.01M14 10h.01M18 10h.01M7 14h10"/>
            </svg>
            <div style={{ fontSize: 36, fontWeight: 700 }}>직접 입력하기</div>
            <div style={{ fontSize: 18, color: 'rgba(255,255,255,0.85)', fontWeight: 400 }}>
              이름 · 전화번호 · 회원번호
            </div>
          </button>
        </div>
      </div>
    </div>
  </div>
);

// =============== Header used by all non-idle screens ===============
const KioskHeader = ({ title, subtitle, onBack }) => (
  <div style={kStyles.header}>
    <div style={{ ...kStyles.headerLeft, flexShrink: 0 }}>
      {onBack && (
        <button onClick={onBack} style={{
          height: 48, padding: '0 18px',
          border: '1px solid var(--k-border-strong)',
          backgroundColor: 'transparent', color: 'var(--k-text)',
          fontSize: 16, fontWeight: 500, cursor: 'pointer', borderRadius: 4,
          fontFamily: 'var(--font-kr)', display: 'flex', alignItems: 'center', gap: 8,
          whiteSpace: 'nowrap', flexShrink: 0,
        }}>
          <span style={{ fontSize: 20 }}>←</span> 뒤로
        </button>
      )}
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexShrink: 0 }}>
        <PBoyLogo size={36}/>
        <div style={{ display: 'flex', flexDirection: 'column', lineHeight: 1.1, whiteSpace: 'nowrap' }}>
          <div style={{ fontFamily: 'var(--font-display)', fontWeight: 700, fontSize: 14, letterSpacing: '0.04em' }}>P-BOY MMA</div>
          <div style={{ fontSize: 11, color: 'var(--k-text-muted)' }}>강남점</div>
        </div>
      </div>
    </div>
    <div style={{ ...kStyles.headerRight, flexShrink: 0, whiteSpace: 'nowrap' }}>
      {subtitle && <span style={{ textTransform: 'uppercase' }}>{subtitle}</span>}
      <span style={{ color: 'var(--k-text)', fontWeight: 600, fontSize: 16, letterSpacing: 0 }}>{title}</span>
    </div>
  </div>
);

window.KioskIdle = KioskIdle;
window.KioskInputSelect = KioskInputSelect;
window.KioskHeader = KioskHeader;
window.KIOSK_W = KIOSK_W;
window.KIOSK_H = KIOSK_H;
