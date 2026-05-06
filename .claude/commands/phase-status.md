전체 phase 진행 상황을 한눈에 보여라.

## 절차

1. `phases/index.json`을 읽어 phase 디렉토리 목록 확보.
2. 각 phase의 `phases/<dir>/index.json`을 읽어 다음을 표 형태로 출력:
   - phase name (`dir`)
   - 전체 status (`pending`/`completed`/`blocked`/`error`)
   - 완료/전체 step 카운트(예: 3/8)
   - 첫 pending step의 `name`·`agent` (있으면)
   - blocked step이 있으면 `blocked_reason`
   - error step이 있으면 `error_message`
3. 카운트 요약을 마지막 줄에 표시: `Total: <N> phases / <X> pending / <Y> completed / <Z> blocked / <W> error`.

## 출력 형식 예시

```
Phase                  | Status     | Steps  | 다음 작업                         | 비고
---------------------- | ---------- | ------ | -------------------------------- | ----
0-bootstrap            | completed  | 3/3    | -                                | 
1-db-schema            | completed  | 5/5    | -                                |
2-checkin-api          | pending    | 2/4    | step3 [backend] memberships-be   |
3-checkin-fe           | blocked    | 0/2    | step1 [frontend] kiosk-search    | blocked: SpeechRecognition 미설치

Total: 4 phases / 1 pending / 2 completed / 1 blocked / 0 error
```

읽기 전용 작업이다. 어떤 파일도 수정하지 마라.
