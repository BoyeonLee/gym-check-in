#!/usr/bin/env python3
"""
PostToolUse Edit/Write TDD 짝검사 hook.

backend의 internal/http, internal/domain, internal/repo, internal/auth, internal/batch
디렉토리에서 .go 파일이 추가/수정됐을 때, 같은 패키지에 대응하는 _test.go 파일도
함께 추가/수정됐는지 git diff --name-only로 확인한다.

테스트 짝이 없으면 stderr `BLOCKED: TDD — <파일> 변경에 대응하는 테스트가 없음` + exit 2.

예외:
- 파일 자체가 _test.go (테스트 파일을 만든 경우)
- backend/cmd/ (엔트리포인트는 단위 테스트 면제)
- db/migrations/, db/seeds/
- frontend/ — 백엔드 TDD 정책만 강제 (frontend는 pnpm test 통과만)
- shared step의 docs/ 등
"""

import json
import os
import subprocess
import sys
from pathlib import Path


TDD_REQUIRED_DIRS = (
    "backend/internal/http/",
    "backend/internal/domain/",
    "backend/internal/repo/",
    "backend/internal/auth/",
    "backend/internal/batch/",
)
EXEMPT_DIRS = (
    "backend/cmd/",
    "backend/internal/testutil/",
    "backend/internal/apperr/",  # 단순 정의
    "backend/internal/config/",  # 환경변수 로더는 자체 테스트보단 통합으로
)


def _is_required_go_file(rel: str) -> bool:
    if not rel.endswith(".go"):
        return False
    if rel.endswith("_test.go"):
        return False
    if any(rel.startswith(d) for d in EXEMPT_DIRS):
        return False
    return any(rel.startswith(d) for d in TDD_REQUIRED_DIRS)


def _expected_test_path(rel: str) -> str:
    """foo.go → foo_test.go (같은 디렉토리)."""
    base = rel[:-3]  # strip .go
    return f"{base}_test.go"


def _git_diff_names(cwd: str) -> list[str]:
    """git diff(staged + unstaged) + untracked 파일 목록을 합쳐 반환."""
    names: set[str] = set()
    for args in (
        ["diff", "--name-only", "HEAD"],
        ["ls-files", "--others", "--exclude-standard"],
    ):
        r = subprocess.run(
            ["git"] + args, cwd=cwd, capture_output=True, text=True
        )
        if r.returncode == 0:
            for line in r.stdout.splitlines():
                if line.strip():
                    names.add(line.strip())
    return sorted(names)


def _project_root_from_cwd(cwd: str) -> Path:
    """worktree 안이면 worktree 루트, 아니면 cwd 자체."""
    parts = Path(cwd).resolve().parts
    if ".worktrees" in parts:
        idx = parts.index(".worktrees")
        if idx + 2 <= len(parts):
            return Path(*parts[: idx + 2])
    return Path(cwd).resolve()


def check_tdd(cwd: str) -> list[str]:
    """현재 worktree의 변경 파일 중 TDD 짝이 없는 파일 목록 반환."""
    root = _project_root_from_cwd(cwd)
    names = _git_diff_names(str(root))
    if not names:
        return []
    changed_set = set(names)
    missing: list[str] = []
    for rel in names:
        if not _is_required_go_file(rel):
            continue
        expected = _expected_test_path(rel)
        if expected in changed_set:
            continue
        # 파일 자체가 테스트가 따로 있는 패키지면 패스(같은 패키지의 다른 _test.go 파일이 있는지)
        # 단순 정책: 해당 파일과 같은 디렉토리의 다른 _test.go 변경이 있으면 통과
        same_dir_test = any(
            other != rel
            and other.endswith("_test.go")
            and os.path.dirname(other) == os.path.dirname(rel)
            for other in changed_set
        )
        if same_dir_test:
            continue
        missing.append(rel)
    return missing


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
    cwd = payload.get("cwd") or os.getcwd()
    file_path = payload.get("tool_input", {}).get("file_path", "")

    # backend Go 파일 변경이 아니면 즉시 통과
    if file_path and not (file_path.endswith(".go") and "/backend/" in file_path):
        return 0

    missing = check_tdd(cwd)
    if missing:
        for m in missing:
            print(
                f"BLOCKED: TDD — {m} 변경에 대응하는 테스트({_expected_test_path(m)})가 없음 "
                f"(사유: backend는 TDD 정책 — Red → Green → Refactor)",
                file=sys.stderr,
            )
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main())
