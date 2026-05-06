현재 blocked 상태인 phase·step을 찾아 사용자에게 사유와 해소 절차를 안내하라.

## 절차

1. `phases/index.json` 읽기. `status: blocked`인 phase가 있으면 그 안의 `phases/<dir>/index.json`을 읽어 첫 blocked step을 찾는다.
2. 해당 step의 `blocked_reason`·`blocked_at`을 출력.
3. blocked 사유에 따른 해소 가이드를 제시:
   - **도구 미설치** (예: `필수 도구 미설치: pnpm`) → 호스트에서 `pnpm`/`go`/`goose` 등을 설치하도록 안내. 설치 후 사용자가 직접 status를 `pending`으로 되돌리도록.
   - **API 키/시크릿 누락** → `.env` 파일에 키를 추가하도록 안내. 키 이름은 reason에 명시되어 있어야 한다.
   - **계약 변경 필요** → shared step을 새로 만들어 `docs/API.md`를 갱신하라고 안내. `/step-create` 사용 권유.
   - **기간 겹침/EXCLUDE 충돌** → 충돌 회원권 ID 또는 운영 절차 안내. 해결 후 retry.
4. 해소 절차 안내 마지막에 사용자가 status를 어떻게 되돌릴지 명시:
   ```
   해소 후:
   1. phases/<dir>/index.json에서 step의 "status"를 "pending"으로 변경.
   2. "blocked_reason" 키 제거.
   3. python3 scripts/execute.py <dir>로 재실행.
   ```

## 주의

- 어떤 파일도 수정하지 마라(읽기 전용).
- `status`를 직접 `pending`으로 바꾸지 마라 — 사용자가 사유를 확인하고 해소한 뒤 직접 변경.
- blocked가 없으면 "현재 blocked 상태인 step이 없습니다"라고 알려라.
