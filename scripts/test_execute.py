"""
execute.py 리팩터링 안전망 테스트.
리팩터링 전후 동작이 동일한지 검증한다.
"""

import json
import os
import subprocess
import sys
import textwrap
from datetime import datetime, timezone, timedelta
from pathlib import Path
from unittest.mock import patch, MagicMock

import pytest

sys.path.insert(0, str(Path(__file__).parent))
import execute as ex


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------

@pytest.fixture
def tmp_project(tmp_path):
    """phases/, CLAUDE.md, docs/ 를 갖춘 임시 프로젝트 구조."""
    phases_dir = tmp_path / "phases"
    phases_dir.mkdir()

    claude_md = tmp_path / "CLAUDE.md"
    claude_md.write_text("# Rules\n- rule one\n- rule two")

    docs_dir = tmp_path / "docs"
    docs_dir.mkdir()
    (docs_dir / "arch.md").write_text("# Architecture\nSome content")
    (docs_dir / "guide.md").write_text("# Guide\nAnother doc")

    return tmp_path


@pytest.fixture
def phase_dir(tmp_project):
    """step 3개를 가진 phase 디렉토리."""
    d = tmp_project / "phases" / "0-mvp"
    d.mkdir()

    index = {
        "project": "TestProject",
        "phase": "mvp",
        "steps": [
            {"step": 0, "name": "setup", "status": "completed", "summary": "프로젝트 초기화 완료"},
            {"step": 1, "name": "core", "status": "completed", "summary": "핵심 로직 구현"},
            {"step": 2, "name": "ui", "status": "pending"},
        ],
    }
    (d / "index.json").write_text(json.dumps(index, indent=2, ensure_ascii=False))
    (d / "step2.md").write_text("# Step 2: UI\n\nUI를 구현하세요.")

    return d


@pytest.fixture
def top_index(tmp_project):
    """phases/index.json (top-level)."""
    top = {
        "phases": [
            {"dir": "0-mvp", "status": "pending"},
            {"dir": "1-polish", "status": "pending"},
        ]
    }
    p = tmp_project / "phases" / "index.json"
    p.write_text(json.dumps(top, indent=2))
    return p


@pytest.fixture
def executor(tmp_project, phase_dir):
    """테스트용 StepExecutor 인스턴스. git 호출은 별도 mock 필요."""
    with patch.object(ex, "ROOT", tmp_project):
        inst = ex.StepExecutor("0-mvp")
    # 내부 경로를 tmp_project 기준으로 재설정
    inst._root = str(tmp_project)
    inst._phases_dir = tmp_project / "phases"
    inst._phase_dir = phase_dir
    inst._phase_dir_name = "0-mvp"
    inst._index_file = phase_dir / "index.json"
    inst._top_index_file = tmp_project / "phases" / "index.json"
    return inst


# ---------------------------------------------------------------------------
# _stamp (= 이전 now_iso)
# ---------------------------------------------------------------------------

class TestStamp:
    def test_returns_kst_timestamp(self, executor):
        result = executor._stamp()
        assert "+0900" in result

    def test_format_is_iso(self, executor):
        result = executor._stamp()
        dt = datetime.strptime(result, "%Y-%m-%dT%H:%M:%S%z")
        assert dt.tzinfo is not None

    def test_is_current_time(self, executor):
        before = datetime.now(ex.StepExecutor.TZ).replace(microsecond=0)
        result = executor._stamp()
        after = datetime.now(ex.StepExecutor.TZ).replace(microsecond=0) + timedelta(seconds=1)
        parsed = datetime.strptime(result, "%Y-%m-%dT%H:%M:%S%z")
        assert before <= parsed <= after


# ---------------------------------------------------------------------------
# _read_json / _write_json
# ---------------------------------------------------------------------------

class TestJsonHelpers:
    def test_roundtrip(self, tmp_path):
        data = {"key": "값", "nested": [1, 2, 3]}
        p = tmp_path / "test.json"
        ex.StepExecutor._write_json(p, data)
        loaded = ex.StepExecutor._read_json(p)
        assert loaded == data

    def test_save_ensures_ascii_false(self, tmp_path):
        p = tmp_path / "test.json"
        ex.StepExecutor._write_json(p, {"한글": "테스트"})
        raw = p.read_text()
        assert "한글" in raw
        assert "\\u" not in raw

    def test_save_indented(self, tmp_path):
        p = tmp_path / "test.json"
        ex.StepExecutor._write_json(p, {"a": 1})
        raw = p.read_text()
        assert "\n" in raw

    def test_load_nonexistent_raises(self, tmp_path):
        with pytest.raises(FileNotFoundError):
            ex.StepExecutor._read_json(tmp_path / "nope.json")


# ---------------------------------------------------------------------------
# _load_guardrails
# ---------------------------------------------------------------------------

class TestLoadGuardrails:
    def test_loads_claude_md_and_docs(self, executor, tmp_project):
        with patch.object(ex, "ROOT", tmp_project):
            result = executor._load_guardrails()
        assert "# Rules" in result
        assert "rule one" in result
        assert "# Architecture" in result
        assert "# Guide" in result

    def test_sections_separated_by_divider(self, executor, tmp_project):
        with patch.object(ex, "ROOT", tmp_project):
            result = executor._load_guardrails()
        assert "---" in result

    def test_docs_sorted_alphabetically(self, executor, tmp_project):
        with patch.object(ex, "ROOT", tmp_project):
            result = executor._load_guardrails()
        arch_pos = result.index("arch")
        guide_pos = result.index("guide")
        assert arch_pos < guide_pos

    def test_no_claude_md(self, executor, tmp_project):
        (tmp_project / "CLAUDE.md").unlink()
        with patch.object(ex, "ROOT", tmp_project):
            result = executor._load_guardrails()
        assert "CLAUDE.md" not in result
        assert "Architecture" in result

    def test_no_docs_dir(self, executor, tmp_project):
        import shutil
        shutil.rmtree(tmp_project / "docs")
        with patch.object(ex, "ROOT", tmp_project):
            result = executor._load_guardrails()
        assert "Rules" in result
        assert "Architecture" not in result

    def test_empty_project(self, tmp_path):
        with patch.object(ex, "ROOT", tmp_path):
            # executor가 필요 없는 static-like 동작이므로 임시 인스턴스
            phases_dir = tmp_path / "phases" / "dummy"
            phases_dir.mkdir(parents=True)
            idx = {"project": "T", "phase": "t", "steps": []}
            (phases_dir / "index.json").write_text(json.dumps(idx))
            inst = ex.StepExecutor.__new__(ex.StepExecutor)
            result = inst._load_guardrails()
        assert result == ""


# ---------------------------------------------------------------------------
# _build_step_context
# ---------------------------------------------------------------------------

class TestBuildStepContext:
    def test_includes_completed_with_summary(self, phase_dir):
        index = json.loads((phase_dir / "index.json").read_text())
        result = ex.StepExecutor._build_step_context(index)
        assert "Step 0 (setup): 프로젝트 초기화 완료" in result
        assert "Step 1 (core): 핵심 로직 구현" in result

    def test_excludes_pending(self, phase_dir):
        index = json.loads((phase_dir / "index.json").read_text())
        result = ex.StepExecutor._build_step_context(index)
        assert "ui" not in result

    def test_excludes_completed_without_summary(self, phase_dir):
        index = json.loads((phase_dir / "index.json").read_text())
        del index["steps"][0]["summary"]
        result = ex.StepExecutor._build_step_context(index)
        assert "setup" not in result
        assert "core" in result

    def test_empty_when_no_completed(self):
        index = {"steps": [{"step": 0, "name": "a", "status": "pending"}]}
        result = ex.StepExecutor._build_step_context(index)
        assert result == ""

    def test_has_header(self, phase_dir):
        index = json.loads((phase_dir / "index.json").read_text())
        result = ex.StepExecutor._build_step_context(index)
        assert result.startswith("## 이전 Step 산출물")


# ---------------------------------------------------------------------------
# _build_preamble
# ---------------------------------------------------------------------------

class TestBuildPreamble:
    def test_includes_project_name(self, executor):
        result = executor._build_preamble("", "")
        assert "TestProject" in result

    def test_includes_guardrails(self, executor):
        result = executor._build_preamble("GUARD_CONTENT", "")
        assert "GUARD_CONTENT" in result

    def test_includes_step_context(self, executor):
        ctx = "## 이전 Step 산출물\n\n- Step 0: done"
        result = executor._build_preamble("", ctx)
        assert "이전 Step 산출물" in result

    def test_includes_commit_example(self, executor):
        result = executor._build_preamble("", "")
        assert "feat(mvp):" in result

    def test_includes_rules(self, executor):
        result = executor._build_preamble("", "")
        assert "작업 규칙" in result
        assert "AC" in result

    def test_no_retry_section_by_default(self, executor):
        result = executor._build_preamble("", "")
        assert "이전 시도 실패" not in result

    def test_retry_section_with_prev_error(self, executor):
        result = executor._build_preamble("", "", prev_error="타입 에러 발생")
        assert "이전 시도 실패" in result
        assert "타입 에러 발생" in result

    def test_includes_max_retries(self, executor):
        result = executor._build_preamble("", "")
        assert str(ex.StepExecutor.MAX_RETRIES) in result

    def test_includes_index_path(self, executor):
        result = executor._build_preamble("", "")
        assert "/phases/0-mvp/index.json" in result


# ---------------------------------------------------------------------------
# _update_top_index
# ---------------------------------------------------------------------------

class TestUpdateTopIndex:
    def test_completed(self, executor, top_index):
        executor._top_index_file = top_index
        executor._update_top_index("completed")
        data = json.loads(top_index.read_text())
        mvp = next(p for p in data["phases"] if p["dir"] == "0-mvp")
        assert mvp["status"] == "completed"
        assert "completed_at" in mvp

    def test_error(self, executor, top_index):
        executor._top_index_file = top_index
        executor._update_top_index("error")
        data = json.loads(top_index.read_text())
        mvp = next(p for p in data["phases"] if p["dir"] == "0-mvp")
        assert mvp["status"] == "error"
        assert "failed_at" in mvp

    def test_blocked(self, executor, top_index):
        executor._top_index_file = top_index
        executor._update_top_index("blocked")
        data = json.loads(top_index.read_text())
        mvp = next(p for p in data["phases"] if p["dir"] == "0-mvp")
        assert mvp["status"] == "blocked"
        assert "blocked_at" in mvp

    def test_other_phases_unchanged(self, executor, top_index):
        executor._top_index_file = top_index
        executor._update_top_index("completed")
        data = json.loads(top_index.read_text())
        polish = next(p for p in data["phases"] if p["dir"] == "1-polish")
        assert polish["status"] == "pending"

    def test_nonexistent_dir_is_noop(self, executor, top_index):
        executor._top_index_file = top_index
        executor._phase_dir_name = "no-such-dir"
        original = json.loads(top_index.read_text())
        executor._update_top_index("completed")
        after = json.loads(top_index.read_text())
        for p_before, p_after in zip(original["phases"], after["phases"]):
            assert p_before["status"] == p_after["status"]

    def test_no_top_index_file(self, executor, tmp_path):
        executor._top_index_file = tmp_path / "nonexistent.json"
        executor._update_top_index("completed")  # should not raise


# ---------------------------------------------------------------------------
# _check_clean_tree (worktree 분기 전 dirty 검사)
# ---------------------------------------------------------------------------

class TestCheckCleanTree:
    def _mock_git(self, executor, response):
        def fake_git(*args, cwd=None):
            return response
        executor._run_git = fake_git

    def test_clean_tree_passes(self, executor):
        self._mock_git(executor, MagicMock(returncode=0, stdout="", stderr=""))
        executor._check_clean_tree()  # should return without exit

    def test_dirty_tree_exits_1(self, executor):
        self._mock_git(executor, MagicMock(returncode=0, stdout=" M src/foo.py\n", stderr=""))
        with pytest.raises(SystemExit) as exc_info:
            executor._check_clean_tree()
        assert exc_info.value.code == 1

    def test_no_git_exits_1(self, executor):
        self._mock_git(executor, MagicMock(returncode=1, stdout="", stderr="not a git repo"))
        with pytest.raises(SystemExit) as exc_info:
            executor._check_clean_tree()
        assert exc_info.value.code == 1


# ---------------------------------------------------------------------------
# _commit_step (mocked)
# ---------------------------------------------------------------------------

class TestCommitStep:
    def test_two_phase_commit(self, executor):
        calls = []
        def fake_git(*args, cwd=None):
            calls.append(args)
            if args[:2] == ("diff", "--cached"):
                return MagicMock(returncode=1)
            return MagicMock(returncode=0, stdout="", stderr="")
        executor._run_git = fake_git

        executor._commit_step(2, "ui")

        commit_calls = [c for c in calls if c[0] == "commit"]
        assert len(commit_calls) == 2
        assert "feat(mvp):" in commit_calls[0][2]
        assert "chore(mvp):" in commit_calls[1][2]

    def test_no_code_changes_skips_feat_commit(self, executor):
        call_count = {"diff": 0}
        calls = []
        def fake_git(*args, cwd=None):
            calls.append(args)
            if args[:2] == ("diff", "--cached"):
                call_count["diff"] += 1
                if call_count["diff"] == 1:
                    return MagicMock(returncode=0)
                return MagicMock(returncode=1)
            return MagicMock(returncode=0, stdout="", stderr="")
        executor._run_git = fake_git

        executor._commit_step(2, "ui")

        commit_msgs = [c[2] for c in calls if c[0] == "commit"]
        assert len(commit_msgs) == 1
        assert "chore" in commit_msgs[0]

    def test_commit_uses_provided_cwd(self, executor):
        calls = []
        def fake_git(*args, cwd=None):
            calls.append((args, cwd))
            if args[:2] == ("diff", "--cached"):
                return MagicMock(returncode=1)
            return MagicMock(returncode=0, stdout="", stderr="")
        executor._run_git = fake_git

        executor._commit_step(2, "ui", cwd="/tmp/some-worktree")

        # 모든 git 호출이 지정된 cwd를 받아야 한다
        for args, cwd in calls:
            assert cwd == "/tmp/some-worktree"


# ---------------------------------------------------------------------------
# _invoke_claude (mocked)
# ---------------------------------------------------------------------------

class TestInvokeClaude:
    def test_invokes_claude_with_correct_args(self, executor):
        mock_result = MagicMock(returncode=0, stdout='{"result": "ok"}', stderr="")
        step = {"step": 2, "name": "ui"}
        preamble = "PREAMBLE\n"

        with patch("subprocess.run", return_value=mock_result) as mock_run:
            output = executor._invoke_claude(step, preamble)

        cmd = mock_run.call_args[0][0]
        assert cmd[0] == "claude"
        assert "-p" in cmd
        assert "--dangerously-skip-permissions" in cmd
        assert "--output-format" in cmd
        # prompt는 ARG_MAX 회피를 위해 stdin(input=)으로 전달.
        prompt = mock_run.call_args.kwargs["input"]
        assert "PREAMBLE" in prompt
        assert "UI를 구현하세요" in prompt

    def test_saves_output_json(self, executor):
        mock_result = MagicMock(returncode=0, stdout='{"ok": true}', stderr="")
        step = {"step": 2, "name": "ui"}

        with patch("subprocess.run", return_value=mock_result):
            executor._invoke_claude(step, "preamble")

        output_file = executor._phase_dir / "step2-output.json"
        assert output_file.exists()
        data = json.loads(output_file.read_text())
        assert data["step"] == 2
        assert data["name"] == "ui"
        assert data["exitCode"] == 0

    def test_nonexistent_step_file_exits(self, executor):
        step = {"step": 99, "name": "nonexistent"}
        with pytest.raises(SystemExit) as exc_info:
            executor._invoke_claude(step, "preamble")
        assert exc_info.value.code == 1

    def test_timeout_is_1800(self, executor):
        mock_result = MagicMock(returncode=0, stdout="{}", stderr="")
        step = {"step": 2, "name": "ui"}

        with patch("subprocess.run", return_value=mock_result) as mock_run:
            executor._invoke_claude(step, "preamble")

        assert mock_run.call_args[1]["timeout"] == 1800


# ---------------------------------------------------------------------------
# progress_indicator (= 이전 Spinner)
# ---------------------------------------------------------------------------

class TestProgressIndicator:
    def test_context_manager(self):
        import time
        with ex.progress_indicator("test") as pi:
            time.sleep(0.15)
        assert pi.elapsed >= 0.1

    def test_elapsed_increases(self):
        import time
        with ex.progress_indicator("test") as pi:
            time.sleep(0.2)
        assert pi.elapsed > 0


# ---------------------------------------------------------------------------
# main() CLI 파싱 (mocked)
# ---------------------------------------------------------------------------

class TestMainCli:
    def test_no_args_exits(self):
        with patch("sys.argv", ["execute.py"]):
            with pytest.raises(SystemExit) as exc_info:
                ex.main()
            assert exc_info.value.code == 2  # argparse exits with 2

    def test_invalid_phase_dir_exits(self):
        with patch("sys.argv", ["execute.py", "nonexistent"]):
            with patch.object(ex, "ROOT", Path("/tmp/fake_nonexistent")):
                with pytest.raises(SystemExit) as exc_info:
                    ex.main()
                assert exc_info.value.code == 1

    def test_missing_index_exits(self, tmp_project):
        (tmp_project / "phases" / "empty").mkdir()
        with patch("sys.argv", ["execute.py", "empty"]):
            with patch.object(ex, "ROOT", tmp_project):
                with pytest.raises(SystemExit) as exc_info:
                    ex.main()
                assert exc_info.value.code == 1


# ---------------------------------------------------------------------------
# _check_blockers (= 이전 main() error/blocked 체크)
# ---------------------------------------------------------------------------

class TestCheckBlockers:
    def _make_executor_with_steps(self, tmp_project, steps):
        d = tmp_project / "phases" / "test-phase"
        d.mkdir(exist_ok=True)
        index = {"project": "T", "phase": "test", "steps": steps}
        (d / "index.json").write_text(json.dumps(index))

        with patch.object(ex, "ROOT", tmp_project):
            inst = ex.StepExecutor.__new__(ex.StepExecutor)
        inst._root = str(tmp_project)
        inst._phases_dir = tmp_project / "phases"
        inst._phase_dir = d
        inst._phase_dir_name = "test-phase"
        inst._index_file = d / "index.json"
        inst._top_index_file = tmp_project / "phases" / "index.json"
        inst._phase_name = "test"
        inst._total = len(steps)
        return inst

    def test_error_step_exits_1(self, tmp_project):
        steps = [
            {"step": 0, "name": "ok", "status": "completed"},
            {"step": 1, "name": "bad", "status": "error", "error_message": "fail"},
        ]
        inst = self._make_executor_with_steps(tmp_project, steps)
        with pytest.raises(SystemExit) as exc_info:
            inst._check_blockers()
        assert exc_info.value.code == 1

    def test_blocked_step_exits_2(self, tmp_project):
        steps = [
            {"step": 0, "name": "ok", "status": "completed"},
            {"step": 1, "name": "stuck", "status": "blocked", "blocked_reason": "API key"},
        ]
        inst = self._make_executor_with_steps(tmp_project, steps)
        with pytest.raises(SystemExit) as exc_info:
            inst._check_blockers()
        assert exc_info.value.code == 2


# ---------------------------------------------------------------------------
# _parse_frontmatter (모듈 함수)
# ---------------------------------------------------------------------------

class TestParseFrontmatter:
    def test_no_frontmatter_returns_empty_meta(self):
        text = "# Step 1\n\n본문이다."
        meta, body = ex._parse_frontmatter(text)
        assert meta == {}
        assert body == text

    def test_simple_agent_field(self):
        text = "---\nagent: backend\n---\n# Step\n본문"
        meta, body = ex._parse_frontmatter(text)
        assert meta == {"agent": "backend"}
        assert body == "# Step\n본문"

    def test_multiple_keys(self):
        text = "---\nagent: frontend\nparallel_group: 2\n---\nbody"
        meta, _ = ex._parse_frontmatter(text)
        assert meta == {"agent": "frontend", "parallel_group": 2}

    def test_list_value(self):
        text = "---\ndepends_on: [step0, step1-be]\n---\nbody"
        meta, _ = ex._parse_frontmatter(text)
        assert meta == {"depends_on": ["step0", "step1-be"]}

    def test_quoted_value(self):
        text = "---\nagent: \"backend\"\n---\nbody"
        meta, _ = ex._parse_frontmatter(text)
        assert meta == {"agent": "backend"}

    def test_unterminated_block_returns_empty(self):
        text = "---\nagent: backend\n# 종료 마커 없음"
        meta, body = ex._parse_frontmatter(text)
        assert meta == {}
        assert body == text

    def test_comment_lines_ignored(self):
        text = "---\n# 이 줄은 주석\nagent: shared\n---\nbody"
        meta, _ = ex._parse_frontmatter(text)
        assert meta == {"agent": "shared"}


# ---------------------------------------------------------------------------
# _read_step_summary (B 방안 — frontmatter summary를 main 인덱스에 박는다)
# ---------------------------------------------------------------------------


class TestReadStepSummary:
    def test_reads_summary_from_frontmatter(self, executor, tmp_path):
        executor._phase_dir = tmp_path
        (tmp_path / "step3.md").write_text(
            "---\nagent: backend\nsummary: 산출물 한 줄\n---\n본문\n",
            encoding="utf-8",
        )
        assert executor._read_step_summary(3) == "산출물 한 줄"

    def test_reads_quoted_summary(self, executor, tmp_path):
        executor._phase_dir = tmp_path
        (tmp_path / "step3.md").write_text(
            '---\nagent: backend\nsummary: "산출물(괄호) + 콜론: 허용"\n---\nbody',
            encoding="utf-8",
        )
        assert executor._read_step_summary(3) == "산출물(괄호) + 콜론: 허용"

    def test_returns_empty_when_no_summary(self, executor, tmp_path):
        executor._phase_dir = tmp_path
        (tmp_path / "step1.md").write_text(
            "---\nagent: backend\n---\n본문", encoding="utf-8"
        )
        assert executor._read_step_summary(1) == ""

    def test_returns_empty_when_no_step_file(self, executor, tmp_path):
        executor._phase_dir = tmp_path
        assert executor._read_step_summary(99) == ""


# ---------------------------------------------------------------------------
# _agent_for_step (index.json > frontmatter > shared 기본값)
# ---------------------------------------------------------------------------

class TestAgentForStep:
    def test_index_field_takes_precedence(self, executor):
        # step.md에 frontmatter 없음, index의 agent만
        agent = executor._agent_for_step({"step": 2, "agent": "backend"})
        assert agent == "backend"

    def test_invalid_index_agent_falls_back(self, executor, phase_dir):
        # index.json에 잘못된 agent가 있으면 frontmatter/기본값으로 fallback
        (phase_dir / "step2.md").write_text("---\nagent: frontend\n---\n# Step")
        agent = executor._agent_for_step({"step": 2, "agent": "garbage"})
        assert agent == "frontend"

    def test_frontmatter_used_when_no_index(self, executor, phase_dir):
        (phase_dir / "step2.md").write_text("---\nagent: frontend\n---\nbody")
        agent = executor._agent_for_step({"step": 2})
        assert agent == "frontend"

    def test_default_shared_when_neither(self, executor, phase_dir):
        (phase_dir / "step2.md").write_text("# 일반 본문")
        agent = executor._agent_for_step({"step": 2})
        assert agent == "shared"

    def test_step_file_missing_returns_shared(self, executor):
        agent = executor._agent_for_step({"step": 99})
        assert agent == "shared"


# ---------------------------------------------------------------------------
# _load_guardrails(agent) — agent별 슬림화
# ---------------------------------------------------------------------------

class TestLoadGuardrailsByAgent:
    @pytest.fixture
    def realish_project(self, tmp_path):
        """루트 CLAUDE.md + 모듈 CLAUDE.md + docs/* 를 갖춘 임시 프로젝트."""
        (tmp_path / "CLAUDE.md").write_text("# Root\nROOT_RULE")
        (tmp_path / "frontend").mkdir()
        (tmp_path / "frontend" / "CLAUDE.md").write_text("# FE\nFE_RULE")
        (tmp_path / "backend").mkdir()
        (tmp_path / "backend" / "CLAUDE.md").write_text("# BE\nBE_RULE")
        (tmp_path / "db").mkdir()
        (tmp_path / "db" / "CLAUDE.md").write_text("# DB\nDB_RULE")
        docs = tmp_path / "docs"
        docs.mkdir()
        for name in ("API", "ARCHITECTURE", "ADR", "UI_GUIDE", "PRD"):
            (docs / f"{name}.md").write_text(f"# {name}\n{name}_BODY")
        # phase 디렉토리도 필요
        phases = tmp_path / "phases" / "p"
        phases.mkdir(parents=True)
        idx = {"project": "T", "phase": "p", "steps": []}
        (phases / "index.json").write_text(json.dumps(idx))
        return tmp_path

    def _instance(self, tmp_project):
        with patch.object(ex, "ROOT", tmp_project):
            inst = ex.StepExecutor.__new__(ex.StepExecutor)
        inst._root = str(tmp_project)
        return inst

    def test_backend_includes_only_relevant(self, realish_project):
        inst = self._instance(realish_project)
        with patch.object(ex, "ROOT", realish_project):
            out = inst._load_guardrails("backend")
        assert "ROOT_RULE" in out
        assert "BE_RULE" in out
        assert "DB_RULE" in out
        assert "API_BODY" in out
        assert "ARCHITECTURE_BODY" in out
        assert "ADR_BODY" in out
        assert "FE_RULE" not in out      # frontend 제외
        assert "UI_GUIDE_BODY" not in out  # UI 제외
        assert "PRD_BODY" not in out      # 슬림화 제외

    def test_frontend_includes_only_relevant(self, realish_project):
        inst = self._instance(realish_project)
        with patch.object(ex, "ROOT", realish_project):
            out = inst._load_guardrails("frontend")
        assert "ROOT_RULE" in out
        assert "FE_RULE" in out
        assert "API_BODY" in out
        assert "UI_GUIDE_BODY" in out
        assert "ADR_BODY" in out
        assert "BE_RULE" not in out
        assert "DB_RULE" not in out

    def test_both_is_union(self, realish_project):
        inst = self._instance(realish_project)
        with patch.object(ex, "ROOT", realish_project):
            out = inst._load_guardrails("both")
        assert "BE_RULE" in out
        assert "FE_RULE" in out
        assert "DB_RULE" in out
        assert "UI_GUIDE_BODY" in out

    def test_backend_no_duplicate_root_claude(self, realish_project):
        inst = self._instance(realish_project)
        with patch.object(ex, "ROOT", realish_project):
            out = inst._load_guardrails("both")
        # ROOT 본문이 한 번만 들어가야 한다 (both 합집합 시 dedupe)
        assert out.count("ROOT_RULE") == 1

    def test_shared_includes_everything(self, realish_project):
        inst = self._instance(realish_project)
        with patch.object(ex, "ROOT", realish_project):
            out = inst._load_guardrails("shared")
        for needle in ("ROOT_RULE", "FE_RULE", "BE_RULE", "DB_RULE",
                       "API_BODY", "UI_GUIDE_BODY", "PRD_BODY", "ADR_BODY"):
            assert needle in out

    def test_default_agent_is_shared(self, realish_project):
        inst = self._instance(realish_project)
        with patch.object(ex, "ROOT", realish_project):
            out = inst._load_guardrails()
        assert "PRD_BODY" in out  # shared만 PRD까지 로드


# ---------------------------------------------------------------------------
# _collect_agents — index.json에서 worktree 필요 여부 추출
# ---------------------------------------------------------------------------

class TestCollectAgents:
    def _make(self, tmp_project, steps):
        d = tmp_project / "phases" / "p"
        d.mkdir(parents=True, exist_ok=True)
        idx = {"project": "T", "phase": "p", "steps": steps}
        (d / "index.json").write_text(json.dumps(idx))
        for s in steps:
            (d / f"step{s['step']}.md").write_text(f"# S{s['step']}")
        with patch.object(ex, "ROOT", tmp_project):
            inst = ex.StepExecutor.__new__(ex.StepExecutor)
        inst._root = str(tmp_project)
        inst._phase_dir = d
        inst._index_file = d / "index.json"
        inst._phase_dir_name = "p"
        return inst

    def test_only_shared_returns_empty(self, tmp_path):
        inst = self._make(tmp_path, [
            {"step": 0, "name": "a", "agent": "shared", "status": "pending"},
        ])
        assert inst._collect_agents() == set()

    def test_backend_only(self, tmp_path):
        inst = self._make(tmp_path, [
            {"step": 0, "name": "a", "agent": "backend", "status": "pending"},
        ])
        assert inst._collect_agents() == {"backend"}

    def test_both_expands_to_be_and_fe(self, tmp_path):
        inst = self._make(tmp_path, [
            {"step": 0, "name": "a", "agent": "both", "status": "pending"},
        ])
        assert inst._collect_agents() == {"backend", "frontend"}

    def test_mixed_collects_all_unique(self, tmp_path):
        inst = self._make(tmp_path, [
            {"step": 0, "name": "a", "agent": "shared", "status": "pending"},
            {"step": 1, "name": "b", "agent": "backend", "status": "pending"},
            {"step": 2, "name": "c", "agent": "frontend", "status": "pending"},
        ])
        assert inst._collect_agents() == {"backend", "frontend"}


# ---------------------------------------------------------------------------
# _invoke_claude — --max-turns 60 추가 확인
# ---------------------------------------------------------------------------

class TestInvokeClaudeMaxTurns:
    def test_includes_max_turns_flag(self, executor):
        mock_result = MagicMock(returncode=0, stdout="{}", stderr="")
        step = {"step": 2, "name": "ui"}
        with patch("subprocess.run", return_value=mock_result) as mock_run:
            executor._invoke_claude(step, "preamble")
        cmd = mock_run.call_args[0][0]
        assert "--max-turns" in cmd
        assert str(ex.StepExecutor.MAX_TURNS) in cmd

    def test_uses_provided_cwd(self, executor):
        mock_result = MagicMock(returncode=0, stdout="{}", stderr="")
        step = {"step": 2, "name": "ui"}
        with patch("subprocess.run", return_value=mock_result) as mock_run:
            executor._invoke_claude(step, "preamble", cwd="/tmp/wt")
        assert mock_run.call_args[1]["cwd"] == "/tmp/wt"

    def test_strips_frontmatter_from_prompt(self, executor, phase_dir):
        # step.md에 frontmatter가 있으면 prompt에는 본문만 들어간다
        (phase_dir / "step2.md").write_text("---\nagent: frontend\n---\n# Step\n실제 본문")
        mock_result = MagicMock(returncode=0, stdout="{}", stderr="")
        with patch("subprocess.run", return_value=mock_result) as mock_run:
            executor._invoke_claude({"step": 2, "name": "ui"}, "PREAMBLE\n")
        # prompt는 stdin(input=)로 전달됨.
        prompt = mock_run.call_args.kwargs["input"]
        assert "agent: frontend" not in prompt
        assert "실제 본문" in prompt
        assert "PREAMBLE" in prompt


# ---------------------------------------------------------------------------
# _build_preamble — agent 역할 안내 주입
# ---------------------------------------------------------------------------

class TestBuildPreambleAgentRole:
    def test_backend_role_included(self, executor):
        out = executor._build_preamble("", "", agent="backend")
        assert "backend-engineer" in out

    def test_frontend_role_included(self, executor):
        out = executor._build_preamble("", "", agent="frontend")
        assert "frontend-engineer" in out

    def test_shared_role_included(self, executor):
        out = executor._build_preamble("", "", agent="shared")
        assert "공유 영역" in out

    def test_both_role_included(self, executor):
        out = executor._build_preamble("", "", agent="both")
        assert "Backend 작업" in out
        assert "Frontend 작업" in out


# ---------------------------------------------------------------------------
# _ensure_branches_and_worktrees (mocked git)
# ---------------------------------------------------------------------------

class TestEnsureBranchesAndWorktrees:
    def test_empty_agents_is_noop(self, executor):
        calls = []
        def fake_git(*args, cwd=None):
            calls.append(args)
            return MagicMock(returncode=0, stdout="", stderr="")
        executor._run_git = fake_git
        executor._ensure_branches_and_worktrees(set())
        assert calls == []

    def test_creates_new_worktree_when_branch_missing(self, executor, tmp_path):
        # ROOT을 tmp_path로 패치해 실제 디렉토리가 만들어지도록
        with patch.object(ex, "ROOT", tmp_path):
            # 인스턴스의 ROOT 캐시도 갱신
            executor._root = str(tmp_path)
            calls = []
            def fake_git(*args, cwd=None):
                calls.append(args)
                if args[:2] == ("rev-parse", "--verify"):
                    return MagicMock(returncode=1, stdout="", stderr="")
                return MagicMock(returncode=0, stdout="", stderr="")
            executor._run_git = fake_git
            executor._ensure_branches_and_worktrees({"backend"})
        # worktree add -b feat/.../be 호출이 있어야 한다
        add_calls = [c for c in calls if c[0] == "worktree" and c[1] == "add"]
        assert len(add_calls) == 1
        assert "-b" in add_calls[0]
        assert any("be" in str(c) for c in add_calls[0])

    def test_creates_worktree_dir(self, executor, tmp_path):
        with patch.object(ex, "ROOT", tmp_path):
            executor._root = str(tmp_path)
            def fake_git(*args, cwd=None):
                if args[:2] == ("rev-parse", "--verify"):
                    return MagicMock(returncode=1)
                return MagicMock(returncode=0, stdout="", stderr="")
            executor._run_git = fake_git
            executor._ensure_branches_and_worktrees({"frontend"})
        assert (tmp_path / ".worktrees").is_dir()

    def test_syncs_settings_local_json(self, executor, tmp_path):
        """worktree 생성 시 main의 .claude/settings.json을 settings.local.json으로 복사."""
        # main settings.json 준비
        (tmp_path / ".claude").mkdir()
        (tmp_path / ".claude" / "settings.json").write_text('{"hooks":{}}', encoding="utf-8")
        with patch.object(ex, "ROOT", tmp_path):
            executor._root = str(tmp_path)
            def fake_git(*args, cwd=None):
                if args[:2] == ("rev-parse", "--verify"):
                    return MagicMock(returncode=1)
                return MagicMock(returncode=0, stdout="", stderr="")
            executor._run_git = fake_git
            executor._ensure_branches_and_worktrees({"backend"})
        # worktree에 settings.local.json이 생겼는지
        local_settings = tmp_path / ".worktrees" / "backend" / ".claude" / "settings.local.json"
        assert local_settings.exists()
        assert '{"hooks":{}}' in local_settings.read_text(encoding="utf-8")


# ---------------------------------------------------------------------------
# Post-completion gate (acceptance + code-reviewer)
# ---------------------------------------------------------------------------

class TestPostCompletionGate:
    def test_shared_agent_skips_acceptance(self, executor):
        # shared는 acceptance 생략 — review만 검증되는데 review도 mock
        executor._run_review = lambda cwd, step_num=None: (True, "")
        gate, msg = executor._post_completion_gate("shared", "/tmp")
        assert gate == "pass"

    def test_backend_blocks_when_go_missing(self, executor, tmp_path):
        # backend/go.mod가 있을 때만 binary 검사가 일어난다 — 그 시점에 go가 없으면 block
        (tmp_path / "backend").mkdir()
        (tmp_path / "backend" / "go.mod").write_text("module test\n")
        executor._which = lambda b: False
        gate, msg = executor._post_completion_gate("backend", str(tmp_path))
        assert gate == "block"
        assert "go" in msg

    def test_backend_skips_acceptance_when_no_go_mod(self, executor, tmp_path):
        # Phase 1처럼 db/ 변경만 있고 backend 스캐폴드(go.mod)가 아직 없으면
        # build/test 대상이 없으므로 acceptance를 skip → review만 통과하면 pass.
        (tmp_path / "backend").mkdir()
        executor._which = lambda b: False  # go 미설치여도 skip 경로라 영향 없어야 함
        executor._run_review = lambda cwd, step_num=None: (True, "")
        gate, msg = executor._post_completion_gate("backend", str(tmp_path))
        assert gate == "pass", f"got {gate}: {msg}"

    def test_backend_pass_with_mocked_subprocess(self, executor, tmp_path):
        # backend 디렉토리 + go.mod 모킹
        (tmp_path / "backend").mkdir()
        (tmp_path / "backend" / "go.mod").write_text("module test\n")
        executor._which = lambda b: True
        executor._run_review = lambda cwd, step_num=None: (True, "")
        with patch("execute.subprocess.run") as mock_run:
            mock_run.return_value = MagicMock(returncode=0, stdout="", stderr="")
            gate, msg = executor._post_completion_gate("backend", str(tmp_path))
        assert gate == "pass", f"got {gate}: {msg}"

    def test_backend_retry_on_go_test_fail(self, executor, tmp_path):
        (tmp_path / "backend").mkdir()
        (tmp_path / "backend" / "go.mod").write_text("module test\n")
        executor._which = lambda b: True
        with patch("execute.subprocess.run") as mock_run:
            # go build 통과, go test 실패
            mock_run.side_effect = [
                MagicMock(returncode=0, stdout="", stderr=""),
                MagicMock(returncode=1, stdout="FAIL", stderr="test failed"),
            ]
            gate, msg = executor._post_completion_gate("backend", str(tmp_path))
        assert gate == "retry"
        assert "acceptance" in msg

    def test_review_block_returns_retry(self, executor):
        executor._run_acceptance = lambda agent, cwd: (True, "")
        with patch("execute.subprocess.run") as mock_run:
            mock_run.return_value = MagicMock(
                returncode=0, stdout="BLOCK\n- A-3: backend/x.go:1 — phone log", stderr=""
            )
            gate, msg = executor._post_completion_gate("backend", "/tmp")
        assert gate == "retry"
        assert "BLOCK" in msg

    def test_review_pass_returns_pass(self, executor):
        executor._run_acceptance = lambda agent, cwd: (True, "")
        with patch("execute.subprocess.run") as mock_run:
            mock_run.return_value = MagicMock(returncode=0, stdout="PASS", stderr="")
            gate, msg = executor._post_completion_gate("backend", "/tmp")
        assert gate == "pass"

    def test_review_unclear_returns_retry(self, executor):
        executor._run_acceptance = lambda agent, cwd: (True, "")
        with patch("execute.subprocess.run") as mock_run:
            mock_run.return_value = MagicMock(returncode=0, stdout="some random output", stderr="")
            gate, msg = executor._post_completion_gate("backend", "/tmp")
        assert gate == "retry"


# ---------------------------------------------------------------------------
# step.md frontmatter 검증
# ---------------------------------------------------------------------------

class TestValidateStepFiles:
    def test_passes_for_valid_steps(self, executor):
        # 기본 fixture는 agent 필드가 step에 없어도 frontmatter 없으면 shared로 fallback
        # 검증 통과해야 함
        executor._validate_step_files()  # exit 1 안 나면 통과

    def test_blocks_invalid_agent(self, executor, phase_dir):
        idx = json.loads((phase_dir / "index.json").read_text())
        idx["steps"][2]["agent"] = "wizard"  # 유효하지 않은 agent
        (phase_dir / "index.json").write_text(json.dumps(idx))
        with pytest.raises(SystemExit) as exc:
            executor._validate_step_files()
        assert exc.value.code == 1

    def test_blocks_unknown_depends_on(self, executor, phase_dir):
        # step2.md에 depends_on이 존재하지 않는 step name을 가리키게
        (phase_dir / "step2.md").write_text(
            "---\nagent: shared\ndepends_on: [nonexistent-step]\n---\n# Step 2\n"
        )
        with pytest.raises(SystemExit) as exc:
            executor._validate_step_files()
        assert exc.value.code == 1

    def test_blocks_parallel_group_same_agent(self, executor, phase_dir):
        idx = json.loads((phase_dir / "index.json").read_text())
        # 두 step을 같은 parallel_group에 같은 agent로 두면 충돌
        idx["steps"][0]["parallel_group"] = 1
        idx["steps"][0]["agent"] = "backend"
        idx["steps"][1]["parallel_group"] = 1
        idx["steps"][1]["agent"] = "backend"
        (phase_dir / "index.json").write_text(json.dumps(idx))
        with pytest.raises(SystemExit) as exc:
            executor._validate_step_files()
        assert exc.value.code == 1

    def test_allows_parallel_group_different_agents(self, executor, phase_dir):
        idx = json.loads((phase_dir / "index.json").read_text())
        idx["steps"][0]["parallel_group"] = 1
        idx["steps"][0]["agent"] = "backend"
        idx["steps"][1]["parallel_group"] = 1
        idx["steps"][1]["agent"] = "frontend"
        (phase_dir / "index.json").write_text(json.dumps(idx))
        # 통과해야 함
        executor._validate_step_files()


# ---------------------------------------------------------------------------
# --dry-run 모드
# ---------------------------------------------------------------------------

class TestDryRun:
    def test_dry_run_does_not_invoke_claude(self, executor, phase_dir, capsys):
        executor._dry_run = True
        # subprocess.run을 호출하면 Claude 호출이 일어난 것 — 검증
        with patch("execute.subprocess.run") as mock_run:
            executor.run()
        # subprocess.run은 _check_clean_tree(git status) 등에서 호출되지 않아야 함
        # (dry_run이 그것들을 건너뛰므로)
        called_args = [c.args for c in mock_run.call_args_list]
        # claude CLI 호출이 없어야 함
        for args in called_args:
            cmd = args[0] if args else []
            if cmd and len(cmd) > 0:
                assert cmd[0] != "claude", f"dry-run mode should not invoke claude: {cmd}"

    def test_dry_run_outputs_plan(self, executor, phase_dir, capsys):
        executor._dry_run = True
        executor.run()
        out = capsys.readouterr().out
        assert "DRY-RUN" in out
        assert "step" in out.lower()

    def test_dry_run_runs_validate(self, executor, phase_dir):
        """dry-run 모드에서도 frontmatter 검증은 동작해야 한다."""
        idx = json.loads((phase_dir / "index.json").read_text())
        idx["steps"][2]["agent"] = "wizard"  # invalid
        (phase_dir / "index.json").write_text(json.dumps(idx))
        executor._dry_run = True
        with pytest.raises(SystemExit) as exc:
            executor.run()
        assert exc.value.code == 1

    def test_dry_run_skips_clean_tree_check(self, executor, phase_dir, capsys):
        """dry-run은 dirty tree여도 통과 — 실행 계획만 보는 용도."""
        executor._dry_run = True
        # _check_clean_tree를 호출하면 fail해야 한다고 가정. dry-run은 호출 안 해야.
        with patch.object(executor, "_check_clean_tree") as mock_check:
            executor.run()
        mock_check.assert_not_called()


# ---------------------------------------------------------------------------
# _finalize — 부분 진행 가드 (검토 게이트 패턴 지원)
# ---------------------------------------------------------------------------

class TestFinalizePartialProgress:
    """
    검토 게이트 패턴: 미래 step을 'deferred' 같은 비-pending status로 두고
    라운드별로 풀어가는 흐름을 지원한다. _finalize는 모든 step이 completed일 때만
    merge·top-index 갱신을 수행해야 한다.
    """

    def _make_index(self, phase_dir, statuses):
        """statuses: status 문자열 리스트. step 번호는 0부터."""
        idx = {
            "project": "TestProject",
            "phase": "mvp",
            "steps": [
                {"step": i, "name": f"s{i}", "status": st}
                for i, st in enumerate(statuses)
            ],
        }
        (phase_dir / "index.json").write_text(json.dumps(idx, ensure_ascii=False))

    def test_skips_when_some_step_deferred(self, executor, phase_dir, capsys):
        """deferred가 남아있으면 finalize 동작 보류."""
        self._make_index(phase_dir, ["completed", "deferred", "deferred"])
        with patch.object(executor, "_run_git") as mock_git, \
             patch.object(executor, "_update_top_index") as mock_top:
            executor._finalize()
        out = capsys.readouterr().out
        assert "부분 진행" in out
        assert "보류" in out
        # git/top-index 작업 호출 안 됨
        mock_git.assert_not_called()
        mock_top.assert_not_called()
        # phase index에 completed_at 기록 안 됨
        idx = json.loads((phase_dir / "index.json").read_text())
        assert "completed_at" not in idx

    def test_skips_when_some_step_pending(self, executor, phase_dir, capsys):
        """pending이 남아있어도(이론적으로) finalize 보류."""
        self._make_index(phase_dir, ["completed", "completed", "pending"])
        with patch.object(executor, "_run_git") as mock_git, \
             patch.object(executor, "_update_top_index") as mock_top:
            executor._finalize()
        out = capsys.readouterr().out
        assert "부분 진행" in out
        mock_git.assert_not_called()
        mock_top.assert_not_called()

    def test_runs_when_all_completed(self, executor, phase_dir, top_index):
        """모든 step이 completed면 finalize 정상 동작 — completed_at 기록 + top-index 갱신."""
        self._make_index(phase_dir, ["completed", "completed", "completed"])
        with patch.object(executor, "_run_git") as mock_git:
            # git diff/commit/merge는 모두 returncode 0 (또는 적절한 mock)으로
            mock_git.return_value = MagicMock(returncode=0, stdout="", stderr="")
            executor._collect_agents = lambda: set()  # worktree merge skip
            executor._finalize()
        idx = json.loads((phase_dir / "index.json").read_text())
        assert "completed_at" in idx
        # top-level phases/index.json도 completed로 업데이트
        top = json.loads(top_index.read_text())
        target = next(p for p in top["phases"] if p["dir"] == "0-mvp")
        assert target["status"] == "completed"

    def test_remaining_step_listed_in_message(self, executor, phase_dir, capsys):
        """보류 메시지에 남은 step 번호·status가 표시되어야 운영자가 다음에 뭘 풀지 알 수 있다."""
        self._make_index(phase_dir, ["completed", "deferred", "pending"])
        with patch.object(executor, "_run_git"), patch.object(executor, "_update_top_index"):
            executor._finalize()
        out = capsys.readouterr().out
        assert "step1" in out
        assert "deferred" in out
        assert "step2" in out
        assert "pending" in out
