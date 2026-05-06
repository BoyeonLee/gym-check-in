#!/usr/bin/env python3
"""
statusLine hook — 항상 보이는 진행 상황 한 줄.

표시 형식:
    phase:<dir> step:<N>/<total> agent:<be|fe|shared> branch:<git-branch>

phases/index.json에서 첫 pending phase를 찾고, 그 phase의 첫 pending step을 표시.
없으면 마지막 completed step의 정보를 표시.

stdin payload는 무시한다.
"""

import json
import os
import subprocess
import sys
from pathlib import Path


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
    """(phase_dir, step_label, agent) — 첫 pending 우선, 없으면 마지막 completed."""
    top = root / "phases" / "index.json"
    if not top.exists():
        return ("", "", "")
    try:
        idx = json.loads(top.read_text(encoding="utf-8"))
    except (json.JSONDecodeError, OSError):
        return ("", "", "")

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
        total = len(steps)
        for s in steps:
            if s.get("status") == "pending":
                return (
                    d,
                    f"{s.get('step')}/{total - 1}",
                    AGENT_SHORT.get(s.get("agent", ""), "?"),
                )
        # 모두 completed
        last = steps[-1]
        return (
            d,
            f"{last.get('step')}/{total - 1}✓",
            AGENT_SHORT.get(last.get("agent", ""), "?"),
        )
    return ("", "", "")


def main(argv: list[str] | None = None, stdin=None) -> int:
    cwd = os.environ.get("CLAUDE_PROJECT_DIR") or os.getcwd()
    root = _project_root(cwd)
    branch = _git_branch(cwd)
    phase, step_label, agent = _phase_step_info(root)

    if phase:
        line = f"phase:{phase} step:{step_label} agent:{agent} branch:{branch}"
    else:
        line = f"branch:{branch}"
    print(line)
    return 0


if __name__ == "__main__":
    sys.exit(main())
