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
# backend/frontend agent가 메인 프로젝트 루트(워크트리 밖)에서 만질 수 있는
# 경로. step.md 검증 절차상 자식 Claude가 자기 step의 status를 마크해야
# 하므로 phases/ 인덱스만 좁게 허용. docs/ · backend/(메인) 등은 여전히 차단.
BACKEND_MAIN_ALLOWED = ("phases/",)
FRONTEND_MAIN_ALLOWED = ("phases/",)
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


def normalize_relpath(file_path: str, cwd: str) -> tuple[str | None, str | None]:
    """file_path를 agent의 작업 루트 기준 상대 경로로 정규화.

    반환: (relpath, source). source는 "work"(워크트리 작업 루트 기준) 또는
    "main"(메인 프로젝트 루트 기준). backend/frontend agent는 워크트리가
    1차 작업 루트지만 phases/ 같은 공유 인덱스를 만지려면 메인 루트
    fallback이 필요해 두 단계 시도한다. shared는 항상 main.

    상대 경로면 cwd 기준으로 절대로 변환 후 처리. 둘 다 실패하면 (None, None).
    """
    p = Path(file_path)
    if not p.is_absolute():
        p = (Path(cwd) / p).resolve()
    else:
        p = p.resolve()

    agent = detect_agent(cwd)
    cwd_parts = Path(cwd).resolve().parts
    if agent in ("backend", "frontend"):
        # 1차: 워크트리 작업 루트 기준
        if ".worktrees" not in cwd_parts:
            return None, None
        idx = cwd_parts.index(".worktrees")
        if idx + 2 > len(cwd_parts):
            return None, None
        work_root = Path(*cwd_parts[: idx + 2])
        try:
            return str(p.relative_to(work_root)).replace(os.sep, "/"), "work"
        except ValueError:
            pass
        # 2차: 메인 프로젝트 루트 기준 (phases/ 인덱스 등 좁은 화이트리스트용)
        main_root = Path(*cwd_parts[:idx])
        try:
            return str(p.relative_to(main_root)).replace(os.sep, "/"), "main"
        except ValueError:
            return None, None

    # shared
    work_root = project_root(cwd)
    try:
        return str(p.relative_to(work_root)).replace(os.sep, "/"), "main"
    except ValueError:
        return None, None


def is_allowed(agent: str, relpath: str, source: str) -> bool:
    if agent == "backend":
        if source == "work":
            return any(relpath.startswith(prefix) for prefix in BACKEND_ALLOWED)
        # main
        return any(relpath.startswith(prefix) for prefix in BACKEND_MAIN_ALLOWED)
    if agent == "frontend":
        if source == "work":
            return any(relpath.startswith(prefix) for prefix in FRONTEND_ALLOWED)
        return any(relpath.startswith(prefix) for prefix in FRONTEND_MAIN_ALLOWED)
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
    rel, source = normalize_relpath(file_path, cwd)

    if rel is None:
        # 작업 루트 밖 — agent 영역을 벗어남
        print(
            f"BLOCKED: {agent} 작업 루트 밖의 파일을 변경할 수 없음 — {file_path} "
            f"(사유: harness.md 공유 파일 정책)",
            file=sys.stderr,
        )
        return 2

    if not is_allowed(agent, rel, source):
        print(
            f"BLOCKED: {agent}는 {rel}를 변경할 수 없음 "
            f"(사유: harness.md 공유 파일 정책 — Tier 1.2 매트릭스)",
            file=sys.stderr,
        )
        return 2

    return 0


if __name__ == "__main__":
    sys.exit(main())
