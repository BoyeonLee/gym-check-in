# Phase 2 Acceptance Checklist

`docs/ROADMAP.md` Phase 2 "검증 기준" 전체를 backend 테스트 코드와 매핑한다. 각 체크 항목 줄 끝의 주석 토큰은 해당 검증을 책임지는 실제 테스트 위치를 가리킨다.

이 문서는 step9의 자가 점검 산출물이다. step9 시점엔 backend agent가 worktree에서 만들어야 하는 hook 정책 때문에 `backend/PHASE2_AC.md`로 작성됐지만, phase 종료 후 spec drift 검증·매핑 갱신을 메인 세션이 직접 할 수 있도록 `phases/phase2-backend-scaffold/`로 이동했다(2026-05-11 postaudit). 검증 스크립트의 `backend/<file>` prefix는 그대로 유효 — 매핑이 가리키는 테스트 파일은 여전히 `backend/internal/...` 경로다.

매핑 규칙: 체크 항목 줄 끝에는 정확히 한 번 매핑 토큰을 넣는다. 본 문서의 prose에는 매핑 토큰 키워드를 다시 등장시키지 않는다(검증 스크립트가 prose를 가짜 양성으로 잡지 않도록).

검증 스크립트: AC 섹션의 awk 명령(체크 항목 줄에서만 매핑을 추출)을 실행하면 `MISS:` 0줄이어야 한다.

## 인증·세션

- [x] 시드 관리자 로그인 → access/refresh + must_change_password=true (step3)  // covered: internal/http/admins_auth_test.go:TestLogin_Success
- [x] 잘못된 비밀번호 로그인 → 401 (step3)  // covered: internal/http/admins_auth_test.go:TestLogin_WrongPassword
- [x] 존재하지 않는 username 로그인 → 401 (step3)  // covered: internal/http/admins_auth_test.go:TestLogin_UnknownUsername
- [x] 5번 비번 틀림 → ACCOUNT_LOCKED, 잠금 동안 정확한 비번도 401 (step3)  // covered: internal/http/admins_auth_test.go:TestLogin_AccountLocked
- [x] 임시 비번 24h 만료 후 로그인 → TEMP_PASSWORD_EXPIRED (step3+step4)  // covered: internal/http/admins_auth_test.go:TestLogin_TempPasswordExpired
- [x] access 만료 → refresh로 새 access 발급, 로그아웃 후 같은 refresh → 401 (step3)  // covered: internal/http/admins_auth_test.go:TestRefresh_RoundTripAndRevoked
- [x] 비번 변경 후 변경 전 refresh 토큰 무효 (step3)  // covered: internal/http/admins_auth_test.go:TestRefresh_StaleAfterPasswordChange
- [x] 약한 비번 변경 시도 → WEAK_PASSWORD (step3)  // covered: internal/http/admins_auth_test.go:TestPasswordChange_WeakPassword
- [x] 비번 변경 시 현재 비번 불일치 → WRONG_CURRENT_PASSWORD (step3)  // covered: internal/http/admins_auth_test.go:TestPasswordChange_WrongCurrent
- [x] 로그아웃 두 번째 호출은 멱등 응답 (step3)  // covered: internal/http/admins_auth_test.go:TestLogout_IdempotentSecondCall
- [x] refresh JWT sub와 호출자 access sub 불일치 → 401 (step3)  // covered: internal/http/admins_auth_test.go:TestLogout_RefreshSubMismatch
- [x] Auth 미들웨어 valid token 통과 + claim 컨텍스트 주입 (step3)  // covered: internal/http/middleware/auth_test.go:TestRequireAuth_ValidToken
- [x] Authorization 헤더 누락 → 401 (step3)  // covered: internal/http/middleware/auth_test.go:TestRequireAuth_MissingHeader
- [x] 만료 access 토큰 → 401 (step3)  // covered: internal/http/middleware/auth_test.go:TestRequireAuth_ExpiredToken
- [x] soft-deleted admin access → 401 즉시 (step3)  // covered: internal/http/middleware/auth_test.go:TestRequireAuth_SoftDeletedAdmin
- [x] 비번 변경 후 stale access (iat<password_updated_at) → 401 (step3)  // covered: internal/http/middleware/auth_test.go:TestRequireAuth_StaleAfterPasswordChange
- [x] must_change_password 가드 — 차단 라우트면 MUST_CHANGE_PASSWORD (step3)  // covered: internal/http/middleware/auth_test.go:TestMustChangePasswordGuard
- [x] RoleGuard RequireGlobal/RequireBranch 분기 (step3)  // covered: internal/http/middleware/auth_test.go:TestRequireGlobalAndRequireBranch
- [x] access claim 필수 필드 누락된 토큰 → 401 (step3)  // covered: internal/auth/jwt_test.go:TestParseAccessRejectsMissingClaims
- [x] access 토큰 서명 불일치 → 401 (step3)  // covered: internal/auth/jwt_test.go:TestParseAccessRejectsBadSignature
- [x] access 토큰 alg=none → 401 (step3)  // covered: internal/auth/jwt_test.go:TestParseAccessRejectsAlgNone
- [x] access 만료 검증 (step3)  // covered: internal/auth/jwt_test.go:TestParseAccessRejectsExpired
- [x] refresh JWT 만료/cross-secret → 401 (step3)  // covered: internal/auth/jwt_test.go:TestParseRefreshRejectsExpiredAndCrossSecret

## 관리자 CRUD

- [x] 지점 관리자 토큰으로 admins 접근 → 403 (step4)  // covered: internal/http/admins_test.go:TestAdmins_BranchAdminForbidden
- [x] GET /api/admins가 branch_name JOIN 반환 (step4)  // covered: internal/http/admins_test.go:TestAdmins_ListIncludesJoinedBranchName
- [x] POST /api/admins + USERNAME_DUPLICATE 처리 (step4)  // covered: internal/http/admins_test.go:TestAdmins_CreateAndDuplicate
- [x] POST /api/admins 약한 비번 → WEAK_PASSWORD (step4)  // covered: internal/http/admins_test.go:TestAdmins_CreateWeakPassword
- [x] POST /api/admins role/branch_id 조합 불일치 → INVALID_ROLE_BRANCH (step4)  // covered: internal/http/admins_test.go:TestAdmins_CreateRoleBranchMismatch
- [x] 본인 PATCH role/branch_id 변경 → CANNOT_MODIFY_SELF_ROLE (step4)  // covered: internal/http/admins_test.go:TestAdmins_PatchSelfRoleBlocked
- [x] PATCH branch_id 변경 → 해당 사용자 refresh 무효 (step4)  // covered: internal/http/admins_test.go:TestAdmins_PatchBranchChangeInvalidatesTokens
- [x] PATCH username 중복 → USERNAME_DUPLICATE (step4)  // covered: internal/http/admins_test.go:TestAdmins_PatchUsernameDuplicate
- [x] PATCH role/branch_id 조합 불일치 → INVALID_ROLE_BRANCH (step4)  // covered: internal/http/admins_test.go:TestAdmins_PatchInvalidRoleBranch
- [x] PATCH soft-deleted admin → 404 (step4)  // covered: internal/http/admins_test.go:TestAdmins_PatchSoftDeletedNotFound
- [x] 본인 계정 DELETE → CANNOT_DELETE_SELF (step4)  // covered: internal/http/admins_test.go:TestAdmins_DeleteSelfBlocked
- [x] DELETE admin happy path + 미존재 404 + audit admin_delete 기록 (step4)  // covered: internal/http/admins_test.go:TestAdmins_DeleteHappyAndMissing
- [x] reset-password 12자 임시 비번 발급 + 응답 1회 + audit password_reset (step4)  // covered: internal/http/admins_test.go:TestAdmins_ResetPasswordHappyPath
- [x] 본인 reset-password → CANNOT_RESET_SELF (step4)  // covered: internal/http/admins_test.go:TestAdmins_ResetPasswordSelfBlocked
- [x] reset-password expires_at = now + 24h (step4)  // covered: internal/http/admins_test.go:TestAdmins_ResetPasswordExpiresIn24h

## 지점 CRUD

- [x] GET /api/branches 공개(키오스크용) — 미인증 통과 (step5)  // covered: internal/http/branches_test.go:TestBranches_ListPublic
- [x] GET/POST 라운드 트립 + audit branch_create (step4)  // covered: internal/http/branches_test.go:TestBranches_ListAndCreateRoundTrip
- [x] 지점 관리자 토큰으로 POST/PATCH/DELETE → 403 (step4)  // covered: internal/http/branches_test.go:TestBranches_BranchAdminCannotMutate
- [x] POST address 중복 → ADDRESS_DUPLICATE (step4)  // covered: internal/http/branches_test.go:TestBranches_CreateAddressDuplicate
- [x] POST name 길이 위반 등 → INVALID_INPUT (step4)  // covered: internal/http/branches_test.go:TestBranches_CreateInvalidName
- [x] PATCH name/address + ADDRESS_DUPLICATE + audit branch_update (step4)  // covered: internal/http/branches_test.go:TestBranches_PatchUpdatesAndDuplicate
- [x] DELETE 빈 지점 성공 + 사용 중 BRANCH_IN_USE + audit branch_delete (step4)  // covered: internal/http/branches_test.go:TestBranches_DeleteEmptyAndInUse

## 회원 CRUD·키오스크 검색

- [x] GET /api/members 인증 필수 → 401 (step5)  // covered: internal/http/members_test.go:TestMembers_GET_RequiresAuth
- [x] 지점 관리자는 자기 지점 회원만 조회 (step5)  // covered: internal/http/members_test.go:TestMembers_BranchAdminScopedList
- [x] 다른 지점 회원 GET /api/members/:id → 404 (step5)  // covered: internal/http/members_test.go:TestMembers_GetById_OtherBranch_404
- [x] cursor 페이지네이션 라운드 트립 (step5)  // covered: internal/http/members_test.go:TestMembers_Pagination
- [x] limit 범위 밖 → INVALID_LIMIT (step5)  // covered: internal/http/members_test.go:TestMembers_LimitOutOfRange
- [x] 잘못된 cursor → INVALID_CURSOR (step5)  // covered: internal/http/members_test.go:TestMembers_BadCursor
- [x] POST /api/members + 같은 지점 phone 중복 → PHONE_DUPLICATE (step5)  // covered: internal/http/members_test.go:TestMembers_CreateAndPhoneDuplicate
- [x] 지점 관리자 POST는 branch_id를 자기 지점으로 강제 (step5)  // covered: internal/http/members_test.go:TestMembers_BranchAdminPostForcesOwnBranch
- [x] POST phone이 11자리 숫자 아님 → INVALID_PHONE (step5)  // covered: internal/http/members_test.go:TestMembers_CreateInvalidPhone
- [x] PATCH /api/members/:id branch_id 무시 + 다른 필드만 갱신 (step5)  // covered: internal/http/members_test.go:TestMembers_PatchIgnoresBranchID
- [x] DELETE soft delete + GET 후 404 (step5)  // covered: internal/http/members_test.go:TestMembers_DeleteAndGet404
- [x] 관리자 목록은 PII 풀 노출(키오스크 마스킹과 분리) (step5)  // covered: internal/http/members_test.go:TestMembers_AdminListExposesFullPII
- [x] GET /api/branches 공개 (키오스크 부트스트랩) (step5)  // covered: internal/http/kiosk_test.go:TestKiosk_BranchesPublic
- [x] GET /api/members/search 응답 마스킹(phone_masked, birth_md, member_id_display) (step5)  // covered: internal/http/kiosk_test.go:TestKiosk_SearchPublicAndMasked
- [x] search는 활성 회원권 없는 회원 제외(expired/paused/refunded/없음) (step5)  // covered: internal/http/kiosk_test.go:TestKiosk_SearchExcludesInactive
- [x] search 잘못된 입력(mode·q 위반) → 400 분기(QUERY_TOO_SHORT/INVALID_PHONE_QUERY/INVALID_MEMBER_ID) (step5)  // covered: internal/http/kiosk_test.go:TestKiosk_SearchInvalidInput
- [x] GET /api/check-ins/today-count KST 기준 카운트 (step5+step7)  // covered: internal/http/kiosk_test.go:TestKiosk_TodayCount
- [x] repo search 결과 21명 초과 → truncated=true (step5)  // covered: internal/repo/members_repo_test.go:TestMembers_Search_Truncated

## 회원권 라이프사이클

- [x] monthly 부여 성공 (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Grant_Monthly_Success
- [x] pass10 부여 성공 (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Grant_Pass10_Success
- [x] Idempotency-Key 같은 body 재호출 시 첫 응답 재반환 (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Grant_IdempotencyReplay
- [x] Idempotency-Key 같은 키·다른 body → IDEMPOTENCY_KEY_CONFLICT (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Grant_IdempotencyConflict
- [x] 부여 시 Idempotency-Key 누락 → 400 IDEMPOTENCY_KEY_REQUIRED (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Grant_IdempotencyKeyRequired
- [x] Idempotency-Key UUIDv4 아님 → INVALID_IDEMPOTENCY_KEY (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Grant_InvalidIdempotencyKey
- [x] start_date 어제 → INVALID_START_DATE (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Grant_InvalidStartDate
- [x] payment.amount <= 0 → INVALID_AMOUNT (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Grant_InvalidAmount
- [x] 잘못된 type/months 조합 → INVALID_INPUT (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Grant_InvalidInput
- [x] 다른 지점 회원에 부여 시도 → 404 (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Grant_OtherBranchMember_404
- [x] soft-deleted 회원에 부여 시도 → 404 (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Grant_SoftDeletedMember_404
- [x] 기간 겹치는 부여 → MEMBERSHIP_PERIOD_OVERLAP (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Grant_PeriodOverlap
- [x] 겹치지 않는 미래 회원권 미리 등록은 통과 (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Grant_FutureNoOverlap_OK
- [x] paid_at/branch_id는 클라 입력 무시·서버 자동 (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Grant_IgnoresClientPaidAtAndBranchID
- [x] 응답 timestamp가 KST(+09:00) 직렬화 (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Grant_TimestampKSTOffset
- [x] GET /api/memberships/:id 본문(payments+events 포함) (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Get_Success
- [x] 환불 후 GET 응답에 음수 결제 row 포함 (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Get_AfterRefund_HasNegativePayment
- [x] 다른 지점 회원권 GET → 404 (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Get_OtherBranch_404
- [x] 존재하지 않는 회원권 GET → 404 (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Get_NotExist_404
- [x] pause 즉시 적용 (start_date<=오늘) → status=paused + end_date 연장 (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Pause_Immediate_Success
- [x] pause 미래 예약 (start_date>오늘) → status=active 유지 (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Pause_Future_StaysActive
- [x] 한 회원권 두 번째 pause → PAUSE_ALREADY_USED (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Pause_AlreadyUsed
- [x] pause 범위 검증 위반 → INVALID_PAUSE_RANGE (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Pause_InvalidPauseRange
- [x] pause end_date 연장 결과가 미래 회원권과 겹침 → MEMBERSHIP_PERIOD_OVERLAP (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Pause_OverlapWithFuture
- [x] 다른 지점 회원권 pause → 404 (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Pause_NotFound
- [x] paused 상태에서 unpause → end_date 잔여 정지일만큼 단축 (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Unpause_Success
- [x] active 회원권에 unpause → NOT_PAUSED (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Unpause_NotPaused
- [x] 다른 지점 회원권 unpause → 404 (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Unpause_NotFound
- [x] 미래 예약 정지 cancel-pause → end_date 복원 + pause_used=false (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_CancelPause_Success
- [x] 즉시 정지(paused) 또는 정지 미예약 상태 cancel-pause → PAUSE_NOT_SCHEDULED (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_CancelPause_NotScheduled
- [x] 다른 지점 회원권 cancel-pause → 404 (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_CancelPause_NotFound
- [x] 환불 정상 — status=refunded + 음수 결제 row (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Refund_Success
- [x] paused 회원권 환불 허용 (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Refund_PausedAllowed
- [x] active+미래 시작 회원권 환불 허용 (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Refund_FutureStartAllowed
- [x] expired 회원권 환불 시도 → MEMBERSHIP_ALREADY_EXPIRED (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Refund_Expired
- [x] 환불 Idempotency-Key 재호출 → 첫 응답 (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Refund_IdempotencyReplay
- [x] 환불 Idempotency-Key 누락 → IDEMPOTENCY_KEY_REQUIRED (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Refund_IdempotencyKeyRequired
- [x] 환불 Idempotency-Key UUIDv4 아님 → INVALID_IDEMPOTENCY_KEY (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Refund_InvalidIdempotencyKey
- [x] 환불 row의 paid_at/method/amount/branch_id 서버 자동·클라 입력 무시 (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Refund_IgnoresClientFields
- [x] 다른 지점 회원권 환불 → 404 (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_Refund_OtherBranch_404
- [x] memberships 라우트는 미인증 시 401 (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_NoAuth_401
- [x] must_change_password=true 토큰은 memberships 라우트 차단 (step6+step10)  // covered: internal/http/memberships_test.go:TestMembership_MustChangePasswordBlocks
- [x] events_repo.ListEventsByMembership 정렬 created_at DESC (postaudit, API.md L302 정합)  // covered: internal/repo/events_repo_test.go:TestInsertEvent_PauseAndUnpauseRoundtrip
- [x] payments_repo.ListPaymentsByMembership 정렬 paid_at DESC (postaudit, API.md L246·L301 정합)  // covered: internal/repo/payments_repo_test.go:TestListPaymentsByMembership_OrderingIncludesRefund

## 체크인

- [x] 체크인 공개 라우트(미인증 통과) (step7)  // covered: internal/http/checkins_test.go:TestCheckIn_PublicNoAuth
- [x] 활성 회원권 없음 → NO_ACTIVE_MEMBERSHIP (step7)  // covered: internal/http/checkins_test.go:TestCheckIn_NoActiveMembership
- [x] 미래 시작 회원권 체크인 → MEMBERSHIP_NOT_STARTED (step7)  // covered: internal/http/checkins_test.go:TestCheckIn_FutureStart
- [x] paused 회원권 체크인 → 422 NO_ACTIVE_MEMBERSHIP (step7)  // covered: internal/http/checkins_test.go:TestCheckIn_PausedMembershipFails
- [x] 키오스크 응답에 PII·결제 정보 없음 (step7)  // covered: internal/http/checkins_test.go:TestCheckIn_KioskResponseHasNoPII
- [x] 같은 회원·지점 5초 내 두 번 체크인 → 같은 응답·row 1개 (step7)  // covered: internal/http/checkins_test.go:TestCheckIn_DoubleClickIdempotent
- [x] pass10 잔여 차감 + remaining=0이면 같은 트랜잭션 status=expired (step7)  // covered: internal/http/checkins_test.go:TestCheckIn_Pass10DecrementsAndExpiresAtZero
- [x] 체크인 입력 검증 (member_id/branch_id 누락 등) → 400 (step7)  // covered: internal/http/checkins_test.go:TestCheckIn_BadInput
- [x] GET /api/check-ins 인증 필수 → 401 (step7)  // covered: internal/http/checkins_test.go:TestCheckInList_RequiresAuth
- [x] 지점 관리자는 자기 지점만 조회 (step7)  // covered: internal/http/checkins_test.go:TestCheckInList_BranchScope
- [x] 전역 관리자 branchId 필터 (step7)  // covered: internal/http/checkins_test.go:TestCheckInList_GlobalBranchFilter
- [x] aggregate enum 외 값 → INVALID_AGGREGATE (step7)  // covered: internal/http/checkins_test.go:TestCheckInList_InvalidAggregate
- [x] aggregate=daily에서 from~to 92일 초과 → RANGE_TOO_LARGE (step7)  // covered: internal/http/checkins_test.go:TestCheckInList_RangeTooLarge
- [x] 잘못된 cursor → INVALID_CURSOR (step7)  // covered: internal/http/checkins_test.go:TestCheckInList_InvalidCursor
- [x] daily 집계 — 같은 회원 같은 날 1 row, checkin_count=2 (step7)  // covered: internal/http/checkins_test.go:TestCheckInList_DailyAggregate
- [x] repo DoCheckIn monthly row insert (step7)  // covered: internal/repo/checkins_repo_test.go:TestDoCheckIn_MonthlyInsertsRow
- [x] repo DoCheckIn pass10 차감 + expired 전환 (step7)  // covered: internal/repo/checkins_repo_test.go:TestDoCheckIn_Pass10_DecrementsAndExpires
- [x] repo DoCheckIn 같은 날 두 번째는 차감 없음 (step7)  // covered: internal/repo/checkins_repo_test.go:TestDoCheckIn_SameDayTwice_DoesNotDecrementTwice
- [x] repo DoCheckIn 활성 회원권 없음 → ErrNoRows (step7)  // covered: internal/repo/checkins_repo_test.go:TestDoCheckIn_NoActiveMembership_ReturnsErrNoRows
- [x] repo FindUnstartedMembership → 미래 회원권 row (step7)  // covered: internal/repo/checkins_repo_test.go:TestFindUnstartedMembership_ReturnsFutureRow
- [x] repo DoCheckIn 다른 지점(branch_id) 호출 → ErrNoRows (조작된 클라 차단 안전망, postaudit)  // covered: internal/repo/checkins_repo_test.go:TestDoCheckIn_RejectsForeignBranch

## 매출·대량 연장

- [x] 매출 응답 gross/refund/net 분리 (step7)  // covered: internal/http/sales_test.go:TestSales_GlobalSummary
- [x] 지점 관리자 토큰으로 sales 조회 → 403 (step7)  // covered: internal/http/sales_test.go:TestSales_BranchAdminForbidden
- [x] 전역 관리자 branchId 필터 (step7)  // covered: internal/http/sales_test.go:TestSales_GlobalBranchFilter
- [x] 잘못된 from/to/aggregate → 400 (step7)  // covered: internal/http/sales_test.go:TestSales_InvalidQuery
- [x] sales 미인증 → 401 (step7)  // covered: internal/http/sales_test.go:TestSales_RequiresAuth
- [x] payments_repo 수단·일별 gross/refund/net 집계 (step7)  // covered: internal/repo/payments_repo_test.go:TestSalesSummary_GrossRefundNetSeparation
- [x] payments_repo 지점 필터 적용 (step7)  // covered: internal/repo/payments_repo_test.go:TestSalesSummary_BranchFilter
- [x] bulk-extend 지점 관리자 토큰 → 403 (step7)  // covered: internal/http/bulk_extend_test.go:TestBulkExtend_BranchAdminForbidden
- [x] bulk-extend Idempotency-Key 누락 → IDEMPOTENCY_KEY_REQUIRED (step7)  // covered: internal/http/bulk_extend_test.go:TestBulkExtend_RequiresIdempotencyKey
- [x] bulk-extend Idempotency-Key UUIDv4 아님 → 400 (step7)  // covered: internal/http/bulk_extend_test.go:TestBulkExtend_InvalidIdempotencyKey
- [x] bulk-extend days 0/91 → INVALID_EXTEND_DAYS (step7)  // covered: internal/http/bulk_extend_test.go:TestBulkExtend_InvalidExtendDays
- [x] bulk-extend +days 적용 (step7)  // covered: internal/http/bulk_extend_test.go:TestBulkExtend_AppliesPlusDays
- [x] bulk-extend 같은 키·같은 body 재호출 → 첫 응답 재반환 (step7)  // covered: internal/http/bulk_extend_test.go:TestBulkExtend_IdempotencyReplay
- [x] bulk-extend 같은 키·다른 body → IDEMPOTENCY_KEY_CONFLICT (step7)  // covered: internal/http/bulk_extend_test.go:TestBulkExtend_IdempotencyConflict
- [x] bulk-extend branch_id/type 필터 (step7)  // covered: internal/http/bulk_extend_test.go:TestBulkExtend_BranchFilter
- [x] bulk-extend EXCLUDE 충돌 → first_conflict_membership_id 응답 (step7)  // covered: internal/http/bulk_extend_test.go:TestBulkExtend_OverlapConflict
- [x] bulk-extend paused/예약 정지 pause_*도 +days 이동 (step7)  // covered: internal/repo/memberships_repo_test.go:TestBulkExtend_FutureScheduledPauseShifts
- [x] bulk-extend EXCLUDE 충돌 시 전체 롤백 (step7)  // covered: internal/repo/memberships_repo_test.go:TestBulkExtend_ConflictRollsBack
- [x] bulk-extend soft-deleted 회원 제외 (step7)  // covered: internal/repo/memberships_repo_test.go:TestBulkExtend_SkipsSoftDeletedMembers

## 자정 KST 배치

- [x] active 회원권 end_date 과거 → expired 전환 (step8)  // covered: internal/batch/batch_test.go:TestRunExpiry_ExpireActiveWhoseEndDateIsPast
- [x] active+end_date=오늘은 expired 전환하지 않음 (경계 보호) (step8)  // covered: internal/batch/batch_test.go:TestRunExpiry_DoesNotExpireActiveEndingToday
- [x] paused 회원권 pause_end_date 경과 → active 복귀 (step8)  // covered: internal/batch/batch_test.go:TestRunExpiry_PausedToActiveWhenPauseEnded
- [x] active 회원권 예약 정지 도래일 → paused 전환 (step8)  // covered: internal/batch/batch_test.go:TestRunExpiry_ActiveToPausedWhenScheduledPauseArrives
- [x] 자정 정리잡 — 24h 지난 idempotency_keys 삭제 (step8)  // covered: internal/batch/batch_test.go:TestRunExpiry_CleanupIdempotencyKeys
- [x] 자정 정리잡 — 15h 지난 revoked_refresh_tokens 삭제 (step8)  // covered: internal/batch/batch_test.go:TestRunExpiry_CleanupRevokedRefreshTokens
- [x] 자정 정리잡 — 1년 지난 admin_audit_logs 삭제 (step8)  // covered: internal/batch/batch_test.go:TestRunExpiry_CleanupAdminAuditLogs
- [x] cron 스케줄러 KST 등록/시작/정지 동작 (step8)  // covered: internal/batch/scheduler_test.go:TestScheduler_StartStopIdempotent

## 인프라·공통

- [x] CORS preflight 204 + 허용 헤더 (step2)  // covered: internal/http/middleware/cors_test.go:TestCORS_PreflightReturns204
- [x] CORS GET 응답에 헤더 부착 (step2)  // covered: internal/http/middleware/cors_test.go:TestCORS_GETAttachesHeadersAndCallsHandler
- [x] CORS 와일드카드 origin 거부 (step2)  // covered: internal/http/middleware/cors_test.go:TestCORS_RejectsWildcardOrigin
- [x] X-Request-ID 생성/전파 (step2)  // covered: internal/http/middleware/requestid_test.go:TestRequestID_GeneratesWhenAbsent
- [x] 클라가 보낸 UUID는 보존 (step2)  // covered: internal/http/middleware/requestid_test.go:TestRequestID_PassesThroughValidUUID
- [x] 비-UUID는 거부하고 새로 발급 (step2)  // covered: internal/http/middleware/requestid_test.go:TestRequestID_RejectsNonUUIDInput
- [x] panic → 500 INTERNAL (step2)  // covered: internal/http/middleware/recovery_test.go:TestRecovery_Returns500WithINTERNAL
- [x] 운영(prod) 응답에 panic value/stack 미노출 (step2)  // covered: internal/http/middleware/recovery_test.go:TestRecovery_ProdHidesPanicValueAndStack
- [x] dev 응답에 message만 노출, stack은 로그만 (step2)  // covered: internal/http/middleware/recovery_test.go:TestRecovery_DevExposesPanicMessageButNotStack
- [x] body size 1MB 초과 → 400 BODY_TOO_LARGE (step2)  // covered: internal/http/middleware/bodylimit_test.go:TestBodyLimit_RejectsByContentLength
- [x] HSTS — prod 헤더 추가 (step2)  // covered: internal/http/middleware/hsts_test.go:TestHSTS_Prod_AddsHeader
- [x] HSTS — dev 헤더 미부착 (step2)  // covered: internal/http/middleware/hsts_test.go:TestHSTS_Dev_OmitsHeader
- [x] rate limit 윈도우 초과 시 429 RATE_LIMITED (step2)  // covered: internal/http/middleware/ratelimit_test.go:TestRateLimit_BlocksOverMax
- [x] rate limit IP 단위 분리 (step2)  // covered: internal/http/middleware/ratelimit_test.go:TestRateLimit_TracksPerIP
- [x] 로그 필드 — request_id/admin_id/ip/method/path/status/duration_ms (step2)  // covered: internal/http/middleware/logger_test.go:TestLogger_EmitsRequiredFields
- [x] 로그에 PII(쿼리 phone·body password) 미포함 (step2)  // covered: internal/http/middleware/logger_test.go:TestLogger_OmitsPIIFromQueryAndBody
- [x] WithTx 40001 retry — 자동 재시도 (step2)  // covered: internal/repo/tx_test.go:TestWithTx_RetriesOnSerializationFailure
- [x] WithTx 40P01 deadlock retry (step2)  // covered: internal/repo/tx_test.go:TestWithTx_RetriesOnDeadlock
- [x] WithTx 3회 초과 실패 시 마지막 에러 (step2)  // covered: internal/repo/tx_test.go:TestWithTx_GivesUpAfter3Attempts
- [x] WithTx non-retryable 에러는 즉시 반환 (step2)  // covered: internal/repo/tx_test.go:TestWithTx_NonRetryableErrorReturnsImmediately
- [x] cursor 인코딩/디코딩 라운드 트립 (step5)  // covered: internal/http/cursor_test.go:TestCursorRoundTrip
- [x] cursor malformed → 400 (step5)  // covered: internal/http/cursor_test.go:TestDecodeCursor_RejectsMalformed
- [x] ParseLimit 기본/최대/오류 분기 (step5)  // covered: internal/http/cursor_test.go:TestParseLimit
- [x] DB pool은 SET TIME ZONE 'UTC' 적용 (step1+step2)  // covered: internal/repo/db_test.go:TestNewPool_AppliesUTC
- [x] admin_audit_logs 기록 미들웨어/헬퍼 (step2)  // covered: internal/audit/audit_test.go:TestAudit_Log_InsertsRow
- [x] healthz는 DB ping 포함 (step1+step2)  // covered: internal/http/health_test.go:TestHealthz_OK
- [x] healthz DB 다운이면 503 (step1+step2)  // covered: internal/http/health_test.go:TestHealthz_PoolDown

<!-- coverage: total 71.6%, internal/http 73.9%, internal/http/middleware 91.9%, internal/repo 73.6%, internal/auth 87.6%, internal/batch 77.5%, internal/apperr 87.9%, internal/cache 94.4% (2026-05-11). 참고선(핸들러 80%, 도메인 90%)에 일부 미달이나 ROADMAP "참고선·절대 기준 아님"에 따라 PASS. -->
