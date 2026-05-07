#!/usr/bin/env python3
"""
statusLine hook — 항상 보이는 진행 상황.

라인 1: harness 진행 상황
    phase:<dir> step:<N>/<total> agent:<be|fe|shared> branch:<git-branch>
라인 2: claude-dashboard 출력(설치되어 있을 때만)

phases/index.json에서 첫 pending phase를 찾고, 그 phase의 첫 pending step을 표시.
없으면 마지막 completed step의 정보를 표시.

stdin payload는 claude-dashboard에 그대로 전달한다.
"""

import json
import os
import subprocess
import sys
from pathlib import Path


DASHBOARD_BASE = Path.home() / ".claude/plugins/cache/claude-dashboard/claude-dashboard"


AGENT_SHORT = {"backend": "be", "frontend": "fe", "both": "both", "shared": "sh"}


def _git_branch(cwd: str) -> str:
    r = subprocess.run(
        ["git", "rev-parse", "--abbrev-ref", "HEAD"],
        cwd=cwd, capture_output=True, text=True,
    )
    return r.stdout.strip() if r.returncode == 0 else "?"


def _project_root(cwd: str) -> Path:
    parts = Path(cwd).resolve().parts
    if ".worktrees" in parts:
        idx = parts.index(".worktrees")
        return Path(*parts[:idx])
    p = Path(cwd).resolve()
    while p != p.parent:
        if (p / "CLAUDE.md").exists() and (p / ".claude").exists():
            return p
        p = p.parent
    return Path(cwd).resolve()


def _phase_step_info(root: Path) -> tuple[str, str, str]:
    """
    (phase_dir, step_label, agent) — 진행 중 phase의 첫 pending step 우선.

    동작 원리 (위에서 아래로 평가):
      1. phases/index.json 순회. phase status='completed'인 phase는 건너뜀
         (다음 phase로 넘어감 — completed phase의 step을 표시하면 다음 진행을
         못 보여 줌).
      2. 진행 중 phase에서 첫 pending step 발견 → 표시 후 종료.
      3. 진행 중 phase에 pending이 없으면(예: 검토 게이트 패턴으로 일부만
         pending이고 나머지는 deferred) → 마지막 completed step에 ✓ 표시.
         (deferred는 무시 — '다음 라운드 대기'이지 마지막 진행 지점이 아님.)
      4. 모든 phase가 다 끝났으면 마지막 phase의 마지막 step에 ✓ 표시
         (전체 작업 종료 표시).
    """
    top = root / "phases" / "index.json"
    if not top.exists():
        return ("", "", "")
    try:
        idx = json.loads(top.read_text(encoding="utf-8"))
    except (json.JSONDecodeError, OSError):
        return ("", "", "")

    last_phase_with_steps: tuple[str, list] | None = None

    for ph in idx.get("phases", []):
        d = ph.get("dir")
        sub = root / "phases" / d / "index.json"
        if not sub.exists():
            continue
        try:
            sub_idx = json.loads(sub.read_text(encoding="utf-8"))
        except (json.JSONDecodeError, OSError):
            continue
        steps = sub_idx.get("steps", [])
        if not steps:
            continue
        last_phase_with_steps = (d, steps)

        # phase 단위 status가 completed면 다음 phase로
        if ph.get("status") == "completed":
            continue

        total = len(steps)

        # 진행 중 phase: 첫 pending step
        for s in steps:
            if s.get("status") == "pending":
                return (
                    d,
                    f"{s.get('step')}/{total - 1}",
                    AGENT_SHORT.get(s.get("agent", ""), "?"),
                )

        # pending 없음 (검토 게이트로 일부만 deferred 등) → 마지막 completed step
        completed = [s for s in steps if s.get("status") == "completed"]
        if completed:
            last = completed[-1]
            return (
                d,
                f"{last.get('step')}/{total - 1}✓",
                AGENT_SHORT.get(last.get("agent", ""), "?"),
            )
        # completed도 없음 (전부 deferred 등) → 다음 phase로

    # 모든 phase 다 봤는데 표시할 게 없음 → 마지막 phase의 마지막 step에 ✓
    if last_phase_with_steps is not None:
        d, steps = last_phase_with_steps
        total = len(steps)
        last = steps[-1]
        return (
            d,
            f"{last.get('step')}/{total - 1}✓",
            AGENT_SHORT.get(last.get("agent", ""), "?"),
        )
    return ("", "", "")


def _dashboard_entry() -> Path | None:
    if not DASHBOARD_BASE.exists():
        return None
    versions = [p for p in DASHBOARD_BASE.iterdir() if p.is_dir()]
    if not versions:
        return None
    latest = max(versions, key=lambda p: p.name)
    entry = latest / "dist" / "index.js"
    return entry if entry.exists() else None


def _dashboard_output(payload: str) -> str:
    entry = _dashboard_entry()
    if entry is None:
        return ""
    try:
        result = subprocess.run(
            ["node", str(entry)],
            input=payload,
            capture_output=True,
            text=True,
            timeout=2,
        )
    except (subprocess.TimeoutExpired, OSError):
        return ""
    return result.stdout.rstrip("\n") if result.returncode == 0 else ""


def main(argv: list[str] | None = None, stdin=None) -> int:
    cwd = os.environ.get("CLAUDE_PROJECT_DIR") or os.getcwd()
    root = _project_root(cwd)
    branch = _git_branch(cwd)
    phase, step_label, agent = _phase_step_info(root)

    src = stdin if stdin is not None else sys.stdin
    payload = "" if src.isatty() else src.read()

    if phase:
        line = f"phase:{phase} step:{step_label} agent:{agent} branch:{branch}"
    else:
        line = f"branch:{branch}"
    print(line)

    dash = _dashboard_output(payload)
    if dash:
        print(dash)
    return 0


if __name__ == "__main__":
    sys.exit(main())
