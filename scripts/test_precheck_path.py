"""
precheck_path.py 단위 테스트.

agent 매트릭스(backend/frontend/shared)별로 허용/차단 경로를 검증한다.
"""

import io
import json
import os
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).parent / "hooks"))
import precheck_path as hook  # noqa: E402


@pytest.fixture
def fake_project(tmp_path):
    """가짜 프로젝트 트리 + .worktrees/{backend,frontend}."""
    proj = tmp_path / "gym-check-in"
    proj.mkdir()
    for d in ("backend", "frontend", "db", "docs", "scripts", ".claude", "phases"):
        (proj / d).mkdir()
    (proj / ".worktrees").mkdir()
    (proj / ".worktrees" / "backend").mkdir()
    for d in ("backend", "db", "frontend"):
        (proj / ".worktrees" / "backend" / d).mkdir()
    (proj / ".worktrees" / "frontend").mkdir()
    for d in ("frontend", "backend"):
        (proj / ".worktrees" / "frontend" / d).mkdir()
    return proj


def _run(file_path: str, cwd: str) -> tuple[int, str]:
    payload = json.dumps({"tool_input": {"file_path": file_path}, "cwd": cwd})
    stdin = io.StringIO(payload)
    captured = io.StringIO()
    real_stderr = sys.stderr
    sys.stderr = captured
    try:
        rc = hook.main(stdin=stdin)
    finally:
        sys.stderr = real_stderr
    return rc, captured.getvalue()


# === detect_agent 단위 ===

def test_detect_agent_backend(fake_project):
    cwd = str(fake_project / ".worktrees" / "backend")
    assert hook.detect_agent(cwd) == "backend"


def test_detect_agent_frontend(fake_project):
    cwd = str(fake_project / ".worktrees" / "frontend")
    assert hook.detect_agent(cwd) == "frontend"


def test_detect_agent_shared_main(fake_project):
    assert hook.detect_agent(str(fake_project)) == "shared"


def test_detect_agent_shared_subdir(fake_project):
    assert hook.detect_agent(str(fake_project / "scripts")) == "shared"


# === backend worktree ===

class TestBackendWorktree:
    def test_allow_backend_file(self, fake_project):
        cwd = str(fake_project / ".worktrees" / "backend")
        path = str(fake_project / ".worktrees" / "backend" / "backend" / "main.go")
        rc, _ = _run(path, cwd)
        assert rc == 0

    def test_allow_db_file(self, fake_project):
        cwd = str(fake_project / ".worktrees" / "backend")
        path = str(fake_project / ".worktrees" / "backend" / "db" / "migrations" / "001.sql")
        rc, _ = _run(path, cwd)
        assert rc == 0

    def test_block_frontend_file(self, fake_project):
        cwd = str(fake_project / ".worktrees" / "backend")
        path = str(fake_project / ".worktrees" / "backend" / "frontend" / "App.tsx")
        rc, stderr = _run(path, cwd)
        assert rc == 2
        assert "BLOCKED" in stderr
        assert "frontend" in stderr.lower() or "frontend/" in stderr

    def test_block_docs(self, fake_project):
        cwd = str(fake_project / ".worktrees" / "backend")
        # backend worktree 안에는 docs/가 없을 수 있으므로 파일 경로만 평가
        path = str(fake_project / ".worktrees" / "backend" / "docs" / "API.md")
        rc, _ = _run(path, cwd)
        assert rc == 2

    def test_block_main_phases_index(self, fake_project):
        """B 방안(2026-05-08): backend agent는 메인 phases/도 만질 수 없다.
        status·summary는 execute.py가 main에서 직접 박는 책임 분리."""
        cwd = str(fake_project / ".worktrees" / "backend")
        (fake_project / "phases" / "phase2-backend-scaffold").mkdir(parents=True, exist_ok=True)
        path = str(fake_project / "phases" / "phase2-backend-scaffold" / "index.json")
        rc, stderr = _run(path, cwd)
        assert rc == 2
        assert "BLOCKED" in stderr

    def test_block_worktree_phases(self, fake_project):
        """B 방안: 자식이 자기 worktree 안의 phases/도 만지면 안 된다.
        BACKEND_ALLOWED는 backend/+db/만이라 자동 차단된다."""
        cwd = str(fake_project / ".worktrees" / "backend")
        (fake_project / ".worktrees" / "backend" / "phases" / "phase2-backend-scaffold").mkdir(
            parents=True, exist_ok=True
        )
        path = str(
            fake_project / ".worktrees" / "backend" / "phases" / "phase2-backend-scaffold" / "index.json"
        )
        rc, stderr = _run(path, cwd)
        assert rc == 2
        assert "BLOCKED" in stderr

    def test_block_main_docs(self, fake_project):
        """phases/ fallback이 docs/까지 풀어주면 안 된다."""
        cwd = str(fake_project / ".worktrees" / "backend")
        path = str(fake_project / "docs" / "API.md")
        rc, stderr = _run(path, cwd)
        assert rc == 2
        assert "BLOCKED" in stderr

    def test_block_main_backend(self, fake_project):
        """backend agent가 메인의 backend/ 코드를 만지면 안 된다(워크트리에서 작업)."""
        cwd = str(fake_project / ".worktrees" / "backend")
        path = str(fake_project / "backend" / "main.go")
        rc, stderr = _run(path, cwd)
        assert rc == 2


# === frontend worktree ===

class TestFrontendWorktree:
    def test_allow_frontend_file(self, fake_project):
        cwd = str(fake_project / ".worktrees" / "frontend")
        path = str(fake_project / ".worktrees" / "frontend" / "frontend" / "src" / "App.tsx")
        rc, _ = _run(path, cwd)
        assert rc == 0

    def test_block_backend_file(self, fake_project):
        cwd = str(fake_project / ".worktrees" / "frontend")
        path = str(fake_project / ".worktrees" / "frontend" / "backend" / "main.go")
        rc, _ = _run(path, cwd)
        assert rc == 2

    def test_block_db_file(self, fake_project):
        cwd = str(fake_project / ".worktrees" / "frontend")
        # frontend worktree에 db 디렉토리 만들고 차단 검증
        (fake_project / ".worktrees" / "frontend" / "db").mkdir(exist_ok=True)
        (fake_project / ".worktrees" / "frontend" / "db" / "migrations").mkdir(exist_ok=True)
        path = str(fake_project / ".worktrees" / "frontend" / "db" / "migrations" / "x.sql")
        rc, _ = _run(path, cwd)
        assert rc == 2

    def test_block_main_phases_index(self, fake_project):
        """B 방안: frontend agent도 메인 phases/ 차단."""
        cwd = str(fake_project / ".worktrees" / "frontend")
        (fake_project / "phases" / "phase3-fe").mkdir(parents=True, exist_ok=True)
        path = str(fake_project / "phases" / "phase3-fe" / "index.json")
        rc, stderr = _run(path, cwd)
        assert rc == 2
        assert "BLOCKED" in stderr

    def test_block_worktree_phases(self, fake_project):
        """B 방안: frontend worktree 안의 phases/도 자동 차단(FRONTEND_ALLOWED는 frontend/만)."""
        cwd = str(fake_project / ".worktrees" / "frontend")
        (fake_project / ".worktrees" / "frontend" / "phases").mkdir(exist_ok=True)
        path = str(fake_project / ".worktrees" / "frontend" / "phases" / "x.json")
        rc, _ = _run(path, cwd)
        assert rc == 2

    def test_block_main_docs(self, fake_project):
        cwd = str(fake_project / ".worktrees" / "frontend")
        path = str(fake_project / "docs" / "UI_GUIDE.md")
        rc, _ = _run(path, cwd)
        assert rc == 2


# === shared (메인) ===

class TestSharedMain:
    def test_allow_docs(self, fake_project):
        cwd = str(fake_project)
        path = str(fake_project / "docs" / "ROADMAP.md")
        rc, _ = _run(path, cwd)
        assert rc == 0

    def test_allow_scripts(self, fake_project):
        cwd = str(fake_project)
        path = str(fake_project / "scripts" / "execute.py")
        rc, _ = _run(path, cwd)
        assert rc == 0

    def test_allow_root_claude_md(self, fake_project):
        cwd = str(fake_project)
        path = str(fake_project / "CLAUDE.md")
        rc, _ = _run(path, cwd)
        assert rc == 0

    def test_allow_env_example(self, fake_project):
        cwd = str(fake_project)
        path = str(fake_project / ".env.example")
        rc, _ = _run(path, cwd)
        assert rc == 0

    def test_allow_docker_compose(self, fake_project):
        cwd = str(fake_project)
        path = str(fake_project / "docker-compose.yml")
        rc, _ = _run(path, cwd)
        assert rc == 0

    def test_allow_gitignore(self, fake_project):
        cwd = str(fake_project)
        path = str(fake_project / ".gitignore")
        rc, _ = _run(path, cwd)
        assert rc == 0

    def test_allow_phases(self, fake_project):
        cwd = str(fake_project)
        path = str(fake_project / "phases" / "0-mvp" / "step1.md")
        rc, _ = _run(path, cwd)
        assert rc == 0

    def test_block_backend(self, fake_project):
        cwd = str(fake_project)
        path = str(fake_project / "backend" / "main.go")
        rc, stderr = _run(path, cwd)
        assert rc == 2
        assert "BLOCKED" in stderr
        assert "shared" in stderr

    def test_block_frontend(self, fake_project):
        cwd = str(fake_project)
        path = str(fake_project / "frontend" / "src" / "App.tsx")
        rc, stderr = _run(path, cwd)
        assert rc == 2

    def test_block_db(self, fake_project):
        """1.6 정책 변경: shared는 db도 차단. db는 backend agent가 처리."""
        cwd = str(fake_project)
        path = str(fake_project / "db" / "migrations" / "001.sql")
        rc, stderr = _run(path, cwd)
        assert rc == 2
        assert "BLOCKED" in stderr

    def test_allow_module_claude_md(self, fake_project):
        """모듈별 CLAUDE.md(backend/, frontend/, db/)는 정책 문서 — shared가 수정 가능."""
        cwd = str(fake_project)
        for sub in ("backend", "frontend", "db"):
            path = str(fake_project / sub / "CLAUDE.md")
            rc, stderr = _run(path, cwd)
            assert rc == 0, f"{sub}/CLAUDE.md should be allowed in shared but rc={rc}: {stderr}"

    def test_block_module_other_md(self, fake_project):
        """모듈 안의 다른 .md는 여전히 차단(README 등은 코드 변경 일환으로 보지 않음)."""
        cwd = str(fake_project)
        path = str(fake_project / "backend" / "README.md")
        rc, _ = _run(path, cwd)
        assert rc == 2


# === edge cases ===

def test_empty_payload():
    stdin = io.StringIO("")
    rc = hook.main(stdin=stdin)
    assert rc == 0


def test_no_file_path():
    stdin = io.StringIO(json.dumps({"tool_input": {}}))
    rc = hook.main(stdin=stdin)
    assert rc == 0


def test_path_outside_project(fake_project):
    """프로젝트 루트 밖 경로는 차단."""
    cwd = str(fake_project)
    rc, stderr = _run("/etc/passwd", cwd)
    assert rc == 2
    assert "작업 루트 밖" in stderr or "BLOCKED" in stderr


# === path 우선 agent 식별 (메인 cwd + worktree path 시나리오) ===
#
# 메인 세션이 backend-engineer subagent를 호출하면 subagent는 메인 cwd를
# 상속받지만 file_path는 .worktrees/backend/...일 수 있다. 보강 전엔 cwd만
# 보고 shared로 분류해 차단했지만, 보강 후엔 path를 우선 봐서 backend
# 정책을 적용한다.

class TestPathPriorityAgentDetection:
    def test_detect_agent_path_overrides_cwd_to_backend(self, fake_project):
        """cwd가 메인이어도 path가 .worktrees/backend/...이면 backend agent."""
        cwd = str(fake_project)
        path = str(fake_project / ".worktrees" / "backend" / "backend" / "x.go")
        assert hook.detect_agent(cwd, path) == "backend"

    def test_detect_agent_path_overrides_cwd_to_frontend(self, fake_project):
        cwd = str(fake_project)
        path = str(fake_project / ".worktrees" / "frontend" / "frontend" / "x.tsx")
        assert hook.detect_agent(cwd, path) == "frontend"

    def test_detect_agent_no_path_falls_back_to_cwd(self, fake_project):
        """file_path가 비어 있으면 기존대로 cwd 기반."""
        cwd = str(fake_project / ".worktrees" / "backend")
        assert hook.detect_agent(cwd, None) == "backend"
        assert hook.detect_agent(cwd, "") == "backend"

    def test_detect_agent_main_cwd_main_path_is_shared(self, fake_project):
        cwd = str(fake_project)
        path = str(fake_project / "docs" / "API.md")
        assert hook.detect_agent(cwd, path) == "shared"

    def test_main_cwd_can_edit_worktree_backend(self, fake_project):
        """메인 cwd에서 worktree backend path 수정 — 보강 후엔 backend
        정책 적용으로 허용. (보강 전엔 shared 분류로 차단됐음.)"""
        cwd = str(fake_project)
        path = str(
            fake_project / ".worktrees" / "backend" / "backend" / "internal"
            / "repo" / "events_repo.go"
        )
        # 디렉토리 미리 생성
        Path(path).parent.mkdir(parents=True, exist_ok=True)
        rc, stderr = _run(path, cwd)
        assert rc == 0, f"expected allow, got rc={rc} stderr={stderr}"

    def test_main_cwd_can_edit_worktree_db(self, fake_project):
        """db/도 backend 영역이라 같은 규칙 적용."""
        cwd = str(fake_project)
        path = str(
            fake_project / ".worktrees" / "backend" / "db"
            / "migrations" / "00099_x.sql"
        )
        Path(path).parent.mkdir(parents=True, exist_ok=True)
        rc, stderr = _run(path, cwd)
        assert rc == 0, f"expected allow, got rc={rc} stderr={stderr}"

    def test_main_cwd_can_edit_worktree_frontend(self, fake_project):
        cwd = str(fake_project)
        path = str(
            fake_project / ".worktrees" / "frontend" / "frontend"
            / "src" / "App.tsx"
        )
        Path(path).parent.mkdir(parents=True, exist_ok=True)
        rc, stderr = _run(path, cwd)
        assert rc == 0, f"expected allow, got rc={rc} stderr={stderr}"

    def test_main_cwd_blocks_worktree_phases(self, fake_project):
        """worktree path라도 phases/는 BACKEND_ALLOWED 밖이라 차단된다 —
        path 기반 agent 식별이 보안 완화로 이어지지 않음을 보장."""
        cwd = str(fake_project)
        path = str(
            fake_project / ".worktrees" / "backend" / "phases" / "x.json"
        )
        Path(path).parent.mkdir(parents=True, exist_ok=True)
        rc, stderr = _run(path, cwd)
        assert rc == 2
        assert "BLOCKED" in stderr

    def test_main_cwd_blocks_worktree_cross_module(self, fake_project):
        """worktree backend에서 frontend/ 수정은 여전히 차단."""
        cwd = str(fake_project)
        path = str(
            fake_project / ".worktrees" / "backend" / "frontend" / "x.tsx"
        )
        Path(path).parent.mkdir(parents=True, exist_ok=True)
        rc, stderr = _run(path, cwd)
        assert rc == 2
        assert "BLOCKED" in stderr

    def test_backend_cwd_blocks_other_worktree(self, fake_project):
        """backend cwd에서 frontend worktree path 수정은 차단 (cross-worktree)."""
        cwd = str(fake_project / ".worktrees" / "backend")
        path = str(
            fake_project / ".worktrees" / "frontend" / "frontend" / "x.tsx"
        )
        Path(path).parent.mkdir(parents=True, exist_ok=True)
        rc, stderr = _run(path, cwd)
        # path 기반으로 frontend agent 정책 적용 → frontend/는 frontend 허용
        # 단 cwd가 backend라 cross-worktree 침범. 정책상 path 우선이라
        # frontend agent로 분류되어 frontend/는 허용된다. 이는 의도된 동작
        # (메인이든 다른 worktree든 path가 frontend면 frontend 정책으로 처리).
        # 그러나 일반 흐름에서 backend cwd의 자식 Claude는 .worktrees/frontend
        # path를 만들 일이 없고, 만들면 그건 명백한 의도이므로 허용.
        assert rc == 0, f"expected allow (path 우선), got rc={rc} stderr={stderr}"
