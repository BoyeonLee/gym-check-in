// Admin: Sales dashboard + Login + Password change

const AdminSales = ({ layout = 'standard' }) => {
  // Daily data — last 14 days
  const days = Array.from({ length: 14 }, (_, i) => ({
    day: `04.${String(15 + i).padStart(2,'0')}`,
    cash: 200000 + Math.floor(Math.sin(i * 1.3) * 150000 + 250000),
    card: 600000 + Math.floor(Math.cos(i * 0.9) * 300000 + 400000),
  }));
  const max = Math.max(...days.map(d => d.cash + d.card));

  return (
    <div style={{ ...aStyles.shell, width: ADMIN_W_DESKTOP, height: ADMIN_H_DESKTOP }}>
      <Sidebar active="sales" role="global"/>
      <div style={aStyles.main}>
        <Topbar
          title="매출"
          breadcrumb="DASHBOARD / SALES"
          action={
            <>
              <button style={aStyles.btnSecondary}>2026년 4월 ▾</button>
              <button style={aStyles.btnSecondary}>전체 지점 ▾</button>
              <button style={aStyles.btnPrimary}>
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M7 10l5 5 5-5M12 15V3"/></svg>
                Excel
              </button>
            </>
          }
        />
        <div style={aStyles.content}>
          {/* KPI cards */}
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 16, marginBottom: 20 }}>
            <KpiCard label="총 매출" value="32,840,000" suffix="원" delta="+12.4%" trend="up"/>
            <KpiCard label="환불" value="1,230,000" suffix="원" delta="-3.1%" trend="down" muted/>
            <KpiCard label="순매출" value="31,610,000" suffix="원" delta="+13.8%" trend="up" accent/>
          </div>

          <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: 16, marginBottom: 20 }}>
            {/* Chart */}
            <div style={{ ...aStyles.card, padding: 20 }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', marginBottom: 16 }}>
                <div>
                  <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: '0.1em', color: 'var(--a-text-muted)', marginBottom: 4 }}>DAILY REVENUE · LAST 14 DAYS</div>
                  <div style={{ fontSize: 16, fontWeight: 700 }}>일별 매출</div>
                </div>
                <div style={{ display: 'flex', gap: 16, fontSize: 11 }}>
                  <span style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                    <span style={{ width: 10, height: 10, backgroundColor: 'var(--a-text)', borderRadius: 2 }}/>
                    카드
                  </span>
                  <span style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                    <span style={{ width: 10, height: 10, backgroundColor: 'var(--pb-red)', borderRadius: 2 }}/>
                    현금
                  </span>
                </div>
              </div>
              {/* Bars */}
              <div style={{ height: 220, display: 'flex', alignItems: 'flex-end', gap: 6 }}>
                {days.map((d, i) => {
                  const total = d.cash + d.card;
                  const totalH = (total / max) * 200;
                  const cardH = (d.card / total) * totalH;
                  const cashH = totalH - cardH;
                  return (
                    <div key={i} style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 6 }}>
                      <div style={{ width: '100%', display: 'flex', flexDirection: 'column', justifyContent: 'flex-end', height: 200 }}>
                        <div style={{ width: '100%', height: cardH, backgroundColor: 'var(--a-text)' }}/>
                        <div style={{ width: '100%', height: cashH, backgroundColor: 'var(--pb-red)' }}/>
                      </div>
                      <div style={{ fontFamily: 'var(--font-mono)', fontSize: 9, color: 'var(--a-text-muted)', letterSpacing: '0.05em' }}>{d.day}</div>
                    </div>
                  );
                })}
              </div>
            </div>

            {/* Method split */}
            <div style={{ ...aStyles.card, padding: 20 }}>
              <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: '0.1em', color: 'var(--a-text-muted)', marginBottom: 4 }}>BY PAYMENT METHOD</div>
              <div style={{ fontSize: 16, fontWeight: 700, marginBottom: 20 }}>수단별 비중</div>

              {/* Donut-like horizontal bar */}
              <div style={{ height: 14, display: 'flex', borderRadius: 7, overflow: 'hidden', marginBottom: 16 }}>
                <div style={{ flex: 78, backgroundColor: 'var(--a-text)' }}/>
                <div style={{ flex: 22, backgroundColor: 'var(--pb-red)' }}/>
              </div>

              {[
                { label: '카드', amount: '25,615,200', pct: 78, color: 'var(--a-text)' },
                { label: '현금', amount: '7,224,800', pct: 22, color: 'var(--pb-red)' },
              ].map((r, i) => (
                <div key={i} style={{ padding: '12px 0', borderBottom: i === 0 ? '1px solid var(--a-border)' : 'none', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                    <span style={{ width: 10, height: 10, backgroundColor: r.color, borderRadius: 2 }}/>
                    <span style={{ fontWeight: 500 }}>{r.label}</span>
                  </div>
                  <div style={{ textAlign: 'right' }}>
                    <div style={{ fontFamily: 'var(--font-mono)', fontWeight: 600, fontSize: 14 }}>₩{r.amount}</div>
                    <div style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--a-text-muted)' }}>{r.pct}%</div>
                  </div>
                </div>
              ))}
            </div>
          </div>

          {/* Daily table */}
          <div style={{ ...aStyles.card, overflow: 'hidden' }}>
            <div style={{ padding: 16, borderBottom: '1px solid var(--a-border)', display: 'flex', justifyContent: 'space-between' }}>
              <div style={{ fontWeight: 700, fontSize: 14 }}>일별 상세</div>
              <button style={aStyles.btnGhost}>전체 보기 →</button>
            </div>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr style={{ backgroundColor: 'var(--a-surface-2)', borderBottom: '1px solid var(--a-border)' }}>
                  {['날짜','요일','카드','현금','환불','순매출','건수'].map(h => (
                    <th key={h} style={{ textAlign: h === '날짜' || h === '요일' ? 'left' : 'right', padding: '10px 14px', fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: '0.1em', color: 'var(--a-text-muted)', fontWeight: 600 }}>{h.toUpperCase()}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {days.slice(-6).reverse().map((d, i) => {
                  const refund = i === 1 ? 230000 : 0;
                  const net = d.card + d.cash - refund;
                  return (
                    <tr key={i} style={{ borderBottom: '1px solid var(--a-border)', height: 44 }}>
                      <td style={{ padding: '0 14px', fontFamily: 'var(--font-mono)', fontWeight: 500 }}>2026.{d.day}</td>
                      <td style={{ padding: '0 14px', color: 'var(--a-text-dim)' }}>{['월','화','수','목','금','토','일'][i]}요일</td>
                      <td style={{ padding: '0 14px', textAlign: 'right', fontFamily: 'var(--font-mono)' }}>₩{d.card.toLocaleString()}</td>
                      <td style={{ padding: '0 14px', textAlign: 'right', fontFamily: 'var(--font-mono)' }}>₩{d.cash.toLocaleString()}</td>
                      <td style={{ padding: '0 14px', textAlign: 'right', fontFamily: 'var(--font-mono)', color: refund > 0 ? 'var(--s-danger)' : 'var(--a-text-muted)' }}>{refund > 0 ? `-₩${refund.toLocaleString()}` : '—'}</td>
                      <td style={{ padding: '0 14px', textAlign: 'right', fontFamily: 'var(--font-mono)', fontWeight: 700 }}>₩{net.toLocaleString()}</td>
                      <td style={{ padding: '0 14px', textAlign: 'right', color: 'var(--a-text-dim)' }}>{4 + (i % 5)}건</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      </div>
    </div>
  );
};

const KpiCard = ({ label, value, suffix, delta, trend, accent, muted }) => (
  <div style={{
    ...aStyles.card,
    padding: 20,
    backgroundColor: accent ? 'var(--a-text)' : 'var(--a-surface)',
    color: accent ? '#fff' : 'var(--a-text)',
    borderColor: accent ? 'var(--a-text)' : 'var(--a-border)',
    position: 'relative', overflow: 'hidden',
  }}>
    <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: '0.15em', color: accent ? 'rgba(255,255,255,0.6)' : 'var(--a-text-muted)', marginBottom: 12 }}>
      {label.toUpperCase()}
    </div>
    <div style={{ display: 'flex', alignItems: 'baseline', gap: 6, marginBottom: 8 }}>
      <span style={{ fontFamily: 'var(--font-display)', fontWeight: 700, fontSize: 36, letterSpacing: '-0.02em', lineHeight: 1 }}>
        {muted ? '−' : ''}₩{value}
      </span>
    </div>
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 12 }}>
      <span style={{
        fontFamily: 'var(--font-mono)',
        padding: '2px 6px',
        backgroundColor: trend === 'up' ? (accent ? 'rgba(255,255,255,0.15)' : 'var(--s-success-bg)') : 'var(--s-danger-bg)',
        color: trend === 'up' ? (accent ? '#fff' : 'var(--s-success)') : 'var(--s-danger)',
        borderRadius: 3, fontWeight: 600,
      }}>
        {trend === 'up' ? '▲' : '▼'} {delta}
      </span>
      <span style={{ color: accent ? 'rgba(255,255,255,0.6)' : 'var(--a-text-muted)' }}>vs. 지난달</span>
    </div>
  </div>
);

// =============== LOGIN ===============
const AdminLogin = () => (
  <div style={{
    width: ADMIN_W_DESKTOP, height: ADMIN_H_DESKTOP,
    backgroundColor: 'var(--a-bg)',
    color: 'var(--a-text)',
    fontFamily: 'var(--font-kr)',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    padding: 56,
    position: 'relative',
    overflow: 'hidden',
  }}>
    {/* Big background wordmark — visible on dark, near-invisible on light */}
    <div className="pb-glow" style={{
      position: 'absolute', inset: 0, display: 'flex',
      alignItems: 'center', justifyContent: 'center',
      pointerEvents: 'none', overflow: 'hidden',
    }}>
      <div style={{
        fontFamily: 'var(--font-display)', fontWeight: 700, fontSize: 320,
        letterSpacing: '-0.04em', lineHeight: 0.85,
        color: 'transparent',
        WebkitTextStroke: '1px color-mix(in srgb, var(--a-text) 5%, transparent)',
        textAlign: 'center', whiteSpace: 'nowrap',
      }}>
        P-BOY<br/>MMA
      </div>
    </div>

    <div style={{
      width: '100%', maxWidth: 420,
      display: 'flex', flexDirection: 'column', alignItems: 'center',
      position: 'relative', zIndex: 2,
    }}>
      {/* Logo */}
      <div style={{ marginBottom: 28 }}>
        <PBoyLogo size={88}/>
      </div>

      {/* Title */}
      <div style={{
        fontFamily: 'var(--font-display)', fontSize: 12,
        letterSpacing: '0.4em', fontWeight: 600,
        color: 'var(--pb-red)', marginBottom: 10,
      }}>
        P-BOY MMA ADMIN
      </div>
      <div style={{
        fontSize: 26, fontWeight: 800, letterSpacing: '-0.02em',
        marginBottom: 32,
      }}>
        관리자 로그인
      </div>

      {/* Login card */}
      <div style={{
        width: '100%',
        backgroundColor: 'var(--a-surface)',
        border: '1px solid var(--a-border)',
        borderRadius: 8,
        padding: '32px 32px 28px',
      }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          <FormField label="아이디" value="hong.kildong"/>
          <div>
            <div style={{ fontSize: 11, color: 'var(--a-text-muted)', fontFamily: 'var(--font-mono)', letterSpacing: '0.1em', marginBottom: 6, display: 'flex', justifyContent: 'space-between' }}>
              <span>비밀번호</span>
              <a style={{ color: 'var(--pb-red)', textDecoration: 'none', fontFamily: 'var(--font-kr)', letterSpacing: 0, cursor: 'pointer' }}>비밀번호 찾기</a>
            </div>
            <div style={{
              height: 40, padding: '0 12px',
              border: '1px solid var(--a-border-strong)',
              borderRadius: 4,
              backgroundColor: 'var(--a-surface)',
              color: 'var(--a-text)',
              display: 'flex', alignItems: 'center',
              fontFamily: 'var(--font-mono)', fontSize: 14, fontWeight: 500, letterSpacing: '0.2em',
            }}>
              ••••••••••
            </div>
          </div>
          <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--a-text-dim)' }}>
            <span style={{ width: 16, height: 16, border: '1.5px solid var(--a-border-strong)', borderRadius: 3, backgroundColor: 'var(--pb-red)', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
              <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="#fff" strokeWidth="3"><path d="M5 13l4 4L19 7"/></svg>
            </span>
            로그인 상태 유지
          </label>
          <button style={{ ...aStyles.btnPrimary, padding: '14px', fontSize: 15, justifyContent: 'center', marginTop: 8 }}>
            로그인
          </button>
        </div>
      </div>
    </div>
  </div>
);

window.AdminSales = AdminSales;
window.AdminLogin = AdminLogin;
window.KpiCard = KpiCard;
