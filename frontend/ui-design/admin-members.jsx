// Admin screens — members list, plan grant form, sales dashboard, login

const sampleMembers = [
  { no: 1247, name: '김도윤', phone: '010-1234-5678', plan: '월권 3개월', status: 'active', start: '2026.02.10', end: '2026.05.10', daysLeft: 14, branch: '강남' },
  { no: 1246, name: '박지민', phone: '010-2345-6789', plan: '10회권', status: 'active', start: '2026.03.15', end: '2026.05.15', daysLeft: 18, branch: '강남', usage: '4/10' },
  { no: 1245, name: '이서연', phone: '010-3456-7890', plan: '월권 1개월', status: 'paused', start: '2026.04.01', end: '2026.05.01', daysLeft: 0, branch: '강남' },
  { no: 1244, name: '최현우', phone: '010-4567-8901', plan: '월권 6개월', status: 'active', start: '2025.11.20', end: '2026.05.20', daysLeft: 23, branch: '강남' },
  { no: 1243, name: '정유진', phone: '010-5678-9012', plan: '10회권', status: 'expired', start: '2026.01.10', end: '2026.03.10', daysLeft: -49, branch: '강남' },
  { no: 1242, name: '장민호', phone: '010-6789-0123', plan: '월권 12개월', status: 'active', start: '2025.08.05', end: '2026.08.05', daysLeft: 100, branch: '강남' },
  { no: 1241, name: '한소희', phone: '010-7890-1234', plan: '월권 3개월', status: 'refunded', start: '2026.03.01', end: '2026.06.01', daysLeft: -1, branch: '강남' },
  { no: 1240, name: '오재훈', phone: '010-8901-2345', plan: '10회권', status: 'active', start: '2026.04.10', end: '2026.06.10', daysLeft: 44, branch: '강남', usage: '2/10' },
];

const AdminMembersList = ({ density = 'comfortable' }) => {
  const rowH = density === 'compact' ? 44 : 56;
  return (
    <div style={{ ...aStyles.shell, width: ADMIN_W_DESKTOP, height: ADMIN_H_DESKTOP }}>
      <Sidebar active="members" role="global"/>
      <div style={aStyles.main}>
        <Topbar
          title="회원 관리"
          breadcrumb="DASHBOARD / MEMBERS"
          action={
            <>
              <button style={aStyles.btnSecondary}>
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M7 10l5 5 5-5M12 15V3"/></svg>
                내보내기
              </button>
              <button style={aStyles.btnPrimary}>
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5"><path d="M12 5v14M5 12h14"/></svg>
                회원 등록
              </button>
            </>
          }
        />
        <div style={aStyles.content}>
          {/* Stats strip */}
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 12, marginBottom: 20 }}>
            {[
              { label: '전체 회원', value: '847', sub: '+12 이번 주' },
              { label: '활성 회원권', value: '623', sub: '73.6%' },
              { label: '7일 내 만료', value: '34', sub: '연락 필요', accent: true },
              { label: '정지 중', value: '18', sub: '' },
            ].map((s, i) => (
              <div key={i} style={{ ...aStyles.card, padding: 16 }}>
                <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--a-text-muted)', letterSpacing: '0.1em', marginBottom: 8 }}>
                  {s.label.toUpperCase()}
                </div>
                <div style={{ display: 'flex', alignItems: 'baseline', gap: 8 }}>
                  <div style={{ fontFamily: 'var(--font-display)', fontSize: 28, fontWeight: 700, letterSpacing: '-0.02em' }}>{s.value}</div>
                  {s.sub && <div style={{ fontSize: 11, color: s.accent ? 'var(--pb-red)' : 'var(--a-text-muted)', fontWeight: 500 }}>{s.sub}</div>}
                </div>
              </div>
            ))}
          </div>

          {/* Filter bar */}
          <div style={{ ...aStyles.card, padding: 12, marginBottom: 16, display: 'flex', gap: 8, alignItems: 'center' }}>
            <div style={{
              flex: 1, display: 'flex', alignItems: 'center', gap: 8,
              padding: '8px 12px', backgroundColor: 'var(--a-surface-2)', borderRadius: 4,
            }}>
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--a-text-muted)" strokeWidth="2"><circle cx="11" cy="11" r="8"/><path d="M21 21l-4.35-4.35"/></svg>
              <span style={{ color: 'var(--a-text-muted)', fontSize: 13 }}>이름·전화·회원번호로 검색</span>
              <span style={{ marginLeft: 'auto', fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--a-text-muted)', letterSpacing: '0.1em' }}>⌘K</span>
            </div>
            {[
              { label: '상태', val: '전체' },
              { label: '회원권', val: '전체' },
              { label: '지점', val: '강남' },
              { label: '기간', val: '최근 30일' },
            ].map((f, i) => (
              <button key={i} style={{ ...aStyles.btnSecondary, fontSize: 12 }}>
                <span style={{ color: 'var(--a-text-muted)' }}>{f.label}:</span> <span style={{ fontWeight: 600 }}>{f.val}</span> <span style={{ fontSize: 10 }}>▾</span>
              </button>
            ))}
          </div>

          {/* Table */}
          <div style={{ ...aStyles.card, overflow: 'hidden' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr style={{ backgroundColor: 'var(--a-surface-2)', borderBottom: '1px solid var(--a-border)' }}>
                  {['회원번호','이름','연락처','회원권','상태','시작일','만료일','잔여','액션'].map((h, i) => (
                    <th key={i} style={{
                      textAlign: 'left',
                      padding: '10px 14px',
                      fontFamily: 'var(--font-mono)',
                      fontSize: 10,
                      letterSpacing: '0.1em',
                      color: 'var(--a-text-muted)',
                      fontWeight: 600,
                      textTransform: 'uppercase',
                    }}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {sampleMembers.map((m, i) => (
                  <tr key={m.no} style={{ borderBottom: '1px solid var(--a-border)', height: rowH }}>
                    <td style={{ padding: '0 14px', fontFamily: 'var(--font-mono)', fontWeight: 600, color: 'var(--pb-red)' }}>#{m.no}</td>
                    <td style={{ padding: '0 14px', fontWeight: 600 }}>{m.name}</td>
                    <td style={{ padding: '0 14px', color: 'var(--a-text-dim)', fontFamily: 'var(--font-mono)' }}>{m.phone}</td>
                    <td style={{ padding: '0 14px' }}>
                      {m.plan}
                      {m.usage && <span style={{ marginLeft: 8, fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--a-text-muted)' }}>{m.usage}</span>}
                    </td>
                    <td style={{ padding: '0 14px' }}><Badge status={m.status}>{m.status}</Badge></td>
                    <td style={{ padding: '0 14px', fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--a-text-dim)' }}>{m.start}</td>
                    <td style={{ padding: '0 14px', fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--a-text-dim)' }}>{m.end}</td>
                    <td style={{ padding: '0 14px', fontFamily: 'var(--font-mono)', fontWeight: 600, color: m.daysLeft < 7 && m.daysLeft >= 0 ? 'var(--pb-red)' : m.daysLeft < 0 ? 'var(--a-text-muted)' : 'var(--a-text)' }}>
                      {m.daysLeft >= 0 ? `${m.daysLeft}일` : '—'}
                    </td>
                    <td style={{ padding: '0 14px' }}>
                      <button style={{ ...aStyles.btnGhost, padding: '4px 8px' }}>상세</button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            {/* Pagination */}
            <div style={{ padding: '12px 14px', borderTop: '1px solid var(--a-border)', display: 'flex', justifyContent: 'space-between', alignItems: 'center', backgroundColor: 'var(--a-surface-2)' }}>
              <div style={{ fontSize: 12, color: 'var(--a-text-muted)' }}>1–8 / 847명</div>
              <div style={{ display: 'flex', gap: 4 }}>
                {['‹','1','2','3','...','107','›'].map((p, i) => (
                  <button key={i} style={{
                    minWidth: 28, height: 28,
                    border: '1px solid var(--a-border)',
                    backgroundColor: p === '1' ? 'var(--a-text)' : 'var(--a-surface)',
                    color: p === '1' ? '#fff' : 'var(--a-text)',
                    borderRadius: 4, fontSize: 12, cursor: 'pointer',
                    fontFamily: 'var(--font-mono)', fontWeight: 600,
                  }}>{p}</button>
                ))}
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
};

// Mobile card stack version
const AdminMembersListMobile = () => (
  <div style={{
    width: ADMIN_W_MOBILE, height: ADMIN_H_MOBILE,
    backgroundColor: 'var(--a-bg)',
    fontFamily: 'var(--font-kr)',
    color: 'var(--a-text)',
    display: 'flex', flexDirection: 'column',
    overflow: 'hidden',
  }}>
    {/* Mobile header */}
    <div style={{ padding: '14px 16px', backgroundColor: 'var(--a-sidebar)', color: '#fff', display: 'flex', alignItems: 'center', gap: 12 }}>
      <button style={{ background: 'transparent', border: 'none', color: '#fff', fontSize: 22, padding: 0 }}>☰</button>
      <PBoyLogo size={28}/>
      <div style={{ fontFamily: 'var(--font-display)', fontWeight: 700, fontSize: 13, letterSpacing: '0.04em' }}>P-BOY MMA</div>
      <div style={{ marginLeft: 'auto', width: 28, height: 28, borderRadius: '50%', backgroundColor: '#fff', color: 'var(--a-bg)', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 12, fontWeight: 600 }}>김</div>
    </div>

    <div style={{ padding: '16px 16px 8px' }}>
      <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--a-text-muted)', letterSpacing: '0.1em', marginBottom: 4 }}>MEMBERS</div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline' }}>
        <div style={{ fontSize: 22, fontWeight: 700 }}>회원 관리</div>
        <div style={{ fontSize: 12, color: 'var(--a-text-muted)' }}>847명</div>
      </div>
    </div>

    {/* Search */}
    <div style={{ padding: '8px 16px' }}>
      <div style={{
        display: 'flex', alignItems: 'center', gap: 8,
        padding: '10px 12px', backgroundColor: 'var(--a-surface)',
        border: '1px solid var(--a-border)', borderRadius: 6,
      }}>
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--a-text-muted)" strokeWidth="2"><circle cx="11" cy="11" r="8"/><path d="M21 21l-4.35-4.35"/></svg>
        <span style={{ color: 'var(--a-text-muted)', fontSize: 13 }}>이름·번호로 검색</span>
      </div>
    </div>

    {/* Filter chips */}
    <div style={{ padding: '8px 16px', display: 'flex', gap: 6, overflowX: 'auto' }}>
      {['전체', '활성', '정지', '만료 임박', '환불'].map((f, i) => (
        <button key={i} style={{
          padding: '6px 12px',
          backgroundColor: i === 0 ? 'var(--a-text)' : 'var(--a-surface)',
          color: i === 0 ? '#fff' : 'var(--a-text-dim)',
          border: '1px solid var(--a-border)',
          borderRadius: 16,
          fontSize: 12, fontWeight: 500,
          whiteSpace: 'nowrap',
        }}>{f}</button>
      ))}
    </div>

    {/* Cards */}
    <div style={{ flex: 1, overflow: 'auto', padding: '8px 16px 16px', display: 'flex', flexDirection: 'column', gap: 10 }}>
      {sampleMembers.slice(0, 5).map(m => (
        <div key={m.no} style={{
          ...aStyles.card,
          padding: 14,
          display: 'flex', flexDirection: 'column', gap: 10,
        }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
            <div>
              <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, marginBottom: 2 }}>
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--pb-red)', fontWeight: 600 }}>#{m.no}</span>
                <Badge status={m.status}/>
              </div>
              <div style={{ fontSize: 17, fontWeight: 700 }}>{m.name}</div>
              <div style={{ fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--a-text-dim)', marginTop: 2 }}>{m.phone}</div>
            </div>
            <div style={{ textAlign: 'right' }}>
              <div style={{ fontSize: 11, color: 'var(--a-text-muted)' }}>잔여</div>
              <div style={{ fontFamily: 'var(--font-mono)', fontWeight: 700, fontSize: 18, color: m.daysLeft < 7 && m.daysLeft >= 0 ? 'var(--pb-red)' : 'var(--a-text)' }}>
                {m.daysLeft >= 0 ? `${m.daysLeft}일` : '—'}
              </div>
            </div>
          </div>
          <div style={{ display: 'flex', justifyContent: 'space-between', paddingTop: 8, borderTop: '1px solid var(--a-border)', fontSize: 12 }}>
            <div>
              <div style={{ color: 'var(--a-text-muted)', fontSize: 10, fontFamily: 'var(--font-mono)', letterSpacing: '0.1em' }}>PLAN</div>
              <div style={{ fontWeight: 500 }}>{m.plan}{m.usage && ` · ${m.usage}`}</div>
            </div>
            <div style={{ textAlign: 'right' }}>
              <div style={{ color: 'var(--a-text-muted)', fontSize: 10, fontFamily: 'var(--font-mono)', letterSpacing: '0.1em' }}>END</div>
              <div style={{ fontFamily: 'var(--font-mono)', fontWeight: 500 }}>{m.end}</div>
            </div>
          </div>
        </div>
      ))}
    </div>
  </div>
);

window.AdminMembersList = AdminMembersList;
window.AdminMembersListMobile = AdminMembersListMobile;
window.sampleMembers = sampleMembers;
