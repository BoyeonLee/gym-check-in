// Kiosk screens part 2 — VoiceSearch, TypingSearch, MemberPick, Done

const ks2 = {
  shell: {
    width: KIOSK_W, height: KIOSK_H,
    backgroundColor: 'var(--k-bg)', color: 'var(--k-text)',
    fontFamily: 'var(--font-kr)',
    position: 'relative', overflow: 'hidden',
    display: 'flex', flexDirection: 'column',
  },
  tab: {
    flex: 1, height: 64,
    border: '1px solid var(--k-border-strong)',
    backgroundColor: 'transparent', color: 'var(--k-text-dim)',
    fontFamily: 'var(--font-kr)', fontWeight: 500, fontSize: 18,
    cursor: 'pointer', borderRadius: 4,
    transition: 'all 0.15s',
  },
  tabActive: {
    backgroundColor: 'var(--k-text)', color: 'var(--k-bg)',
    borderColor: 'var(--k-text)', fontWeight: 700,
  },
  numKey: {
    height: 88,
    border: '1px solid var(--k-border-strong)',
    backgroundColor: 'var(--k-surface)', color: 'var(--k-text)',
    fontFamily: 'var(--font-display)', fontWeight: 600, fontSize: 32,
    cursor: 'pointer', borderRadius: 4,
    transition: 'all 0.1s',
  },
};

// =============== C. VOICE SEARCH (listening) ===============
const KioskVoiceSearch = ({ state = 'listening', attempt = 1, onSwitchType, onBack, transcript = '' }) => {
  // state: 'listening' | 'failed' | 'success'
  const isListening = state === 'listening';
  const isFailed = state === 'failed';
  return (
    <div style={ks2.shell}>
      <KioskHeader title="음성 검색" subtitle={`Step 2 · attempt ${attempt}/3`} onBack={onBack}/>
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', padding: 64, position: 'relative' }}>

        {/* Visualizer */}
        <div style={{ position: 'relative', width: 280, height: 280, display: 'flex', alignItems: 'center', justifyContent: 'center', marginBottom: 48 }}>
          {isListening && (
            <>
              <div className="pb-pulse-ring" style={{ position: 'absolute', inset: 0, border: '2px solid var(--pb-red)', borderRadius: '50%' }}/>
              <div className="pb-pulse-ring" style={{ position: 'absolute', inset: 0, border: '2px solid var(--pb-red)', borderRadius: '50%', animationDelay: '0.6s' }}/>
            </>
          )}
          <div style={{
            width: 200, height: 200,
            borderRadius: '50%',
            backgroundColor: isFailed ? 'var(--k-surface)' : 'var(--pb-red)',
            border: isFailed ? '2px solid var(--s-warning)' : 'none',
            display: 'flex', alignItems: 'center', justifyContent: 'center',
            transition: 'all 0.3s',
          }}>
            {isFailed ? (
              <svg width="80" height="80" viewBox="0 0 24 24" fill="none" stroke="#D97706" strokeWidth="2">
                <path d="M12 9v4M12 17h.01"/>
                <circle cx="12" cy="12" r="10"/>
              </svg>
            ) : (
              <svg width="80" height="80" viewBox="0 0 24 24" fill="none" stroke="#fff" strokeWidth="2">
                <path d="M12 2a3 3 0 0 0-3 3v7a3 3 0 0 0 6 0V5a3 3 0 0 0-3-3z"/>
                <path d="M19 10v2a7 7 0 0 1-14 0v-2M12 19v3"/>
              </svg>
            )}
          </div>
        </div>

        {/* Waveform */}
        {isListening && (
          <div style={{ display: 'flex', alignItems: 'center', gap: 4, marginBottom: 32, height: 48 }}>
            {[...Array(24)].map((_, i) => (
              <div key={i} style={{
                width: 4,
                height: `${20 + Math.abs(Math.sin((i * 137) % 360) * 60)}%`,
                backgroundColor: 'var(--pb-red)',
                animation: `pb-pulse ${0.6 + (i % 5) * 0.15}s ease-in-out infinite`,
                animationDelay: `${i * 0.04}s`,
                borderRadius: 2,
              }}/>
            ))}
          </div>
        )}

        <div style={{ textAlign: 'center', marginBottom: 12 }}>
          <div style={{ fontSize: 44, fontWeight: 700, letterSpacing: '-0.02em', marginBottom: 12 }}>
            {isFailed ? '잘 들리지 않았어요' : '듣고 있어요'}
          </div>
          <div style={{ fontSize: 22, color: 'var(--k-text-dim)' }}>
            {isFailed
              ? `다시 시도하시거나 직접 입력해 주세요 (${attempt}/3)`
              : '회원님의 이름을 말씀해 주세요'}
          </div>
        </div>

        {transcript && (
          <div style={{
            marginTop: 24, padding: '16px 32px',
            backgroundColor: 'var(--k-surface)', border: '1px solid var(--k-border)',
            fontSize: 24, fontWeight: 500, borderRadius: 4,
            fontFamily: 'var(--font-mono)',
          }}>
            "{transcript}"
          </div>
        )}

        {/* Switch to typing */}
        <button onClick={onSwitchType} style={{
          position: 'absolute', bottom: 48, right: 48,
          height: 64, padding: '0 32px',
          border: isFailed ? '1px solid var(--pb-red)' : '1px solid var(--k-border-strong)',
          backgroundColor: isFailed ? 'var(--pb-red)' : 'transparent',
          color: 'var(--k-text)',
          fontFamily: 'var(--font-kr)', fontWeight: 600, fontSize: 18,
          cursor: 'pointer', borderRadius: 4,
          display: 'flex', alignItems: 'center', gap: 12,
        }}>
          직접 입력하기 <span>→</span>
        </button>
      </div>
    </div>
  );
};

// =============== D. TYPING SEARCH ===============
const KioskTypingSearch = ({ tab = 'phone', value = '', onTab, onKey, onSubmit, onBack, error }) => {
  const placeholder = {
    name: '예: 홍길동',
    phone: '전화번호 뒤 4자리',
    id: '회원번호 (4자리)',
  }[tab];

  // numpad keys for phone & id
  const numKeys = ['1','2','3','4','5','6','7','8','9','','0','⌫'];

  // hangul keys for name (2-row layout, simplified)
  const hangul1 = ['ㅂ','ㅈ','ㄷ','ㄱ','ㅅ','ㅛ','ㅕ','ㅑ','ㅐ','ㅔ'];
  const hangul2 = ['ㅁ','ㄴ','ㅇ','ㄹ','ㅎ','ㅗ','ㅓ','ㅏ','ㅣ'];
  const hangul3 = ['⇧','ㅋ','ㅌ','ㅊ','ㅍ','ㅠ','ㅜ','ㅡ','⌫'];

  return (
    <div style={ks2.shell}>
      <KioskHeader title="직접 입력" subtitle="Step 2" onBack={onBack}/>
      <div style={{ flex: 1, padding: '32px 64px', display: 'flex', flexDirection: 'column', gap: 24 }}>

        {/* Tabs */}
        <div style={{ display: 'flex', gap: 12 }}>
          {[
            { id: 'name', label: '이름' },
            { id: 'phone', label: '전화 뒷 4자리' },
            { id: 'id', label: '회원번호' },
          ].map(t => (
            <button key={t.id} onClick={() => onTab && onTab(t.id)}
              style={{ ...ks2.tab, ...(tab === t.id ? ks2.tabActive : {}) }}>
              {t.label}
            </button>
          ))}
        </div>

        {/* Input display */}
        <div style={{
          height: 120,
          border: error ? '2px solid var(--s-danger)' : '2px solid var(--k-border-strong)',
          backgroundColor: 'var(--k-surface)',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          fontSize: 56, fontWeight: 700, letterSpacing: '0.05em',
          fontFamily: tab === 'name' ? 'var(--font-kr)' : 'var(--font-mono)',
          borderRadius: 4,
          color: value ? 'var(--k-text)' : 'var(--k-text-muted)',
        }}>
          {value || placeholder}
        </div>

        {error && (
          <div style={{ color: 'var(--s-danger)', fontSize: 16, marginTop: -16, display: 'flex', alignItems: 'center', gap: 8 }}>
            <span>⚠</span> {error}
          </div>
        )}

        {/* Keypad */}
        <div style={{ flex: 1, display: 'flex', gap: 16 }}>
          {tab === 'name' ? (
            <div style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 8 }}>
              {[hangul1, hangul2, hangul3].map((row, i) => (
                <div key={i} style={{ display: 'grid', gridTemplateColumns: `repeat(${row.length}, 1fr)`, gap: 8 }}>
                  {row.map((k, j) => (
                    <button key={j} onClick={() => onKey && onKey(k)}
                      style={{ ...ks2.numKey, fontSize: 26, height: 76 }}>
                      {k}
                    </button>
                  ))}
                </div>
              ))}
            </div>
          ) : (
            <div style={{ flex: 1, display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 12 }}>
              {numKeys.map((k, i) => (
                <button key={i} onClick={() => k && onKey && onKey(k)}
                  style={{
                    ...ks2.numKey,
                    visibility: k ? 'visible' : 'hidden',
                    fontSize: k === '⌫' ? 28 : 36,
                  }}>
                  {k}
                </button>
              ))}
            </div>
          )}

          {/* Right column: submit */}
          <div style={{ width: 280, display: 'flex', flexDirection: 'column', gap: 12 }}>
            <button onClick={onSubmit} disabled={!value} style={{
              flex: 1,
              backgroundColor: value ? 'var(--pb-red)' : 'var(--k-surface)',
              border: value ? '1px solid var(--pb-red)' : '1px solid var(--k-border)',
              color: value ? '#fff' : 'var(--k-text-muted)',
              fontFamily: 'var(--font-kr)', fontSize: 32, fontWeight: 700,
              cursor: value ? 'pointer' : 'not-allowed',
              borderRadius: 4,
              display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 8,
            }}>
              <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2">
                <path d="M5 12h14M13 6l6 6-6 6"/>
              </svg>
              검색
            </button>
            <button style={{
              height: 76,
              backgroundColor: 'transparent',
              border: '1px solid var(--k-border-strong)',
              color: 'var(--k-text-dim)',
              fontFamily: 'var(--font-kr)', fontSize: 18, fontWeight: 500,
              cursor: 'pointer', borderRadius: 4,
            }}>
              한/영
            </button>
          </div>
        </div>
      </div>
    </div>
  );
};

// =============== E. MEMBER PICK ===============
// Mask helpers
const maskName = (name) => {
  if (!name || name.length < 2) return name;
  if (name.length === 2) return name[0] + '○';
  return name[0] + '○'.repeat(name.length - 2) + name[name.length - 1];
};
const maskPhone = (p) => {
  // p like "010-1234-5678" → "010-****-5678"
  const parts = p.split('-');
  if (parts.length !== 3) return p;
  return `${parts[0]}-****-${parts[2]}`;
};
const maskBirth = (b) => {
  // "1990-04-15" → "**-04-15"
  const parts = b.split('-');
  if (parts.length !== 3) return b;
  return `**-${parts[1]}-${parts[2]}`;
};

const KioskMemberPick = ({ candidates = [], query = '홍길동', onPick, onBack }) => (
  <div style={ks2.shell}>
    <KioskHeader title="회원 선택" subtitle="Step 3" onBack={onBack}/>
    <div style={{ flex: 1, padding: '32px 64px', display: 'flex', flexDirection: 'column' }}>
      <div style={{ marginBottom: 24 }}>
        <div style={{ fontSize: 36, fontWeight: 700, letterSpacing: '-0.02em', marginBottom: 8 }}>
          본인을 선택해 주세요
        </div>
        <div style={{ fontSize: 18, color: 'var(--k-text-dim)' }}>
          "<span style={{ color: 'var(--pb-red)' }}>{query}</span>" 검색 결과 · {candidates.length}명
        </div>
      </div>

      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 12, overflow: 'auto' }} className="k-scroll">
        {candidates.map((m, i) => (
          <button key={m.id || i}
            onClick={() => onPick && onPick(m)}
            style={{
              minHeight: 120,
              padding: '20px 28px',
              backgroundColor: 'var(--k-surface)',
              border: '1px solid var(--k-border-strong)',
              color: 'var(--k-text)',
              cursor: 'pointer',
              borderRadius: 4,
              display: 'flex', alignItems: 'center', gap: 24,
              textAlign: 'left',
              fontFamily: 'var(--font-kr)',
              transition: 'all 0.15s',
            }}>
            {/* Member number — large */}
            <div style={{
              minWidth: 100,
              fontFamily: 'var(--font-mono)',
              fontWeight: 700,
              fontSize: 28,
              color: 'var(--pb-red)',
              letterSpacing: '0.02em',
            }}>
              #{String(m.memberNo).padStart(4, '0')}
            </div>
            <div style={{ width: 1, height: 64, backgroundColor: 'var(--k-border)' }}/>
            {/* Name — large */}
            <div style={{ flex: 1 }}>
              <div style={{ fontSize: 36, fontWeight: 700, letterSpacing: '-0.01em', marginBottom: 6 }}>
                {maskName(m.name)}
              </div>
              <div style={{
                display: 'flex', gap: 16,
                fontSize: 14, color: 'var(--k-text-dim)',
                fontFamily: 'var(--font-mono)',
                letterSpacing: '0.02em',
              }}>
                <span>{maskPhone(m.phone)}</span>
                <span>·</span>
                <span>{maskBirth(m.birth)}</span>
                <span>·</span>
                <span>{m.plan}</span>
              </div>
            </div>
            {/* Arrow */}
            <div style={{
              width: 56, height: 56,
              border: '1px solid var(--k-border-strong)',
              borderRadius: 4,
              display: 'flex', alignItems: 'center', justifyContent: 'center',
            }}>
              <span style={{ fontSize: 24 }}>→</span>
            </div>
          </button>
        ))}
      </div>

      <div style={{ marginTop: 16, padding: 16, fontSize: 14, color: 'var(--k-text-muted)', textAlign: 'center', fontFamily: 'var(--font-mono)', letterSpacing: '0.05em' }}>
        본인이 없으신가요? 카운터 직원에게 문의해 주세요
      </div>
    </div>
  </div>
);

// =============== F. CHECK IN DONE ===============
const KioskDone = ({ name = '홍길동', memberNo = 1234, plan = '월권 3개월', daysLeft = 47, todayCount = 48, onAuto }) => {
  const [count, setCount] = React.useState(3);
  React.useEffect(() => {
    if (count <= 0) { onAuto && onAuto(); return; }
    const t = setTimeout(() => setCount(count - 1), 1000);
    return () => clearTimeout(t);
  }, [count]);

  return (
    <div style={ks2.shell}>
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', padding: 64, position: 'relative' }}>

        {/* Big check */}
        <div style={{ position: 'relative', marginBottom: 48 }}>
          <div className="pb-pulse-ring" style={{
            position: 'absolute', inset: 0, border: '2px solid var(--pb-red)', borderRadius: '50%',
          }}/>
          <div style={{
            width: 200, height: 200,
            borderRadius: '50%',
            backgroundColor: 'var(--pb-red)',
            display: 'flex', alignItems: 'center', justifyContent: 'center',
          }}>
            <svg width="100" height="100" viewBox="0 0 24 24" fill="none" stroke="#fff" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round">
              <path d="M5 13l4 4L19 7"/>
            </svg>
          </div>
        </div>

        {/* Welcome */}
        <div style={{ textAlign: 'center', marginBottom: 48 }}>
          <div style={{
            fontFamily: 'var(--font-display)',
            fontSize: 14, letterSpacing: '0.4em',
            color: 'var(--pb-red)', fontWeight: 600,
            marginBottom: 16,
          }}>
            ✓ CHECKED IN · {String(new Date().getHours()).padStart(2,'0')}:{String(new Date().getMinutes()).padStart(2,'0')}
          </div>
          <div style={{ fontSize: 72, fontWeight: 800, letterSpacing: '-0.03em', lineHeight: 1, marginBottom: 16 }}>
            환영합니다,<br/>{name}님
          </div>
          <div style={{ fontSize: 22, color: 'var(--k-text-dim)' }}>
            Welcome back. Train hard.
          </div>
        </div>

        {/* Info row */}
        <div style={{
          display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 0,
          width: '100%', maxWidth: 720,
          border: '1px solid var(--k-border)',
          borderRadius: 4,
          backgroundColor: 'var(--k-surface)',
        }}>
          {[
            { label: 'MEMBER', value: `#${String(memberNo).padStart(4,'0')}`, mono: true },
            { label: 'PLAN', value: plan },
            { label: 'DAYS LEFT', value: `${daysLeft}일`, accent: daysLeft < 7 },
          ].map((c, i) => (
            <div key={i} style={{
              padding: '20px 24px',
              borderRight: i < 2 ? '1px solid var(--k-border)' : 'none',
              textAlign: 'center',
            }}>
              <div style={{ fontFamily: 'var(--font-mono)', fontSize: 11, letterSpacing: '0.15em', color: 'var(--k-text-muted)', marginBottom: 8 }}>
                {c.label}
              </div>
              <div style={{
                fontFamily: c.mono ? 'var(--font-mono)' : 'var(--font-display)',
                fontWeight: 700, fontSize: 28,
                color: c.accent ? 'var(--pb-red)' : 'var(--k-text)',
              }}>
                {c.value}
              </div>
            </div>
          ))}
        </div>

        {/* Today counter */}
        <div style={{
          position: 'absolute', top: 32, right: 32,
          padding: '8px 14px',
          border: '1px solid var(--k-border)',
          fontFamily: 'var(--font-mono)', fontSize: 13,
          color: 'var(--k-text-dim)',
          letterSpacing: '0.05em', textTransform: 'uppercase',
          borderRadius: 4,
        }}>
          오늘 <span style={{ color: 'var(--k-text)', fontWeight: 600 }}>{todayCount}</span> 명째
        </div>

        {/* Countdown */}
        <div style={{
          position: 'absolute', bottom: 48,
          fontFamily: 'var(--font-mono)', fontSize: 13,
          color: 'var(--k-text-muted)',
          letterSpacing: '0.1em', textTransform: 'uppercase',
        }}>
          {count}초 후 자동으로 돌아갑니다
        </div>
      </div>
    </div>
  );
};

window.KioskVoiceSearch = KioskVoiceSearch;
window.KioskTypingSearch = KioskTypingSearch;
window.KioskMemberPick = KioskMemberPick;
window.KioskDone = KioskDone;
window.maskName = maskName;
window.maskPhone = maskPhone;
window.maskBirth = maskBirth;
