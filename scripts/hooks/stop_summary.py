#!/usr/bin/env python3
"""
Stop hook — Claude 세션 종료 시 변경 요약을 stderr로 출력.

자율 실행 중 invisible 변경을 사용자가 마지막에 한눈에 보도록 한다.
출력은 stderr이며 종료 코드는 항상 0(차단 아님).

stdin payload는 무시한다(세션 종료 시점이라 컨텍스트 불필요).
"""

import os
import subprocess
import sys


def _run_git(args: list[str], cwd: str) -> str:
    r = subprocess.run(
        ["git"] + args, cwd=cwd, capture_output=True, text=True
    )
    return r.stdout if r.returncode == 0 else ""


def main(argv: list[str] | None = None, stdin=None) -> int:
    cwd = os.getcwd()
    status = _run_git(["status", "--short"], cwd)
    diff_stat = _run_git(["diff", "--stat", "HEAD"], cwd)

    has_status = status.strip()
    has_diff = diff_stat.strip()

    if not has_status and not has_diff:
        # 변경 없음 — 조용히 종료
        return 0

    print("─" * 60, file=sys.stderr)
    print("[Stop hook] 세션 종료 — 변경 요약", file=sys.stderr)
    if has_status:
        print("\ngit status --short:", file=sys.stderr)
        print(status.rstrip(), file=sys.stderr)
    if has_diff:
        print("\ngit diff --stat HEAD:", file=sys.stderr)
        print(diff_stat.rstrip(), file=sys.stderr)
    print("─" * 60, file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
