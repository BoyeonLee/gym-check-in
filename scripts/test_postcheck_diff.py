"""
postcheck_diff.py 단위 테스트.

PII·시크릿·ADR 외 import 검출을 검증.
"""

import io
import json
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).parent / "hooks"))
import postcheck_diff as hook  # noqa: E402


def _run(file_path: str) -> tuple[int, str]:
    payload = json.dumps({"tool_input": {"file_path": file_path}})
    stdin = io.StringIO(payload)
    captured = io.StringIO()
    real_stderr = sys.stderr
    sys.stderr = captured
    try:
        rc = hook.main(stdin=stdin)
    finally:
        sys.stderr = real_stderr
    return rc, captured.getvalue()


def _write(tmp_path, name, content):
    p = tmp_path / name
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(content, encoding="utf-8")
    return str(p)


# === PII 휴대폰 ===

class TestPiiPhone:
    def test_block_phone_in_backend_code(self, tmp_path):
        path = _write(tmp_path, "backend/main.go", '''
package main
const myPhone = "01012345678"
''')
        rc, stderr = _run(path)
        assert rc == 2
        assert "PII" in stderr
        assert "01012345678" in stderr

    def test_allow_phone_in_test(self, tmp_path):
        path = _write(tmp_path, "backend/main_test.go", '''
package main
const testPhone = "01012345678"
''')
        rc, _ = _run(path)
        assert rc == 0

    def test_allow_dummy_phone(self, tmp_path):
        path = _write(tmp_path, "backend/main.go", '''
const dummy = "00000000000"
const dummy2 = "01099999999"
''')
        rc, _ = _run(path)
        assert rc == 0


# === 시크릿 평문 ===

class TestSecrets:
    def test_block_jwt_secret_plain(self, tmp_path):
        path = _write(tmp_path, "backend/config.go", '''
JWT_ACCESS_SECRET = "supersecret123"
''')
        rc, stderr = _run(path)
        assert rc == 2
        assert "SECRET" in stderr

    def test_block_password_plain(self, tmp_path):
        path = _write(tmp_path, "backend/seed.go", '''
password = "MyPassword123"
''')
        rc, stderr = _run(path)
        assert rc == 2
        assert "SECRET" in stderr

    def test_block_bcrypt_hash_inline(self, tmp_path):
        path = _write(tmp_path, "backend/seed.go", '''
const seedHash = "$2a$12$abcdefghij1234567890ab"
''')
        rc, stderr = _run(path)
        assert rc == 2
        assert "bcrypt" in stderr.lower()


# === 로그 PII ===

class TestLogPii:
    def test_block_console_log_token(self, tmp_path):
        path = _write(tmp_path, "frontend/src/api.ts", '''
console.log("token", token);
''')
        rc, stderr = _run(path)
        assert rc == 2
        assert "LOG-PII" in stderr

    def test_block_slog_phone(self, tmp_path):
        path = _write(tmp_path, "backend/handler.go", '''
slog.Info("checkin", "phone", member.Phone)
''')
        rc, stderr = _run(path)
        assert rc == 2
        assert "LOG-PII" in stderr

    def test_block_slog_birth_date(self, tmp_path):
        path = _write(tmp_path, "backend/handler.go", '''
slog.Debug("member", "birth_date", m.BirthDate)
''')
        rc, stderr = _run(path)
        assert rc == 2

    def test_allow_normal_log(self, tmp_path):
        path = _write(tmp_path, "backend/handler.go", '''
slog.Info("checkin success", "member_id", id)
''')
        rc, _ = _run(path)
        assert rc == 0


# === Frontend ADR 외 import ===

class TestFrontendImport:
    def test_block_axios(self, tmp_path):
        path = _write(tmp_path, "frontend/src/api.ts", '''
import axios from "axios";
''')
        rc, stderr = _run(path)
        assert rc == 2
        assert "ADR-EXT" in stderr
        assert "axios" in stderr

    def test_block_anthropic_sdk(self, tmp_path):
        path = _write(tmp_path, "frontend/src/voice.ts", '''
import Anthropic from "@anthropic-ai/sdk";
''')
        rc, stderr = _run(path)
        assert rc == 2

    def test_block_firebase(self, tmp_path):
        path = _write(tmp_path, "frontend/src/init.ts", '''
import firebase from "firebase/app";
''')
        rc, _ = _run(path)
        assert rc == 2

    def test_allow_react_router(self, tmp_path):
        path = _write(tmp_path, "frontend/src/App.tsx", '''
import { BrowserRouter } from "react-router-dom";
''')
        rc, _ = _run(path)
        assert rc == 0

    def test_allow_relative_import(self, tmp_path):
        path = _write(tmp_path, "frontend/src/api.ts", '''
import { foo } from "./util";
import { bar } from "@/components/Button";
''')
        rc, _ = _run(path)
        assert rc == 0


# === Backend Go ADR 외 import ===

class TestBackendImport:
    def test_block_logrus(self, tmp_path):
        path = _write(tmp_path, "backend/main.go", '''
package main
import (
    "github.com/sirupsen/logrus"
    "fmt"
)
''')
        rc, stderr = _run(path)
        assert rc == 2
        assert "ADR-EXT" in stderr
        assert "logrus" in stderr

    def test_allow_gin(self, tmp_path):
        path = _write(tmp_path, "backend/main.go", '''
package main
import (
    "github.com/gin-gonic/gin"
    "github.com/jackc/pgx/v5/pgxpool"
)
''')
        rc, _ = _run(path)
        assert rc == 0

    def test_allow_std_lib(self, tmp_path):
        path = _write(tmp_path, "backend/main.go", '''
package main
import (
    "encoding/json"
    "fmt"
    "log/slog"
)
''')
        rc, _ = _run(path)
        assert rc == 0


# === 가이드라인 문서(.md) 정책 완화 ===

class TestDocFiles:
    """
    .md/.markdown 가이드라인 문서는 형식 설명 패턴(휴대폰 번호 예시·bcrypt prefix)을 허용한다.
    그러나 진짜 시크릿(JWT 키·password 평문)은 .md에서도 그대로 차단한다.
    """

    def test_allow_phone_in_md(self, tmp_path):
        path = _write(tmp_path, "phases/phase2/step5.md", '''
# Step 5
관리자 응답은 phone(예: 01012345678 같은 11자리 숫자)을 풀 노출한다.
''')
        rc, _ = _run(path)
        assert rc == 0

    def test_allow_bcrypt_prefix_in_md(self, tmp_path):
        path = _write(tmp_path, "phases/phase2/step1.md", '''
# Step 1
출력은 $2a$12$ 또는 $2b$12$로 시작하는 60자 bcrypt 해시.
''')
        rc, _ = _run(path)
        assert rc == 0

    def test_block_jwt_secret_in_md(self, tmp_path):
        path = _write(tmp_path, "phases/phase2/step3.md", '''
# Step 3
export JWT_ACCESS_SECRET="supersecret123"
''')
        rc, stderr = _run(path)
        assert rc == 2
        assert "SECRET" in stderr
        assert "JWT" in stderr

    def test_block_password_plain_in_md(self, tmp_path):
        path = _write(tmp_path, "docs/example.md", '''
sample config:
password = "MyPassword123"
''')
        rc, stderr = _run(path)
        assert rc == 2
        assert "SECRET" in stderr

    def test_allow_phone_in_markdown_extension(self, tmp_path):
        path = _write(tmp_path, "phases/x.markdown", '''
phone 예시: 01012345678
''')
        rc, _ = _run(path)
        assert rc == 0

    def test_phone_still_blocked_in_go_code(self, tmp_path):
        # 이전 정책 유지 확인 (regression)
        path = _write(tmp_path, "backend/seed.go", '''
package main
const myPhone = "01012345678"
''')
        rc, _ = _run(path)
        assert rc == 2

    def test_bcrypt_still_blocked_in_go_code(self, tmp_path):
        # 이전 정책 유지 확인 (regression)
        path = _write(tmp_path, "backend/seed.go", '''
const seedHash = "$2a$12$abcdefghij1234567890ab"
''')
        rc, _ = _run(path)
        assert rc == 2


# === edge cases ===

def test_no_file_path():
    stdin = io.StringIO(json.dumps({"tool_input": {}}))
    rc = hook.main(stdin=stdin)
    assert rc == 0


def test_nonexistent_file():
    stdin = io.StringIO(json.dumps({"tool_input": {"file_path": "/nonexistent/foo.go"}}))
    rc = hook.main(stdin=stdin)
    assert rc == 0


def test_empty_payload():
    stdin = io.StringIO("")
    rc = hook.main(stdin=stdin)
    assert rc == 0
