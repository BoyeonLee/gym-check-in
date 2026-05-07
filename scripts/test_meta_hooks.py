"""
session_start.py, statusline.py, stop_summary.py 단위 테스트.

세 hook 모두 stdout/stderr 출력만 하는 read-only hook이라 한 파일에 묶음.
"""

import io
import json
import os
import sys
from pathlib import Path
from unittest.mock import patch, MagicMock

import pytest

sys.path.insert(0, str(Path(__file__).parent / "hooks"))
import session_start as ss  # noqa: E402
import statusline as sl  # noqa: E402
import stop_summary as st  # noqa: E402


@pytest.fixture
def fake_root(tmp_path):
    """phases/, .claude/, CLAUDE.md를 갖춘 가짜 프로젝트 루트."""
    (tmp_path / ".claude").mkdir()
    (tmp_path / "CLAUDE.md").write_text("# Project")
    (tmp_path / "phases").mkdir()
    return tmp_path


def _write_phases(root: Path, phases: list[dict]):
    """phases/index.json + 각 phase의 index.json을 작성."""
    top = {"phases": [{"dir": p["dir"], "status": p.get("status", "pending")} for p in phases]}
    (root / "phases" / "index.json").write_text(json.dumps(top))
    for p in phases:
        d = root / "phases" / p["dir"]
        d.mkdir(exist_ok=True)
        (d / "index.json").write_text(json.dumps({"steps": p.get("steps", [])}))


# ===========================================================================
# session_start
# ===========================================================================

class TestSessionStart:
    def test_outputs_project_root_and_branch(self, fake_root, capsys, monkeypatch):
        monkeypatch.setenv("CLAUDE_PROJECT_DIR", str(fake_root))
        with patch.object(ss, "_git", return_value="main"):
            rc = ss.main()
        assert rc == 0
        out = capsys.readouterr().out
        assert "프로젝트 위치" in out
        assert str(fake_root) in out
        assert "branch: main" in out

    def test_outputs_worktree_when_inside_one(self, fake_root, capsys, monkeypatch):
        wt = fake_root / ".worktrees" / "backend"
        wt.mkdir(parents=True)
        monkeypatch.setenv("CLAUDE_PROJECT_DIR", str(wt))
        with patch.object(ss, "_git", return_value="feat/be"):
            rc = ss.main()
        out = capsys.readouterr().out
        assert "agent=backend" in out

    def test_phase_summary_with_pending(self, fake_root, capsys, monkeypatch):
        _write_phases(fake_root, [
            {
                "dir": "0-mvp",
                "status": "pending",
                "steps": [
                    {"step": 0, "name": "setup", "agent": "shared", "status": "completed"},
                    {"step": 1, "name": "core", "agent": "backend", "status": "pending"},
                ],
            }
        ])
        monkeypatch.setenv("CLAUDE_PROJECT_DIR", str(fake_root))
        with patch.object(ss, "_git", return_value="main"):
            rc = ss.main()
        out = capsys.readouterr().out
        assert "0-mvp" in out
        assert "core" in out
        assert "backend" in out

    def test_phase_summary_all_completed(self, fake_root, capsys, monkeypatch):
        _write_phases(fake_root, [
            {
                "dir": "0-mvp",
                "status": "completed",
                "steps": [
                    {"step": 0, "name": "setup", "agent": "shared", "status": "completed"},
                ],
            }
        ])
        monkeypatch.setenv("CLAUDE_PROJECT_DIR", str(fake_root))
        with patch.object(ss, "_git", return_value="main"):
            rc = ss.main()
        out = capsys.readouterr().out
        assert "0-mvp: completed (1/1)" in out

    def test_no_phases_doesnt_crash(self, fake_root, capsys, monkeypatch):
        # phases/index.json 없음
        monkeypatch.setenv("CLAUDE_PROJECT_DIR", str(fake_root))
        with patch.object(ss, "_git", return_value="main"):
            rc = ss.main()
        assert rc == 0


# ===========================================================================
# statusline
# ===========================================================================

class TestStatusline:
    @pytest.fixture(autouse=True)
    def _mock_dashboard(self, monkeypatch):
        # 환경별로 설치된 claude-dashboard 출력이 stdout 끝에 붙지 않도록 mock.
        monkeypatch.setattr(sl, "_dashboard_output", lambda payload: "")

    def test_branch_only_when_no_phases(self, fake_root, capsys, monkeypatch):
        monkeypatch.setenv("CLAUDE_PROJECT_DIR", str(fake_root))
        with patch.object(sl, "_git_branch", return_value="main"):
            rc = sl.main(stdin=io.StringIO(""))
        assert rc == 0
        out = capsys.readouterr().out.strip()
        assert out == "branch:main"

    def test_first_pending_step(self, fake_root, capsys, monkeypatch):
        _write_phases(fake_root, [
            {
                "dir": "1-db",
                "status": "pending",
                "steps": [
                    {"step": 0, "name": "schema", "agent": "backend", "status": "completed"},
                    {"step": 1, "name": "seed", "agent": "backend", "status": "pending"},
                ],
            }
        ])
        monkeypatch.setenv("CLAUDE_PROJECT_DIR", str(fake_root))
        with patch.object(sl, "_git_branch", return_value="feat/db"):
            rc = sl.main(stdin=io.StringIO(""))
        out = capsys.readouterr().out.strip()
        assert "phase:1-db" in out
        assert "step:1/1" in out
        assert "agent:be" in out
        assert "branch:feat/db" in out

    def test_all_completed_marker(self, fake_root, capsys, monkeypatch):
        _write_phases(fake_root, [
            {
                "dir": "0-mvp",
                "status": "completed",
                "steps": [
                    {"step": 0, "name": "x", "agent": "shared", "status": "completed"},
                ],
            }
        ])
        monkeypatch.setenv("CLAUDE_PROJECT_DIR", str(fake_root))
        with patch.object(sl, "_git_branch", return_value="main"):
            rc = sl.main(stdin=io.StringIO(""))
        out = capsys.readouterr().out.strip()
        assert "✓" in out

    def test_skips_completed_phase_to_next(self, fake_root, capsys, monkeypatch):
        """phase status='completed'는 건너뛰고 다음 진행 중 phase의 pending step을 표시."""
        _write_phases(fake_root, [
            {
                "dir": "phase1-db-init",
                "status": "completed",
                "steps": [
                    {"step": 1, "name": "migrations", "agent": "backend", "status": "completed"},
                    {"step": 2, "name": "seed", "agent": "backend", "status": "completed"},
                ],
            },
            {
                "dir": "phase2-backend-scaffold",
                "status": "pending",
                "steps": [
                    {"step": 1, "name": "scaffold", "agent": "backend", "status": "pending"},
                    {"step": 2, "name": "middleware", "agent": "backend", "status": "deferred"},
                ],
            },
        ])
        monkeypatch.setenv("CLAUDE_PROJECT_DIR", str(fake_root))
        with patch.object(sl, "_git_branch", return_value="main"):
            rc = sl.main(stdin=io.StringIO(""))
        out = capsys.readouterr().out.strip()
        assert "phase:phase2-backend-scaffold" in out
        assert "step:1/1" in out
        # phase1의 step:2/1✓이 표시되면 안 됨 (이전 버그)
        assert "phase:phase1-db-init" not in out

    def test_review_gate_shows_last_completed(self, fake_root, capsys, monkeypatch):
        """검토 게이트 상태: 일부 step만 completed, 나머지 deferred → 마지막 completed에 ✓."""
        _write_phases(fake_root, [
            {
                "dir": "phase2-backend-scaffold",
                "status": "pending",
                "steps": [
                    {"step": 1, "name": "scaffold", "agent": "backend", "status": "completed"},
                    {"step": 2, "name": "middleware", "agent": "backend", "status": "deferred"},
                    {"step": 3, "name": "auth", "agent": "backend", "status": "deferred"},
                ],
            },
        ])
        monkeypatch.setenv("CLAUDE_PROJECT_DIR", str(fake_root))
        with patch.object(sl, "_git_branch", return_value="main"):
            rc = sl.main(stdin=io.StringIO(""))
        out = capsys.readouterr().out.strip()
        assert "phase:phase2-backend-scaffold" in out
        # 마지막 completed step(step1)에 ✓
        assert "step:1/2✓" in out

    def test_all_phases_completed_shows_last_phase(self, fake_root, capsys, monkeypatch):
        """모든 phase가 completed면 마지막 phase의 마지막 step에 ✓."""
        _write_phases(fake_root, [
            {
                "dir": "phase1",
                "status": "completed",
                "steps": [{"step": 1, "name": "a", "agent": "backend", "status": "completed"}],
            },
            {
                "dir": "phase2",
                "status": "completed",
                "steps": [
                    {"step": 1, "name": "x", "agent": "backend", "status": "completed"},
                    {"step": 2, "name": "y", "agent": "backend", "status": "completed"},
                ],
            },
        ])
        monkeypatch.setenv("CLAUDE_PROJECT_DIR", str(fake_root))
        with patch.object(sl, "_git_branch", return_value="main"):
            rc = sl.main(stdin=io.StringIO(""))
        out = capsys.readouterr().out.strip()
        assert "phase:phase2" in out
        assert "step:2/1✓" in out


# ===========================================================================
# stop_summary
# ===========================================================================

class TestStopSummary:
    def test_silent_when_clean(self, capsys):
        with patch.object(st, "_run_git", return_value=""):
            rc = st.main()
        assert rc == 0
        captured = capsys.readouterr()
        # 변경 없으면 stdout/stderr 모두 비어야 함
        assert captured.err == ""
        assert captured.out == ""

    def test_outputs_when_status_present(self, capsys):
        def fake_git(args, cwd):
            if args[0] == "status":
                return " M backend/main.go\n"
            return ""
        with patch.object(st, "_run_git", side_effect=fake_git):
            rc = st.main()
        assert rc == 0
        err = capsys.readouterr().err
        assert "Stop hook" in err
        assert "main.go" in err

    def test_outputs_when_diff_stat_present(self, capsys):
        def fake_git(args, cwd):
            if args[0] == "diff":
                return " backend/main.go | 5 +++++\n"
            return ""
        with patch.object(st, "_run_git", side_effect=fake_git):
            rc = st.main()
        err = capsys.readouterr().err
        assert "Stop hook" in err
        assert "main.go" in err

    def test_no_crash_on_subprocess_failure(self, capsys, monkeypatch):
        """git이 없거나 repo가 아니어도 hook이 crash하지 않아야 함."""
        with patch.object(st, "_run_git", return_value=""):
            rc = st.main()
        assert rc == 0
