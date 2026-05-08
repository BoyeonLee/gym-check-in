-- +goose Up

-- idempotency_keys.response_body 컬럼을 jsonb → bytea로 전환.
--
-- 사유: HTTP 핸들러가 idempotent 재시도 시 첫 응답을 byte-for-byte 그대로
-- 복원해야 한다(backend/CLAUDE.md "응답 byte-for-byte 보존"). PostgreSQL의
-- jsonb 타입은 입력을 정규화하면서 공백을 재배치하므로(`{"a":1}` →
-- `{"a": 1}`) 원문 보존이 깨진다. bytea로 바꾸면 핸들러가 발급한 바이트가
-- 그대로 round-trip된다.
--
-- 데이터 보존: 기존 jsonb 값은 USING 절로 ::text → ::bytea 캐스팅. JSON
-- 텍스트의 UTF-8 인코딩이 그대로 보존된다(jsonb 자체가 UTF-8 정규형으로
-- 저장돼 있어 캐스팅 결과는 잘 정의된다).
alter table idempotency_keys
  alter column response_body type bytea
  using response_body::text::bytea;

-- +goose Down

-- 역방향: bytea → jsonb. 저장된 값이 유효한 JSON일 때만 round-trip 가능.
-- 모든 핸들러가 JSON을 직렬화해 저장하므로 이 가정은 안전하다(테스트 픽스처
-- 포함).
alter table idempotency_keys
  alter column response_body type jsonb
  using convert_from(response_body, 'UTF8')::jsonb;
