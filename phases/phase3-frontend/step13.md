---
agent: frontend
---

# Step 13: 관리자 — 관리자 CRUD + 비번 리셋 (전역 전용)

## 목표

전역 관리자가 다른 관리자 계정을 관리하는 화면. 비번 리셋의 임시 비번 1회 표시가 보안적으로 가장 민감.

이 step이 끝나면:
- `/admin/admins`: 목록(deleted_at IS NULL).
- 관리자 생성: username·password·role·branch_id. 백엔드가 must_change_password=true + temp_password_expires_at=now()+24h 자동.
- 관리자 수정: username·role·branch_id 변경 (password/잠금 컬럼은 변경 불가). 본인 행에서 role/branch_id 편집 비활성.
- 관리자 삭제: soft delete. 본인 차단 (CANNOT_DELETE_SELF 409 fallback).
- 비번 리셋: 응답의 임시 비번을 화면에 1회 표시(복사 버튼) + expires_at 안내. localStorage·콘솔·로그 어디에도 남기지 않는다.

## 읽어야 할 파일

- `frontend/CLAUDE.md` — Admins 페이지 정책, 본인 row 편집 비활성, 임시 비번 1회 표시
- `backend/CLAUDE.md` — POST /api/admins, PATCH /api/admins/:id, DELETE, /reset-password
- `docs/API.md` — 요청·응답·에러 코드
- step 3 (apiFetch), step 4 (AuthContext: 현재 admin id)

## 작업

### 1. `src/api/admins.ts`

```ts
export interface Admin {
  id: number
  username: string
  role: 'global' | 'branch'
  branch_id: number | null
  branch_name: string | null
  must_change_password: boolean
  last_login_at: string | null
  failed_login_count: number
  locked_until: string | null
  created_at: string
}

export async function listAdmins() {
  return apiFetch<{ items: Admin[] }>('/api/admins')
}
export async function createAdmin(body: { username: string; password: string; role: 'global' | 'branch'; branch_id?: number }) {
  return apiFetch<Admin>('/api/admins', { method: 'POST', body })
}
export async function updateAdmin(id: number, body: { username?: string; role?: 'global' | 'branch'; branch_id?: number | null }) {
  return apiFetch<Admin>(`/api/admins/${id}`, { method: 'PATCH', body })
}
export async function deleteAdmin(id: number) {
  return apiFetch(`/api/admins/${id}`, { method: 'DELETE' })
}
export interface ResetPasswordResponse {
  temp_password: string
  expires_at: string  // KST +09:00
}
export async function resetAdminPassword(id: number) {
  return apiFetch<ResetPasswordResponse>(`/api/admins/${id}/reset-password`, { method: 'POST' })
}
```

### 2. `src/pages/admin/Admins/List.tsx`

라우트 가드: 전역 전용.

상태:
- `useQuery(['admins'], listAdmins)`.
- 현재 admin id: AuthContext에서. **단 AuthContext에 admin_id 필드가 없을 가능성이 있으므로 step 4에서 username으로 매칭하거나 백엔드 응답에 id가 있으면 활용**. 가장 안전한 방법: `username === auth.username`으로 자기 행 식별.

UI:
- 표: username · role · branch · last_login_at · 잠금 상태 · 액션(수정/삭제/비번 리셋).
- 본인 행은 role/branch_id 편집 컨트롤 비활성. 삭제 버튼도 비활성("본인 계정은 삭제 불가").

### 3. `src/pages/admin/Admins/Edit.tsx` (등록·수정 겸용 모달)

상태:
- `username`(1~50자 unique)
- `password`(등록 시만 — 강도 검증)
- `role`('global' | 'branch')
- `branch_id`(role='branch'면 필수, 'global'이면 null)

처리:
- 등록: POST → 성공 시 invalidate + 닫기 + 토스트.
- 수정: PATCH → 성공 시 invalidate + 닫기. body에는 변경된 필드만.
- 본인 행 수정 시 role·branch_id 컨트롤 비활성(서버가 `CANNOT_MODIFY_SELF_ROLE` 409 반환하지만 클라도 1차 방어).
- branch_id 변경 시 안내: "해당 관리자가 자동 로그아웃됩니다(refresh 토큰 무효화)".
- 실패:
  - `USERNAME_DUPLICATE` 409: 인라인.
  - `WEAK_PASSWORD` 400: 인라인 강도 가이드.
  - `CANNOT_MODIFY_SELF_ROLE` 409: 안내 + 컨트롤 비활성화.

### 4. `src/pages/admin/Admins/ResetPasswordDialog.tsx`

confirm 모달:
- "X님의 비밀번호를 재설정합니다. 24시간 안에 첫 로그인 후 본인이 직접 변경해야 합니다." 확인 버튼.

처리:
- 본인 행에서는 진입 차단 (서버 `CANNOT_RESET_SELF` 409).
- 호출 → 응답 받으면 모달을 결과 화면으로 전환:
  - 큰 박스에 `temp_password` 표시 + 복사 버튼.
  - "유효 기한: {expires_at}" KST 표시.
  - "이 비밀번호는 다시 표시되지 않습니다. 안전한 방법으로 본인에게 전달하세요." 안내.
- 모달 닫기 후 메모리에서 즉시 폐기 (`setTempPassword(null)`).
- **절대 금지**: localStorage·sessionStorage·console.log·에러 메시지에 임시 비번 저장/노출.

복사 버튼:
- `navigator.clipboard.writeText(temp_password)` + 토스트 ("클립보드에 복사됨"). 클립보드는 30초 후 자동 해제(`setTimeout`으로 빈 문자열 덮어쓰기 — 베스트 에포트).

### 5. 컴포넌트 테스트

- `Admins/List.test.tsx`: 목록 렌더, 본인 행 편집/삭제 비활성, 잠금 상태 표시.
- `Admins/Edit.test.tsx`:
  - 등록: 강도 검증 실패 → 비활성, 성공 → invalidate.
  - 수정: 본인 행에서 role/branch_id 비활성.
  - branch_id 변경 시 안내 토스트.
  - USERNAME_DUPLICATE 인라인.
- `Admins/ResetPasswordDialog.test.tsx`:
  - 본인 행 → 모달 진입 차단.
  - 호출 → 응답의 temp_password 표시 + 복사 버튼.
  - 닫기 후 memory에 잔여 없음 (rerender 시 null).
  - 절대 localStorage에 저장되지 않음(spy로 검증).

## 핵심 규칙

- **본인 보호**: 본인 role/branch_id 편집·본인 삭제·본인 비번 리셋 클라에서 차단. 서버가 409로 백업.
- **임시 비번은 메모리 전용**: state로만 보유, 모달 닫는 순간 폐기. localStorage·sessionStorage·console·에러 메시지 모두 금지.
- **비번 리셋 후 자동 로그아웃**: 대상 사용자가 다음 호출에서 `iat < password_updated_at` 검증으로 401. 별도 처리 필요 없지만 안내는 표시.
- **branch_id 변경 → 토큰 무효화**: 사용자에게 명확히 안내.
- **USERNAME_DUPLICATE는 soft-deleted 미포함**: 백엔드가 deleted_at IS NULL만 보므로 동일 username 재사용 가능(삭제 후).

## Acceptance Criteria

```bash
cd frontend && pnpm lint && pnpm build && pnpm test
```

수동:
- 신규 지점 관리자 생성 → 임시 비번 없이 username/password 직접 지정.
- 그 사용자로 로그인 → must_change_password 강제 → 비번 변경 후 정상 사용.
- 다른 관리자 row에서 "비번 리셋" → 임시 비번 1회 표시 + 24h 만료 안내. localStorage spy로 임시 비번 미저장 검증.
- 본인 행에서 role/branch_id/삭제/리셋 모두 비활성.
- branch_id 변경 → 해당 사용자 다음 요청에서 401 자동 로그아웃.

## 검증 절차

1. AC.
2. **`code-reviewer` 호출 (필수 — 보안 영역).** 임시 비번 노출 경로 검증.
3. step13 status 갱신.

## 금지사항

- **임시 비번을 localStorage·sessionStorage·콘솔에 절대 저장 금지.**
- **임시 비번을 로그·에러 메시지·toast 외 영역에 노출 금지.**
- 본인 row 편집/삭제/리셋 컨트롤을 노출 금지.
- `password_hash`·`failed_login_count`·`locked_until`을 PATCH body에 포함 금지.
- 임시 비번을 URL query/path에 포함 금지(브라우저 history 영구 저장).
- 클립보드에 임시 비번을 30초 이상 유지하지 마라(베스트 에포트 자동 클리어).
