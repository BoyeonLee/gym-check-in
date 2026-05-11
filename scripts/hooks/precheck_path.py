#!/usr/bin/env python3
"""
PreToolUse Edit/Write 경로 제약 hook.

agent 식별은 file_path를 우선, cwd를 보조로 사용해 worktree boundary와
shared 정책을 강제한다.

매트릭스 (B 방안 — 책임 분리):
- .worktrees/backend/  → backend/**, db/**             (phases/ 포함 그 외 차단)
- .worktrees/frontend/ → frontend/**                   (phases/ 포함 그 외 차단)
- 메인 디렉토리(shared) → docs/**, scripts/**, .claude/**,
                          루트 CLAUDE.md, .env.example, docker-compose.yml,
                          .gitignore, phases/**        (그 외 차단)

자식 Claude(backend/frontend agent)는 phases/ 메타를 만질 수 없다 —
status·summary·timestamp는 execute.py가 main 인덱스에 직접 박는다(B 방안,
acceptance + code-reviewer gate 결과 기준).

agent 식별 우선순위(2026-05-11 보강):
1. file_path가 .worktrees/<agent>/... 안이면 그 agent 정책 적용 (cwd 무시).
   메인 세션이 worktree path를 직접 만지거나 backend-engineer subagent가
   메인 cwd를 상속받아 호출돼도 같은 path는 같은 정책으로 검증된다.
   (이 보강 전엔 subagent가 차단당해 Bash heredoc으로 우회하는 패턴이
   반복됨 — Phase 2 postaudit에서 발생.)
2. cwd가 .worktrees/<agent>/... 안이면 그 agent 정책 (자식 Claude 일반 흐름).
3. 둘 다 worktree 밖이면 shared.

표준 입력으로 hook payload(JSON)가 들어온다:
    { "tool_input": { "file_path": "..." }, "cwd": "...", ... }

도구 호출자(Edit/Write)는 절대 경로를 file_path로 넘긴다.
hook은 file_path가 결정된 agent 영역의 허용 경로 안에 있는지 검증한다.
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
# 경로. B 방안(책임 분리, 2026-05-08) 이후로 자식 Claude는 phases/ 메타를
# 만지지 않는다 — status·summary는 execute.py가 acceptance + code-reviewer
# gate 결과로 main 인덱스에 직접 기록한다. 자식이 옛 패턴으로 phases를
# 만지려 해도 hook이 차단해 자가 교정으로 유도. 또한 worktree 안의
# phases/도 BACKEND_ALLOWED/FRONTEND_ALLOWED에 없으므로 자동 차단된다.
BACKEND_MAIN_ALLOWED: tuple[str, ...] = ()
FRONTEND_MAIN_ALLOWED: tuple[str, ...] = ()
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


def _agent_from_parts(parts) -> str | None:
    """경로 parts에서 .worktrees/<agent>/ 패턴을 찾아 agent 이름 반환."""
    if ".worktrees" in parts:
        idx = parts.index(".worktrees")
        if idx + 1 < len(parts):
            wt = parts[idx + 1]
            if wt == "backend":
                return "backend"
            if wt == "frontend":
                return "frontend"
    return None


def detect_agent(cwd: str, file_path: str | None = None) -> str:
    """agent 식별. file_path 우선, 그 다음 cwd, 둘 다 worktree 밖이면 shared.

    이 우선순위가 중요한 이유: 메인 세션이 backend-engineer subagent를 호출하면
    subagent의 cwd는 메인을 상속받지만 file_path는 .worktrees/backend/... 일 수
    있다. cwd만 보면 shared로 잘못 분류되어 hook이 차단되고, subagent가
    Bash heredoc(`cat > file <<EOF`)으로 우회하는 패턴이 반복된다(2026-05-11
    Phase 2 postaudit에서 발생). file_path를 우선 보면 메인 세션·subagent
    모두 같은 path는 같은 agent 정책으로 검증된다.
    """
    if file_path:
        p = Path(file_path)
        if not p.is_absolute():
            p = (Path(cwd) / p).resolve()
        else:
            p = p.resolve()
        a = _agent_from_parts(p.parts)
        if a is not None:
            return a
    a = _agent_from_parts(Path(cwd).resolve().parts)
    return a if a is not None else "shared"


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

    file_path가 .worktrees/<agent>/... 안이면 cwd와 무관하게 그 worktree
    기준으로 정규화된다 — 메인 세션이 worktree path를 직접 만지거나
    backend-engineer subagent가 메인 cwd를 상속받아 호출돼도 같은 path는
    같은 정책으로 검증된다.
    """
    p = Path(file_path)
    if not p.is_absolute():
        p = (Path(cwd) / p).resolve()
    else:
        p = p.resolve()

    # 1순위: file_path가 worktree 안이면 그 worktree 기준
    p_parts = p.parts
    if ".worktrees" in p_parts:
        idx = p_parts.index(".worktrees")
        if idx + 2 <= len(p_parts):
            work_root = Path(*p_parts[: idx + 2])
            try:
                return str(p.relative_to(work_root)).replace(os.sep, "/"), "work"
            except ValueError:
                return None, None

    # 2순위: cwd가 worktree 안이면 (기존 로직 — 자식 Claude가 worktree에서 작업)
    agent_from_cwd = _agent_from_parts(Path(cwd).resolve().parts)
    cwd_parts = Path(cwd).resolve().parts
    if agent_from_cwd in ("backend", "frontend"):
        idx = cwd_parts.index(".worktrees")
        if idx + 2 > len(cwd_parts):
            return None, None
        work_root = Path(*cwd_parts[: idx + 2])
        try:
            return str(p.relative_to(work_root)).replace(os.sep, "/"), "work"
        except ValueError:
            pass
        # 메인 프로젝트 루트 fallback (phases/ 인덱스 등 좁은 화이트리스트용)
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
    agent = detect_agent(cwd, file_path)
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
