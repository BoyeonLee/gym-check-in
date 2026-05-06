// Admin app — light theme, dashboard with sidebar + header

const ADMIN_W_DESKTOP = 1440;
const ADMIN_H_DESKTOP = 900;
const ADMIN_W_MOBILE = 390;
const ADMIN_H_MOBILE = 844;

const aStyles = {
  shell: {
    backgroundColor: 'var(--a-bg)', color: 'var(--a-text)',
    fontFamily: 'var(--font-kr)',
    fontSize: 14,
    display: 'flex',
    overflow: 'hidden',
  },
  sidebar: {
    width: 240,
    backgroundColor: 'var(--a-sidebar)',
    color: 'var(--a-sidebar-text)',
    padding: '20px 14px',
    display: 'flex', flexDirection: 'column', gap: 4,
    flexShrink: 0,
  },
  navItem: {
    display: 'flex', alignItems: 'center', gap: 10,
    padding: '10px 12px',
    borderRadius: 4,
    fontSize: 14, fontWeight: 500,
    color: 'var(--a-sidebar-text-dim)',
    cursor: 'pointer',
  },
  navItemActive: {
    backgroundColor: 'rgba(255,255,255,0.06)',
    color: 'var(--a-sidebar-text)',
    fontWeight: 600,
  },
  main: {
    flex: 1,
    display: 'flex', flexDirection: 'column',
    overflow: 'hidden',
  },
  topbar: {
    height: 64,
    padding: '0 28px',
    backgroundColor: 'var(--a-surface)',
    borderBottom: '1px solid var(--a-border)',
    display: 'flex', alignItems: 'center', justifyContent: 'space-between',
    flexShrink: 0,
  },
  content: {
    flex: 1,
    padding: 28,
    overflow: 'auto',
    backgroundColor: 'var(--a-bg)',
  },
  card: {
    backgroundColor: 'var(--a-surface)',
    border: '1px solid var(--a-border)',
    borderRadius: 6,
  },
  btnPrimary: {
    backgroundColor: 'var(--pb-red)',
    color: '#fff',
    border: 'none',
    padding: '8px 16px',
    fontSize: 13,
    fontWeight: 600,
    borderRadius: 4,
    cursor: 'pointer',
    fontFamily: 'var(--font-kr)',
    display: 'inline-flex', alignItems: 'center', gap: 6,
  },
  btnSecondary: {
    backgroundColor: 'var(--a-surface)',
    color: 'var(--a-text)',
    border: '1px solid var(--a-border-strong)',
    padding: '8px 14px',
    fontSize: 13,
    fontWeight: 500,
    borderRadius: 4,
    cursor: 'pointer',
    fontFamily: 'var(--font-kr)',
    display: 'inline-flex', alignItems: 'center', gap: 6,
  },
  btnGhost: {
    backgroundColor: 'transparent',
    color: 'var(--a-text-dim)',
    border: 'none',
    padding: '6px 10px',
    fontSize: 13,
    fontWeight: 500,
    borderRadius: 4,
    cursor: 'pointer',
  },
  badge: {
    display: 'inline-flex', alignItems: 'center', gap: 4,
    padding: '2px 8px',
    fontSize: 11,
    fontWeight: 600,
    borderRadius: 3,
    fontFamily: 'var(--font-mono)',
    letterSpacing: '0.04em',
    textTransform: 'uppercase',
  },
};

const Badge = ({ status, children }) => {
  const map = {
    active: { bg: 'var(--s-success-bg)', fg: 'var(--s-success)' },
    paused: { bg: 'var(--s-warning-bg)', fg: 'var(--s-warning)' },
    expired: { bg: '#F5F5F5', fg: 'var(--a-text-dim)' },
    refunded: { bg: 'var(--s-danger-bg)', fg: 'var(--s-danger)' },
  };
  const c = map[status] || map.expired;
  return (
    <span style={{ ...aStyles.badge, backgroundColor: c.bg, color: c.fg }}>
      {children || status}
    </span>
  );
};

// Sidebar nav
const Sidebar = ({ active = 'members', role = 'global', branch = '강남점', collapsed = false }) => {
  const items = [
    { id: 'members', label: '회원 관리', icon: 'users', global: false },
    { id: 'plans', label: '회원권 관리', icon: 'card', global: false },
    { id: 'checkins', label: '체크인 이력', icon: 'check', global: false },
    { id: 'sales', label: '매출', icon: 'chart', global: true },
    { id: 'extend', label: '대량 연장', icon: 'plus', global: true },
    { id: 'branches', label: '지점/관리자', icon: 'building', global: true },
  ];
  const filtered = items.filter(i => role === 'global' || !i.global);

  const Icon = ({ name }) => {
    const paths = {
      users: <><circle cx="9" cy="7" r="4"/><path d="M3 21v-2a4 4 0 0 1 4-4h4a4 4 0 0 1 4 4v2M16 3.13a4 4 0 0 1 0 7.75M21 21v-2a4 4 0 0 0-3-3.87"/></>,
      card: <><rect x="2" y="5" width="20" height="14" rx="2"/><path d="M2 10h20"/></>,
      check: <><path d="M9 11l3 3L22 4M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11"/></>,
      chart: <><path d="M3 3v18h18M7 14l4-4 4 4 5-5"/></>,
      plus: <><circle cx="12" cy="12" r="10"/><path d="M12 8v8M8 12h8"/></>,
      building: <><rect x="4" y="2" width="16" height="20" rx="1"/><path d="M8 6h2M14 6h2M8 10h2M14 10h2M8 14h2M14 14h2M10 22v-4h4v4"/></>,
    };
    return <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8">{paths[name]}</svg>;
  };

  return (
    <div style={aStyles.sidebar}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '8px 12px', marginBottom: 16 }}>
        <PBoyLogo size={32}/>
        <div style={{ display: 'flex', flexDirection: 'column', lineHeight: 1.1 }}>
          <div style={{ fontFamily: 'var(--font-display)', fontWeight: 700, fontSize: 14, letterSpacing: '0.04em', color: '#fff' }}>P-BOY MMA</div>
          <div style={{ fontSize: 10, color: 'var(--a-sidebar-text-dim)', fontFamily: 'var(--font-mono)', letterSpacing: '0.05em' }}>ADMIN</div>
        </div>
      </div>

      {/* Branch selector */}
      <div style={{
        margin: '0 0 16px',
        padding: '10px 12px',
        border: '1px solid rgba(255,255,255,0.08)',
        borderRadius: 4,
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        cursor: 'pointer',
      }}>
        <div>
          <div style={{ fontSize: 10, color: 'var(--a-sidebar-text-dim)', fontFamily: 'var(--font-mono)', letterSpacing: '0.1em', marginBottom: 2 }}>
            {role === 'global' ? 'ALL BRANCHES' : 'BRANCH'}
          </div>
          <div style={{ fontSize: 13, fontWeight: 600, color: '#fff' }}>{role === 'global' ? '전체 지점' : branch}</div>
        </div>
        <span style={{ color: 'var(--a-sidebar-text-dim)', fontSize: 12 }}>▾</span>
      </div>

      <div style={{ fontSize: 10, color: 'var(--a-sidebar-text-dim)', fontFamily: 'var(--font-mono)', letterSpacing: '0.1em', padding: '8px 12px 6px' }}>
        MENU
      </div>
      {filtered.map(i => (
        <div key={i.id} style={{
          ...aStyles.navItem,
          ...(active === i.id ? aStyles.navItemActive : {}),
        }}>
          {active === i.id && <span style={{ position: 'absolute', left: 0, width: 3, height: 18, backgroundColor: 'var(--pb-red)', borderRadius: 1, marginLeft: -14 }}/>}
          <Icon name={i.icon}/>
          <span>{i.label}</span>
          {i.global && (
            <span style={{
              marginLeft: 'auto',
              fontFamily: 'var(--font-mono)',
              fontSize: 9,
              padding: '2px 5px',
              backgroundColor: 'rgba(225,6,0,0.15)',
              color: 'var(--pb-red)',
              borderRadius: 2,
              letterSpacing: '0.05em',
            }}>GLOBAL</span>
          )}
        </div>
      ))}

      <div style={{ marginTop: 'auto', borderTop: '1px solid rgba(255,255,255,0.08)', paddingTop: 12 }}>
        <div style={aStyles.navItem}>
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8">
            <rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>
          </svg>
          <span>비밀번호 변경</span>
        </div>
        <div style={aStyles.navItem}>
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8">
            <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9"/>
          </svg>
          <span>로그아웃</span>
        </div>
      </div>
    </div>
  );
};

const Topbar = ({ title, breadcrumb, action }) => (
  <div style={aStyles.topbar}>
    <div>
      {breadcrumb && (
        <div style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--a-text-muted)', letterSpacing: '0.1em', marginBottom: 4 }}>
          {breadcrumb}
        </div>
      )}
      <div style={{ fontSize: 18, fontWeight: 700, letterSpacing: '-0.01em' }}>{title}</div>
    </div>
    <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
      {action}
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, paddingLeft: 12, borderLeft: '1px solid var(--a-border)' }}>
        <div style={{ width: 32, height: 32, borderRadius: '50%', backgroundColor: 'var(--a-text)', color: 'var(--a-bg)', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 13, fontWeight: 600 }}>
          김
        </div>
        <div style={{ fontSize: 13 }}>
          <div style={{ fontWeight: 600 }}>김관리</div>
          <div style={{ fontSize: 11, color: 'var(--a-text-muted)' }}>전역 관리자</div>
        </div>
      </div>
    </div>
  </div>
);

window.aStyles = aStyles;
window.Badge = Badge;
window.Sidebar = Sidebar;
window.Topbar = Topbar;
window.ADMIN_W_DESKTOP = ADMIN_W_DESKTOP;
window.ADMIN_H_DESKTOP = ADMIN_H_DESKTOP;
window.ADMIN_W_MOBILE = ADMIN_W_MOBILE;
window.ADMIN_H_MOBILE = ADMIN_H_MOBILE;
