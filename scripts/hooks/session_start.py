#!/usr/bin/env python3
"""
SessionStart hook — 새 Claude 세션이 시작될 때 현재 프로젝트 위치를 자동 주입.

Claude Code의 SessionStart hook output 규약:
- stdout이 그대로 시스템 메시지로 주입된다.
- stderr는 사용자 콘솔에만 표시.

표시 항목:
1. 현재 git branch
2. worktree 위치(있다면)
3. phases 진행 상황 — 첫 pending step과 완료/대기 카운트
"""

import json
import os
import subprocess
import sys
from pathlib import Path


def _git(args: list[str], cwd: str) -> str:
    r = subprocess.run(["git"] + args, cwd=cwd, capture_output=True, text=True)
    return r.stdout.strip() if r.returncode == 0 else ""


def _detect_worktree(cwd: str) -> str | None:
    parts = Path(cwd).resolve().parts
    if ".worktrees" in parts:
        idx = parts.index(".worktrees")
        if idx + 1 < len(parts):
            return parts[idx + 1]
    return None


def _project_root(cwd: str) -> Path:
    parts = Path(cwd).resolve().parts
    if ".worktrees" in parts:
        idx = parts.index(".worktrees")
        return Path(*parts[:idx])
    # cwd가 프로젝트 루트인지 확인 (CLAUDE.md 존재 여부)
    p = Path(cwd).resolve()
    while p != p.parent:
        if (p / "CLAUDE.md").exists() and (p / ".claude").exists():
            return p
        p = p.parent
    return Path(cwd).resolve()


def _phase_summary(root: Path) -> str:
    top = root / "phases" / "index.json"
    if not top.exists():
        return ""
    try:
        idx = json.loads(top.read_text(encoding="utf-8"))
    except (json.JSONDecodeError, OSError):
        return ""

    lines = []
    for ph in idx.get("phases", []):
        d = ph.get("dir")
        st = ph.get("status", "?")
        sub = root / "phases" / d / "index.json"
        if not sub.exists():
            lines.append(f"  - {d}: {st}")
            continue
        try:
            sub_idx = json.loads(sub.read_text(encoding="utf-8"))
        except (json.JSONDecodeError, OSError):
            lines.append(f"  - {d}: {st}")
            continue
        steps = sub_idx.get("steps", [])
        done = sum(1 for s in steps if s.get("status") == "completed")
        total = len(steps)
        first_pending = next(
            (s for s in steps if s.get("status") == "pending"), None
        )
        if first_pending:
            ag = first_pending.get("agent", "?")
            nm = first_pending.get("name", "?")
            lines.append(
                f"  - {d}: {st} ({done}/{total}) — 다음: step{first_pending.get('step')} [{ag}] {nm}"
            )
        else:
            lines.append(f"  - {d}: {st} ({done}/{total})")
    if not lines:
        return ""
    return "## Phase 진행 상황\n" + "\n".join(lines)


def main(argv: list[str] | None = None, stdin=None) -> int:
    cwd = os.environ.get("CLAUDE_PROJECT_DIR") or os.getcwd()
    root = _project_root(cwd)

    branch = _git(["rev-parse", "--abbrev-ref", "HEAD"], cwd)
    worktree = _detect_worktree(cwd)

    parts = ["## 프로젝트 위치"]
    parts.append(f"- 루트: {root}")
    if worktree:
        parts.append(f"- 현재 worktree: .worktrees/{worktree} (agent={worktree})")
    parts.append(f"- git branch: {branch or '(unknown)'}")

    phase_block = _phase_summary(root)
    if phase_block:
        parts.append("")
        parts.append(phase_block)

    parts.append("")
    parts.append(
        "## Reminder\n"
        "- 작업 시작 전 `harness.md` 워크플로우 확인.\n"
        "- 코드 변경은 step.md frontmatter의 agent 영역만(backend/frontend/db).\n"
        "- shared step에선 backend/·frontend/·db/ 수정 금지(hook이 차단)."
    )

    print("\n".join(parts))
    return 0


if __name__ == "__main__":
    sys.exit(main())
