# 운영 가이드

배포 후 운영 중에 발생하는 일들의 절차. MVP 범위에서 최소한만 정리한다. 호스팅 결정(ADR-010 예정)이 끝나면 플랫폼별 명령을 추가.

## 환경 분리
| 항목 | 개발(`APP_ENV=dev`) | 운영(`APP_ENV=prod`) |
|------|----|----|
| 프론트 URL | `http://localhost:5173` | `https://app.example.com` |
| API URL | `http://localhost:8080` | `https://api.example.com` |
| TLS | 없음 | 호스팅 플랫폼이 종료 + HSTS 헤더 |
| 비밀 관리 | 루트 `.env` (gitignore) | 호스팅 플랫폼 시크릿 매니저 |

운영 배포 시 시크릿(DB 비밀번호, `JWT_ACCESS_SECRET`, `JWT_REFRESH_SECRET`, 시드 해시)은 절대 `.env`로 커밋하지 않는다. Fly.io secrets / Railway env / AWS Secrets Manager 등 플랫폼 기능을 사용.

## HTTPS 인증서
- 호스팅 플랫폼(Fly.io / Railway / Render)이 자동 갱신해주는 Let's Encrypt 사용.
- 도메인 구매 후 플랫폼 콘솔에서 도메인 연결 → 자동 발급.
- Web Speech API와 PWA는 HTTPS가 필수다(localhost 예외 있음). 운영에서 HTTP면 키오스크 음성 인식이 동작하지 않는다.

## 백업·복구
**백업**:
- 호스팅이 제공하는 자동 일일 백업 사용(Fly.io Postgres / Railway Postgres 모두 옵션 제공).
- 추가로 주 1회 `pg_dump` → S3(또는 Backblaze B2) 압축 업로드를 cron으로. 보관 4주.

**복구 RPO/RTO** (MVP 목표):
- RPO: 24시간 (마지막 자동 백업까지)
- RTO: 4시간 (수동 복구 절차)

**복구 절차**:
1. 호스팅 콘솔에서 새 DB 인스턴스 생성
2. 가장 최근 백업으로 복원(`pg_restore` 또는 콘솔)
3. `DATABASE_URL`을 새 인스턴스로 교체 → 앱 재배포
4. 시드 관리자 비번 분실 시 아래 "전역 관리자 비번 분실" 절차

## 비밀번호 분실 절차

### 지점 관리자 비번 분실
1. 전역 관리자가 `/admin/admins`에서 해당 계정의 "비번 리셋" 버튼 클릭
2. 화면에 임시 비밀번호(12자리)가 1회 표시됨 (복사 버튼)
3. 해당 지점 관리자에게 안전한 채널(전화/대면)로 전달
4. 지점 관리자가 그 비번으로 로그인 → 강제 변경 화면 → 본인 비번으로 변경

**주의**: 임시 비번은 복사 후 화면을 닫으면 다시 볼 수 없다. 새로고침해도 안 보인다. 잃어버리면 다시 리셋.

### 전역 관리자 비번 분실(MVP)
셀프서비스 리셋 없음(ADR-006). DB 직접 접근으로 처리.

```bash
# 1. 운영 DB로 새 비번 해시 생성
ssh ops-host
go run ./backend/cmd/hashpw "$NEW_PASSWORD"
# → $2a$12$... 출력

# 2. DB에서 직접 갱신
psql "$DATABASE_URL" <<SQL
update admins
set password_hash = '<위의 해시>',
    must_change_password = true,
    failed_login_count = 0,
    locked_until = NULL
where role = 'global' and username = '<전역 관리자 username>';
SQL

# 3. 해당 사용자의 기존 refresh 토큰을 모두 무효화하고 싶다면
#    (revoked_refresh_tokens는 무효화 목록이므로, 비번 변경 후 발급된 토큰들을 거기에 INSERT 하는 게 맞다.
#     비번 변경 직후의 jti는 알 수 없으므로 보통 jti 단위 INSERT 대신 다음 로그인 전까지는 영향이 없다.
#     해당 사용자가 이미 로그인 상태라면 백엔드에 임시로 admin_audit_logs에서 jti를 회수하거나
#     JWT 비밀키 회전(아래 항목)으로 일괄 무효화한다.)
psql "$DATABASE_URL" -c "select jti from revoked_refresh_tokens where admin_id = <id> limit 1;"  # 존재 여부 확인용
```

이후 그 임시 비번으로 로그인 → 강제 변경 흐름. 평문 비번을 쉘 히스토리에 남기지 않으려면 `read -s NEW_PASSWORD`로 입력받고 실행 후 `unset NEW_PASSWORD`.

## JWT 비밀키 회전
`JWT_ACCESS_SECRET` 또는 `JWT_REFRESH_SECRET`이 노출되면(깃 실수 커밋, 로그 유출, 운영자 PC 손상 등) 즉시 회전한다.

```bash
# 1. 새 비밀키 생성 (각각 다른 값)
openssl rand -base64 48   # 새 access secret
openssl rand -base64 48   # 새 refresh secret

# 2. 호스팅 플랫폼 시크릿 매니저에서 두 값 갱신
#    (Fly.io: fly secrets set, Railway: 콘솔, AWS: Secrets Manager)

# 3. 백엔드 재배포(=재시작)
```

회전 직후 결과:
- 기존에 발급된 모든 access JWT가 즉시 무효화 → API 호출이 401.
- 기존 refresh JWT도 즉시 무효화 → 자동 갱신 불가.
- **모든 관리자가 강제 로그아웃되어 다시 로그인해야 한다.**

운영 영향이 크므로 정기 회전(분기·반기)은 권장하지 않고, **노출 의심 시에만** 회전한다. 회전 사실은 관리자 계정 채널(전화/대면)로 사전 공지.

## 마이그레이션 적용 (배포 파이프라인)
모든 스키마 변경은 goose 마이그레이션. 배포 순서:

1. 새 코드 배포 **전에** 마이그레이션 적용
   ```bash
   goose -dir db/migrations postgres "$DATABASE_URL" up
   ```
2. 마이그레이션이 backward-compatible(기존 코드와 호환)하면 0-downtime. NOT NULL 컬럼 추가·DROP 같은 breaking change는 두 번 배포 (1) NULL 허용 추가 + 백필 → (2) NOT NULL로 전환·이전 컬럼 DROP.
3. `goose down`은 운영에서 자동 실행하지 않는다(데이터 손실 위험). 롤백 필요 시 운영자가 수동 결정.
4. 새 코드 배포 후 `goose status`로 적용 상태 확인.

## 자정 배치 실패 대응
인-프로세스 cron이 매일 KST 00:01에 실행. 실패 시:

1. 백엔드 로그에서 `batch.run-expiry` 슬러그 검색
2. 수동으로 1회 실행:
   ```bash
   ./bin/server batch run-expiry
   ```
3. 실행 결과(전환된 row 수)를 로그로 확인
4. 실패가 반복되면 외부 스케줄러(Fly.io scheduled machines / Railway cron)로 분리(ROADMAP Phase 4 검토 사항)

배치가 하루 누락되면 만료된 회원권이 다음 날까지 active로 남아 체크인이 통과될 수 있다. 운영자가 수동 실행하면 즉시 정상화.

## 로그·관측성 (MVP)
- 백엔드: `slog` 구조화 로그를 stdout으로. 호스팅 플랫폼이 수집·검색 제공. 필드: `request_id`, `admin_id`, `ip`, `method`, `path`, `status`, `duration_ms`, `error_code`.
- `X-Request-ID`: 모든 요청·응답에 포함. 사용자 문의 시 이 값을 받으면 즉시 추적 가능.
- 감사 로그(`admin_audit_logs`): 로그인·관리자/지점 CRUD·비번 변경/리셋이 자동 기록. 보관 1년(자정 배치가 정리). 보안 사고 조사 1차 자료.
- 키오스크/관리자 프론트: 에러는 콘솔 로그 + 사용자에게 토스트. 별도 Sentry 등 도입은 Phase 4 이후.
- **로그에 절대 남기지 않는 것**: 비밀번호, JWT 토큰, 회원 전화번호, 생년월일, 임시 비밀번호.

## 태블릿 운영

### 권장 환경
- **Android 태블릿 + Chrome** (가장 안정. Web Speech API 한국어 인식이 가장 잘 됨).
- 화면 크기 10인치 이상. 9.7인치도 가능하나 7인치는 비추(터치 타겟 64px 기준 미달).

### 최초 설정 절차
1. Wi-Fi 연결
2. Chrome으로 운영 URL 접속 → 관리자 로그인 → 지점 선택 → `localStorage`에 저장됨
3. Chrome 메뉴 → "홈 화면에 추가" → 아이콘 생성
4. 추가된 아이콘으로 진입 → 주소창·탭 없이 풀스크린 표시 확인
5. 회원에게 안내문 부착(키오스크 재시작 시 풀스크린 다시 진입 방법)

### 지점 재설정
관리자가 키오스크 설정을 바꾸려면:
1. 화면 우상단 5초 롱프레스 → 지점 재설정 화면 진입
2. 관리자 로그인 → 새 지점 선택
3. `localStorage.branchId` 갱신

### 추가 잠금(MVP 범위 밖)
회원이 임의 종료·다른 앱 전환을 막고 싶으면:
- Android: Fully Kiosk Browser (유료, 라이선스 ~$10) 또는 OS 화면 고정
- iOS: Guided Access 모드(설정 → 손쉬운 사용 → 가이드 접근)

## 매출·체크인 데이터 정정
잘못 입력된 결제·체크인을 수정해야 할 때:
- **체크인은 삭제하지 않는다**. 잘못된 row가 있어도 `aggregate=daily` 집계가 회원 단위 1회로 수렴.
- **결제는 환불 row로 상쇄**한다. 잘못된 결제는 같은 금액의 음수 row를 같은 `paid_at`으로 추가해 매출이 0이 되게 한다(`POST /api/memberships/:id/refund` + 사유 메모로 구분).
- 직접 DB UPDATE는 피한다. 추적 어렵고 `membership_events` 이력이 남지 않는다.

## 배포 체크리스트(운영 첫 배포)
- [ ] 호스팅 플랫폼 결정 (ADR-010)
- [ ] 도메인 구매 + DNS 설정
- [ ] HTTPS 인증서 발급 확인
- [ ] 시크릿 등록(DB 비번, JWT_ACCESS_SECRET, JWT_REFRESH_SECRET)
- [ ] 운영 DB에 마이그레이션 적용
- [ ] 운영 DB에 시드 적용(전역 관리자 1명 + 지점)
- [ ] `APP_ENV=prod` 설정 확인 → HSTS 헤더 응답에 포함되는지 확인
- [ ] 자정 배치가 KST 기준으로 실행되는지 다음 날 로그 확인
- [ ] 태블릿 1대로 풀 시나리오 테스트(로그인 → 지점 선택 → 회원 등록 → 회원권 부여 → 체크인)
- [ ] 백업 자동화 설정 + 1회 복구 리허설
- [ ] 전역 관리자에게 비밀번호 분실 절차 문서 전달
