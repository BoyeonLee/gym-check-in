// Main canvas view — wires up all screens into a design canvas

const App = () => {
  const [tweaks, setTweak] = useTweaks(/*EDITMODE-BEGIN*/{
    "accentIntensity": "restrained",
    "tableDensity": "comfortable",
    "showAdminMobile": true
  }/*EDITMODE-END*/);

  return (
    <>
      <TweaksPanel title="Tweaks">
        <TweakSection title="브랜드">
          <TweakRadio
            label="액센트 강도"
            value={tweaks.accentIntensity}
            onChange={v => setTweak('accentIntensity', v)}
            options={[
              { value: 'restrained', label: 'Restrained' },
              { value: 'bold', label: 'Bold' },
            ]}
          />
        </TweakSection>
        <TweakSection title="관리자">
          <TweakRadio
            label="테이블 밀도"
            value={tweaks.tableDensity}
            onChange={v => setTweak('tableDensity', v)}
            options={[
              { value: 'compact', label: 'Compact' },
              { value: 'comfortable', label: 'Comfortable' },
            ]}
          />
          <TweakToggle
            label="모바일 뷰 표시"
            value={tweaks.showAdminMobile}
            onChange={v => setTweak('showAdminMobile', v)}
          />
        </TweakSection>
        <TweakSection title="프로토타입">
          <TweakButton
            label="키오스크 인터랙티브 프로토타입 열기"
            onClick={() => {
              // Preserve preview token (?t=...) and srcmap when navigating
              const search = window.location.search || '';
              window.location.href = 'kiosk-prototype.html' + search;
            }}
          />
          <div style={{ fontSize: 11, color: '#888', marginTop: 6, lineHeight: 1.5 }}>
            현재 탭에서 열립니다. 돌아오려면 브라우저 뒤로가기.
          </div>
        </TweakSection>
      </TweaksPanel>

      <DesignCanvas title="P-BOY MMA · Check-in System" subtitle="Kiosk + Admin · 6 priority screens · 다크/라이트 양쪽 모두 검토">

        {/* ============== KIOSK · DARK (default) ============== */}
        <DCSection id="kiosk-idle" title="01. 키오스크 Idle · DARK (기본)" subtitle="첫인상을 결정하는 화면 · 1280×800 가로 태블릿">
          <DCArtboard id="idle-energetic" label="Energetic" width={KIOSK_W} height={KIOSK_H}>
            <KioskIdle/>
          </DCArtboard>
        </DCSection>

        {/* ============== KIOSK · LIGHT ============== */}
        <DCSection id="kiosk-idle-light" title="01-L. 키오스크 Idle · LIGHT" subtitle="밝은 매장·자연광 환경용 · 동일 컴포넌트, 토큰만 반전">
          <DCArtboard id="idle-energetic-light" label="Energetic — Light" width={KIOSK_W} height={KIOSK_H}>
            <div className="theme-kiosk-light" style={{ width: '100%', height: '100%' }}>
              <KioskIdle/>
            </div>
          </DCArtboard>
        </DCSection>

        <DCSection id="kiosk-flow" title="01-2. 키오스크 흐름" subtitle="Idle 이후 단계들 — 프로토타입에서 실제로 클릭 가능">
          <DCArtboard id="kiosk-input" label="Step 1 — Input Select" width={KIOSK_W} height={KIOSK_H}>
            <KioskInputSelect/>
          </DCArtboard>
          <DCArtboard id="kiosk-voice" label="Step 2 — Voice (listening)" width={KIOSK_W} height={KIOSK_H}>
            <KioskVoiceSearch state="listening" attempt={1}/>
          </DCArtboard>
          <DCArtboard id="kiosk-voice-fail" label="Step 2 — Voice failed (3rd attempt)" width={KIOSK_W} height={KIOSK_H}>
            <KioskVoiceSearch state="failed" attempt={3} transcript="홍기 둥..."/>
          </DCArtboard>
          <DCArtboard id="kiosk-typing-num" label="Step 2 — Typing · 전화 뒷자리" width={KIOSK_W} height={KIOSK_H}>
            <KioskTypingSearch tab="phone" value="5678"/>
          </DCArtboard>
          <DCArtboard id="kiosk-typing-name" label="Step 2 — Typing · 한글 이름" width={KIOSK_W} height={KIOSK_H}>
            <KioskTypingSearch tab="name" value="홍길동"/>
          </DCArtboard>
        </DCSection>

        <DCSection id="kiosk-pick" title="02. 키오스크 MemberPick" subtitle="동명이인 구분 · 마스킹된 회원 row">
          <DCArtboard id="pick-default" label="기본 — 2명 후보" width={KIOSK_W} height={KIOSK_H}>
            <KioskMemberPick query="홍길동" candidates={[
              { id: 1, memberNo: 1247, name: '홍길동', phone: '010-1234-5678', birth: '1990-04-15', plan: '월권 3M' },
              { id: 2, memberNo: 894, name: '홍길동', phone: '010-9876-5432', birth: '1985-11-22', plan: '10회권' },
            ]}/>
          </DCArtboard>
          <DCArtboard id="pick-many" label="다수 — 4명 후보" width={KIOSK_W} height={KIOSK_H}>
            <KioskMemberPick query="김민" candidates={[
              { id: 1, memberNo: 1247, name: '김민준', phone: '010-1111-2222', birth: '1992-04-15', plan: '월권 12M' },
              { id: 2, memberNo: 894, name: '김민서', phone: '010-3333-4444', birth: '1988-11-22', plan: '월권 3M' },
              { id: 3, memberNo: 432, name: '김민호', phone: '010-5555-6666', birth: '1995-07-08', plan: '10회권' },
              { id: 4, memberNo: 1102, name: '김민지', phone: '010-7777-8888', birth: '1990-02-19', plan: '월권 6M' },
            ]}/>
          </DCArtboard>
          <DCArtboard id="kiosk-done" label="04. 키오스크 Done" width={KIOSK_W} height={KIOSK_H}>
            <KioskDone name="홍길동" memberNo={1247} plan="월권 3개월" daysLeft={47} todayCount={48}/>
          </DCArtboard>
        </DCSection>

        {/* ============== KIOSK 흐름 · LIGHT ============== */}
        <DCSection id="kiosk-flow-light" title="01-2-L. 키오스크 흐름 · LIGHT" subtitle="동일 단계, 토큰만 반전 — 밝은 매장 환경 검토용">
          <DCArtboard id="kiosk-input-light" label="Step 1 — Input Select · Light" width={KIOSK_W} height={KIOSK_H}>
            <div className="theme-kiosk-light" style={{ width: '100%', height: '100%' }}>
              <KioskInputSelect/>
            </div>
          </DCArtboard>
          <DCArtboard id="kiosk-voice-light" label="Step 2 — Voice (listening) · Light" width={KIOSK_W} height={KIOSK_H}>
            <div className="theme-kiosk-light" style={{ width: '100%', height: '100%' }}>
              <KioskVoiceSearch state="listening" attempt={1}/>
            </div>
          </DCArtboard>
          <DCArtboard id="kiosk-voice-fail-light" label="Step 2 — Voice failed · Light" width={KIOSK_W} height={KIOSK_H}>
            <div className="theme-kiosk-light" style={{ width: '100%', height: '100%' }}>
              <KioskVoiceSearch state="failed" attempt={3} transcript="홍기 둥..."/>
            </div>
          </DCArtboard>
          <DCArtboard id="kiosk-typing-num-light" label="Step 2 — Typing · 전화 · Light" width={KIOSK_W} height={KIOSK_H}>
            <div className="theme-kiosk-light" style={{ width: '100%', height: '100%' }}>
              <KioskTypingSearch tab="phone" value="5678"/>
            </div>
          </DCArtboard>
          <DCArtboard id="kiosk-typing-name-light" label="Step 2 — Typing · 이름 · Light" width={KIOSK_W} height={KIOSK_H}>
            <div className="theme-kiosk-light" style={{ width: '100%', height: '100%' }}>
              <KioskTypingSearch tab="name" value="홍길동"/>
            </div>
          </DCArtboard>
        </DCSection>

        {/* ============== KIOSK MemberPick · LIGHT ============== */}
        <DCSection id="kiosk-pick-light" title="02-L. 키오스크 MemberPick · LIGHT" subtitle="동명이인 구분 화면 — Light 변형">
          <DCArtboard id="pick-default-light" label="기본 — 2명 후보 · Light" width={KIOSK_W} height={KIOSK_H}>
            <div className="theme-kiosk-light" style={{ width: '100%', height: '100%' }}>
              <KioskMemberPick query="홍길동" candidates={[
                { id: 1, memberNo: 1247, name: '홍길동', phone: '010-1234-5678', birth: '1990-04-15', plan: '월권 3M' },
                { id: 2, memberNo: 894, name: '홍길동', phone: '010-9876-5432', birth: '1985-11-22', plan: '10회권' },
              ]}/>
            </div>
          </DCArtboard>
          <DCArtboard id="pick-many-light" label="다수 — 4명 후보 · Light" width={KIOSK_W} height={KIOSK_H}>
            <div className="theme-kiosk-light" style={{ width: '100%', height: '100%' }}>
              <KioskMemberPick query="김민" candidates={[
                { id: 1, memberNo: 1247, name: '김민준', phone: '010-1111-2222', birth: '1992-04-15', plan: '월권 12M' },
                { id: 2, memberNo: 894, name: '김민서', phone: '010-3333-4444', birth: '1988-11-22', plan: '월권 3M' },
                { id: 3, memberNo: 432, name: '김민호', phone: '010-5555-6666', birth: '1995-07-08', plan: '10회권' },
                { id: 4, memberNo: 1102, name: '김민지', phone: '010-7777-8888', birth: '1990-02-19', plan: '월권 6M' },
              ]}/>
            </div>
          </DCArtboard>
          <DCArtboard id="kiosk-done-light" label="04. 키오스크 Done · Light" width={KIOSK_W} height={KIOSK_H}>
            <div className="theme-kiosk-light" style={{ width: '100%', height: '100%' }}>
              <KioskDone name="홍길동" memberNo={1247} plan="월권 3개월" daysLeft={47} todayCount={48}/>
            </div>
          </DCArtboard>
        </DCSection>

        {/* ============== ADMIN · LIGHT (기본) ============== */}
        <DCSection id="admin-members" title="03. 관리자 · 회원 목록 · LIGHT (기본)" subtitle="반응형 — 데스크탑 테이블 → 모바일 카드 스택">
          <DCArtboard id="members-desktop" label="Desktop · 1440×900" width={ADMIN_W_DESKTOP} height={ADMIN_H_DESKTOP}>
            <AdminMembersList density={tweaks.tableDensity}/>
          </DCArtboard>
          <DCArtboard id="members-mobile" label="Mobile · 390×844" width={ADMIN_W_MOBILE} height={ADMIN_H_MOBILE}>
            <AdminMembersListMobile/>
          </DCArtboard>
        </DCSection>

        <DCSection id="admin-grant" title="04. 관리자 · 회원권 부여" subtitle="회원권 종류 · 기간 · 결제를 한 폼에 통합">
          <DCArtboard id="grant-desktop" label="Desktop" width={ADMIN_W_DESKTOP} height={ADMIN_H_DESKTOP}>
            <AdminPlanGrant/>
          </DCArtboard>
        </DCSection>

        <DCSection id="admin-sales" title="05. 관리자 · 매출 대시보드" subtitle="KPI 3장 + 일별 차트 + 수단별 비중 + 상세 표">
          <DCArtboard id="sales-desktop" label="Desktop · Standard" width={ADMIN_W_DESKTOP} height={ADMIN_H_DESKTOP}>
            <AdminSales/>
          </DCArtboard>
        </DCSection>

        <DCSection id="admin-login" title="06. 관리자 · 로그인" subtitle="중앙 정렬 · 로고 + 폼 · 키오스크와 시각적으로 연결되는 다크 배경">
          <DCArtboard id="login-dark" label="Dark (기본)" width={ADMIN_W_DESKTOP} height={ADMIN_H_DESKTOP}>
            <div className="theme-admin-dark" style={{ width: '100%', height: '100%' }}>
              <AdminLogin/>
            </div>
          </DCArtboard>
          <DCArtboard id="login-light" label="Light" width={ADMIN_W_DESKTOP} height={ADMIN_H_DESKTOP}>
            <AdminLogin/>
          </DCArtboard>
        </DCSection>

        {/* ============== ADMIN · DARK ============== */}
        <DCSection id="admin-dark" title="07. 관리자 · DARK 모드" subtitle="동일 화면, 토큰만 반전 — 야간 운영·다중 모니터 환경 검토용">
          <DCArtboard id="members-dark" label="회원 목록 — Dark" width={ADMIN_W_DESKTOP} height={ADMIN_H_DESKTOP}>
            <div className="theme-admin-dark" style={{ width: '100%', height: '100%' }}>
              <AdminMembersList density={tweaks.tableDensity}/>
            </div>
          </DCArtboard>
          <DCArtboard id="grant-dark" label="회원권 부여 — Dark" width={ADMIN_W_DESKTOP} height={ADMIN_H_DESKTOP}>
            <div className="theme-admin-dark" style={{ width: '100%', height: '100%' }}>
              <AdminPlanGrant/>
            </div>
          </DCArtboard>
          <DCArtboard id="sales-dark" label="매출 대시보드 — Dark" width={ADMIN_W_DESKTOP} height={ADMIN_H_DESKTOP}>
            <div className="theme-admin-dark" style={{ width: '100%', height: '100%' }}>
              <AdminSales/>
            </div>
          </DCArtboard>
        </DCSection>

      </DesignCanvas>
    </>
  );
};

const root = ReactDOM.createRoot(document.getElementById('root'));
root.render(<App/>);
