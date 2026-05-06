// Admin: Plan grant form + Sales dashboard + Login

const AdminPlanGrant = () => (
  <div style={{ ...aStyles.shell, width: ADMIN_W_DESKTOP, height: ADMIN_H_DESKTOP }}>
    <Sidebar active="plans" role="global"/>
    <div style={aStyles.main}>
      <Topbar
        title="회원권 부여"
        breadcrumb="MEMBERS / 김도윤 #1247 / 회원권 부여"
        action={<><button style={aStyles.btnSecondary}>취소</button></>}
      />
      <div style={{ ...aStyles.content, display: 'grid', gridTemplateColumns: '1fr 360px', gap: 20, alignItems: 'start' }}>
        {/* Left: form */}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          {/* Member context */}
          <div style={{ ...aStyles.card, padding: 16, display: 'flex', alignItems: 'center', gap: 14 }}>
            <div style={{ width: 44, height: 44, borderRadius: 4, backgroundColor: 'var(--pb-red)', color: '#fff', display: 'flex', alignItems: 'center', justifyContent: 'center', fontWeight: 700, fontFamily: 'var(--font-display)', fontSize: 18 }}>김</div>
            <div style={{ flex: 1 }}>
              <div style={{ display: 'flex', alignItems: 'baseline', gap: 8 }}>
                <span style={{ fontSize: 16, fontWeight: 700 }}>김도윤</span>
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--pb-red)' }}>#1247</span>
              </div>
              <div style={{ fontSize: 12, color: 'var(--a-text-muted)', fontFamily: 'var(--font-mono)' }}>010-1234-5678 · 2025.06 가입 · 강남점</div>
            </div>
            <div style={{ display: 'flex', gap: 8 }}>
              <Badge status="active">활성</Badge>
              <span style={{ fontSize: 12, color: 'var(--a-text-muted)' }}>현재 월권 14일 남음</span>
            </div>
          </div>

          {/* Plan type selector */}
          <div style={{ ...aStyles.card, padding: 20 }}>
            <div style={{ fontSize: 11, fontFamily: 'var(--font-mono)', color: 'var(--a-text-muted)', letterSpacing: '0.1em', marginBottom: 12 }}>STEP 1 · 회원권 종류</div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
              <div style={{
                padding: 16, border: '2px solid var(--pb-red)', borderRadius: 6,
                backgroundColor: 'rgba(225,6,0,0.04)', cursor: 'pointer', position: 'relative',
              }}>
                <div style={{ position: 'absolute', top: 12, right: 12, width: 16, height: 16, borderRadius: '50%', backgroundColor: 'var(--pb-red)', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                  <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="#fff" strokeWidth="3"><path d="M5 13l4 4L19 7"/></svg>
                </div>
                <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--pb-red)', letterSpacing: '0.1em', marginBottom: 6 }}>MONTHLY</div>
                <div style={{ fontSize: 16, fontWeight: 700, marginBottom: 4 }}>월권</div>
                <div style={{ fontSize: 12, color: 'var(--a-text-dim)' }}>매일 무제한 이용 · N개월 단위</div>
              </div>
              <div style={{
                padding: 16, border: '1px solid var(--a-border-strong)', borderRadius: 6, cursor: 'pointer', position: 'relative',
              }}>
                <div style={{ position: 'absolute', top: 12, right: 12, width: 16, height: 16, borderRadius: '50%', border: '1.5px solid var(--a-border-strong)' }}/>
                <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--a-text-muted)', letterSpacing: '0.1em', marginBottom: 6 }}>PASS-10</div>
                <div style={{ fontSize: 16, fontWeight: 700, marginBottom: 4 }}>10회권</div>
                <div style={{ fontSize: 12, color: 'var(--a-text-dim)' }}>10회 이용 · 2개월 유효</div>
              </div>
            </div>

            {/* Monthly options */}
            <div style={{ marginTop: 20 }}>
              <div style={{ fontSize: 12, color: 'var(--a-text-dim)', marginBottom: 8 }}>기간</div>
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(5, 1fr)', gap: 8 }}>
                {[
                  { m: 1, p: '120,000' },
                  { m: 3, p: '330,000', active: true },
                  { m: 6, p: '600,000' },
                  { m: 12, p: '1,080,000', tag: '최대 할인' },
                  { m: 0, p: '커스텀' },
                ].map((o, i) => (
                  <div key={i} style={{
                    padding: 12, textAlign: 'center', cursor: 'pointer',
                    border: o.active ? '2px solid var(--a-text)' : '1px solid var(--a-border-strong)',
                    borderRadius: 4, position: 'relative',
                    backgroundColor: o.active ? 'var(--a-text)' : 'var(--a-surface)',
                    color: o.active ? '#fff' : 'var(--a-text)',
                  }}>
                    {o.tag && <div style={{ position: 'absolute', top: -8, right: 8, padding: '1px 6px', backgroundColor: 'var(--pb-red)', color: '#fff', fontSize: 9, fontFamily: 'var(--font-mono)', letterSpacing: '0.05em', borderRadius: 2 }}>{o.tag}</div>}
                    <div style={{ fontFamily: 'var(--font-display)', fontWeight: 700, fontSize: 20 }}>{o.m === 0 ? '✎' : `${o.m}M`}</div>
                    <div style={{ fontSize: 11, opacity: 0.8, marginTop: 2, fontFamily: 'var(--font-mono)' }}>{o.p === '커스텀' ? o.p : `₩${o.p}`}</div>
                  </div>
                ))}
              </div>
            </div>
          </div>

          {/* Dates */}
          <div style={{ ...aStyles.card, padding: 20 }}>
            <div style={{ fontSize: 11, fontFamily: 'var(--font-mono)', color: 'var(--a-text-muted)', letterSpacing: '0.1em', marginBottom: 12 }}>STEP 2 · 시작일 / 만료일</div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 24px 1fr 1fr', gap: 12, alignItems: 'end' }}>
              <FormField label="시작일" value="2026.05.10"/>
              <div style={{ textAlign: 'center', color: 'var(--a-text-muted)', paddingBottom: 12 }}>→</div>
              <FormField label="만료일 (자동 계산)" value="2026.08.10" readOnly/>
              <FormField label="총 일수" value="92일" readOnly mono/>
            </div>
          </div>

          {/* Payment */}
          <div style={{ ...aStyles.card, padding: 20 }}>
            <div style={{ fontSize: 11, fontFamily: 'var(--font-mono)', color: 'var(--a-text-muted)', letterSpacing: '0.1em', marginBottom: 12 }}>STEP 3 · 결제 정보</div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
              <FormField label="금액" value="330,000" suffix="원" mono/>
              <div>
                <div style={{ fontSize: 11, color: 'var(--a-text-muted)', fontFamily: 'var(--font-mono)', letterSpacing: '0.1em', marginBottom: 6 }}>결제 수단</div>
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 6 }}>
                  <button style={{ ...aStyles.btnSecondary, padding: '10px', justifyContent: 'center', backgroundColor: 'var(--a-text)', color: '#fff', borderColor: 'var(--a-text)' }}>💳 카드</button>
                  <button style={{ ...aStyles.btnSecondary, padding: '10px', justifyContent: 'center' }}>💵 현금</button>
                </div>
              </div>
              <FormField label="결제일" value="2026.05.10"/>
              <FormField label="메모 (선택)" value="" placeholder="할인 사유 등"/>
            </div>
          </div>
        </div>

        {/* Right: summary */}
        <div style={{ ...aStyles.card, padding: 20, position: 'sticky', top: 20 }}>
          <div style={{ fontSize: 11, fontFamily: 'var(--font-mono)', color: 'var(--a-text-muted)', letterSpacing: '0.1em', marginBottom: 16 }}>요약</div>
          {[
            ['회원', '김도윤 #1247'],
            ['종류', '월권 (Monthly)'],
            ['기간', '3개월'],
            ['시작일', '2026.05.10'],
            ['만료일', '2026.08.10'],
            ['수단', '카드'],
          ].map(([k, v]) => (
            <div key={k} style={{ display: 'flex', justifyContent: 'space-between', padding: '10px 0', borderBottom: '1px solid var(--a-border)', fontSize: 13 }}>
              <span style={{ color: 'var(--a-text-muted)' }}>{k}</span>
              <span style={{ fontWeight: 500 }}>{v}</span>
            </div>
          ))}
          <div style={{ display: 'flex', justifyContent: 'space-between', padding: '16px 0 8px' }}>
            <span style={{ fontSize: 13, color: 'var(--a-text-muted)' }}>결제 금액</span>
            <span style={{ fontFamily: 'var(--font-display)', fontWeight: 700, fontSize: 28, letterSpacing: '-0.02em' }}>
              ₩330,000
            </span>
          </div>
          <button style={{ ...aStyles.btnPrimary, width: '100%', justifyContent: 'center', padding: '14px', fontSize: 15, marginTop: 12 }}>
            회원권 부여 + 결제 완료
          </button>
          <button style={{ ...aStyles.btnGhost, width: '100%', justifyContent: 'center', marginTop: 6, fontSize: 12 }}>
            결제는 나중에
          </button>
        </div>
      </div>
    </div>
  </div>
);

const FormField = ({ label, value, placeholder, readOnly, mono, suffix }) => (
  <div>
    <div style={{ fontSize: 11, color: 'var(--a-text-muted)', fontFamily: 'var(--font-mono)', letterSpacing: '0.1em', marginBottom: 6 }}>
      {label.toUpperCase()}
    </div>
    <div style={{
      height: 40, padding: '0 12px',
      border: '1px solid var(--a-border-strong)',
      borderRadius: 4,
      backgroundColor: readOnly ? 'var(--a-surface-2)' : 'var(--a-surface)',
      color: value ? 'var(--a-text)' : 'var(--a-text-muted)',
      display: 'flex', alignItems: 'center', justifyContent: 'space-between',
      fontFamily: mono ? 'var(--font-mono)' : 'var(--font-kr)',
      fontSize: 14, fontWeight: 500,
    }}>
      <span>{value || placeholder}</span>
      {suffix && <span style={{ color: 'var(--a-text-muted)', fontSize: 12 }}>{suffix}</span>}
    </div>
  </div>
);

window.AdminPlanGrant = AdminPlanGrant;
window.FormField = FormField;
