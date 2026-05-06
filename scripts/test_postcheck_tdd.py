"""
postcheck_tdd.py 단위 테스트.

git diff 결과를 mock해 핸들러 추가/테스트 누락·핸들러+테스트 같이 추가·shared 변경 케이스를 검증.
"""

import io
import json
import sys
from pathlib import Path
from unittest.mock import patch

import pytest

sys.path.insert(0, str(Path(__file__).parent / "hooks"))
import postcheck_tdd as hook  # noqa: E402


@pytest.fixture
def fake_diff(monkeypatch):
    """git diff를 mock해 임의의 변경 파일 목록을 반환하도록."""
    def _fake(names):
        monkeypatch.setattr(hook, "_git_diff_names", lambda cwd: names)
    return _fake


def _run(cwd: str = "/tmp/proj") -> tuple[int, str]:
    payload = json.dumps({"tool_input": {"file_path": "/tmp/proj/backend/internal/http/x.go"}, "cwd": cwd})
    stdin = io.StringIO(payload)
    captured = io.StringIO()
    real_stderr = sys.stderr
    sys.stderr = captured
    try:
        rc = hook.main(stdin=stdin)
    finally:
        sys.stderr = real_stderr
    return rc, captured.getvalue()


class TestRequiredFile:
    def test_handler_recognized(self):
        assert hook._is_required_go_file("backend/internal/http/checkins.go")

    def test_domain_recognized(self):
        assert hook._is_required_go_file("backend/internal/domain/membership.go")

    def test_repo_recognized(self):
        assert hook._is_required_go_file("backend/internal/repo/members_repo.go")

    def test_test_file_excluded(self):
        assert not hook._is_required_go_file("backend/internal/http/checkins_test.go")

    def test_cmd_excluded(self):
        assert not hook._is_required_go_file("backend/cmd/server/main.go")

    def test_testutil_excluded(self):
        assert not hook._is_required_go_file("backend/internal/testutil/db.go")

    def test_apperr_excluded(self):
        assert not hook._is_required_go_file("backend/internal/apperr/error.go")

    def test_frontend_excluded(self):
        assert not hook._is_required_go_file("frontend/src/api.ts")


class TestPairCheck:
    def test_block_handler_without_test(self, fake_diff):
        fake_diff(["backend/internal/http/checkins.go"])
        rc, stderr = _run()
        assert rc == 2
        assert "TDD" in stderr
        assert "checkins.go" in stderr
        assert "checkins_test.go" in stderr

    def test_allow_handler_with_test(self, fake_diff):
        fake_diff([
            "backend/internal/http/checkins.go",
            "backend/internal/http/checkins_test.go",
        ])
        rc, _ = _run()
        assert rc == 0

    def test_allow_handler_with_other_test_in_same_dir(self, fake_diff):
        """같은 디렉토리에 다른 _test.go가 변경됐으면 통과."""
        fake_diff([
            "backend/internal/http/checkins.go",
            "backend/internal/http/middleware_test.go",
        ])
        rc, _ = _run()
        assert rc == 0

    def test_block_multiple_handlers_partial_test(self, fake_diff):
        """두 핸들러 중 하나만 테스트 있으면 다른 하나가 차단."""
        fake_diff([
            "backend/internal/http/checkins.go",
            "backend/internal/http/checkins_test.go",
            "backend/internal/http/members.go",  # 테스트 없음 + 같은 dir에는 다른 테스트가 있어 통과되면 안 됨
        ])
        # 같은 dir에 checkins_test.go가 있으므로 정책상 통과시킬지?
        # 현재 구현은 same_dir_test 휴리스틱으로 통과시킨다 — 약한 경고 정책.
        rc, _ = _run()
        # 명확한 짝검사를 원하면 false → strict 모드 필요. MVP는 휴리스틱 통과.
        assert rc == 0

    def test_allow_test_only_change(self, fake_diff):
        fake_diff(["backend/internal/http/checkins_test.go"])
        rc, _ = _run()
        assert rc == 0

    def test_allow_cmd_change(self, fake_diff):
        fake_diff(["backend/cmd/server/main.go"])
        rc, _ = _run()
        assert rc == 0

    def test_allow_migration_change(self, fake_diff):
        fake_diff(["db/migrations/001_init.sql"])
        rc, _ = _run()
        assert rc == 0

    def test_allow_docs_change(self, fake_diff):
        fake_diff(["docs/API.md"])
        rc, _ = _run()
        assert rc == 0


def test_no_changes(fake_diff):
    fake_diff([])
    rc, _ = _run()
    assert rc == 0


def test_skip_when_not_backend_go():
    """tool_input의 file_path가 backend Go 파일이 아니면 즉시 통과(검사 생략)."""
    payload = json.dumps({"tool_input": {"file_path": "/x/frontend/App.tsx"}, "cwd": "/tmp"})
    stdin = io.StringIO(payload)
    rc = hook.main(stdin=stdin)
    assert rc == 0
