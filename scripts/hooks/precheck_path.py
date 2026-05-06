#!/usr/bin/env python3
"""
PreToolUse Edit/Write 경로 제약 hook.

cwd로 현재 위치를 식별해 worktree boundary와 shared 정책을 강제한다.

매트릭스:
- .worktrees/backend/  → backend/**, db/**            (그 외 차단)
- .worktrees/frontend/ → frontend/**                  (그 외 차단)
- 메인 디렉토리(shared) → docs/**, scripts/**, .claude/**,
                          루트 CLAUDE.md, .env.example, docker-compose.yml,
                          .gitignore, phases/**       (그 외 차단)

표준 입력으로 hook payload(JSON)가 들어온다:
    { "tool_input": { "file_path": "..." }, "cwd": "...", ... }

도구 호출자(Edit/Write)는 절대 경로를 file_path로 넘긴다.
hook은 file_path가 cwd로 결정된 agent 영역의 허용 경로 안에 있는지 검증한다.
"""

import json
import os
import sys
from pathlib import Path


# 각 위치별 허용 prefix 목록(프로젝트 루트 기준 상대 경로).
# 빈 문자열은 루트 자체(예: 루트의 CLAUDE.md). 정확 매칭은 EXACT_FILES로.
BACKEND_ALLOWED = ("backend/", "db/")
FRONTEND_ALLOWED = ("frontend/",)
SHARED_ALLOWED = (
    "docs/",
    "scripts/",
    ".claude/",
    "phases/",
)
SHARED_EXACT = (
    "CLAUDE.md",
    ".env.example",
    "docker-compose.yml",
    ".gitignore",
    # 모듈별 정책 문서(코드 아님) — shared가 정책 갱신 시 만진다.
    "backend/CLAUDE.md",
    "frontend/CLAUDE.md",
    "db/CLAUDE.md",
)


def detect_agent(cwd: str) -> str:
    """cwd로부터 agent 식별. backend/frontend/shared 중 하나."""
    parts = Path(cwd).resolve().parts
    if ".worktrees" in parts:
        idx = parts.index(".worktrees")
        if idx + 1 < len(parts):
            wt = parts[idx + 1]
            if wt == "backend":
                return "backend"
            if wt == "frontend":
                return "frontend"
    return "shared"


def project_root(cwd: str) -> Path:
    """cwd로부터 프로젝트 루트(.worktrees/<x>의 부모, 또는 cwd 자체) 추정."""
    p = Path(cwd).resolve()
    parts = p.parts
    if ".worktrees" in parts:
        idx = parts.index(".worktrees")
        return Path(*parts[:idx])
    return p


def normalize_relpath(file_path: str, cwd: str) -> str | None:
    """file_path를 agent의 작업 루트 기준 상대 경로로 정규화.

    - backend/frontend agent의 경우 작업 루트는 .worktrees/<agent>/.
    - shared의 경우 작업 루트는 프로젝트 루트.

    상대 경로면 cwd 기준으로 절대로 변환 후 처리. 작업 루트 밖이면 None.
    """
    p = Path(file_path)
    if not p.is_absolute():
        p = (Path(cwd) / p).resolve()
    else:
        p = p.resolve()

    agent = detect_agent(cwd)
    cwd_parts = Path(cwd).resolve().parts
    if agent in ("backend", "frontend"):
        # 작업 루트 = .worktrees/<agent>
        if ".worktrees" not in cwd_parts:
            return None
        idx = cwd_parts.index(".worktrees")
        if idx + 2 > len(cwd_parts):
            return None
        work_root = Path(*cwd_parts[: idx + 2])
    else:
        work_root = project_root(cwd)

    try:
        rel = p.relative_to(work_root)
    except ValueError:
        return None
    return str(rel).replace(os.sep, "/")


def is_allowed(agent: str, relpath: str) -> bool:
    if agent == "backend":
        return any(relpath.startswith(prefix) for prefix in BACKEND_ALLOWED)
    if agent == "frontend":
        return any(relpath.startswith(prefix) for prefix in FRONTEND_ALLOWED)
    # shared
    if relpath in SHARED_EXACT:
        return True
    return any(relpath.startswith(prefix) for prefix in SHARED_ALLOWED)


def _read_payload(stream) -> dict:
    raw = stream.read()
    if not raw.strip():
        return {}
    try:
        return json.loads(raw)
    except json.JSONDecodeError:
        return {}


def main(argv: list[str] | None = None, stdin=None) -> int:
    stream = stdin if stdin is not None else sys.stdin
    payload = _read_payload(stream)
    file_path = payload.get("tool_input", {}).get("file_path", "")
    if not file_path:
        return 0

    cwd = payload.get("cwd") or os.getcwd()
    agent = detect_agent(cwd)
    rel = normalize_relpath(file_path, cwd)

    if rel is None:
        # 작업 루트 밖 — agent 영역을 벗어남
        print(
            f"BLOCKED: {agent} 작업 루트 밖의 파일을 변경할 수 없음 — {file_path} "
            f"(사유: harness.md 공유 파일 정책)",
            file=sys.stderr,
        )
        return 2

    if not is_allowed(agent, rel):
        print(
            f"BLOCKED: {agent}는 {rel}를 변경할 수 없음 "
            f"(사유: harness.md 공유 파일 정책 — Tier 1.2 매트릭스)",
            file=sys.stderr,
        )
        return 2

    return 0


if __name__ == "__main__":
    sys.exit(main())
