#!/usr/bin/env python3
"""
PreToolUse Bash 차단 hook.

Claude Code의 PreToolUse hook으로 등록되어, Bash 도구가 실행되기 직전에
명령 문자열을 검사한다. 위험한 패턴이 매칭되면 stderr에 메시지를 쓰고
exit 2로 도구 호출을 차단한다.

표준 입력으로 hook payload(JSON)가 들어온다. 형식:
    { "tool_input": { "command": "..." }, ... }

차단 패턴은 자율 실행 모드에서 Claude가 무심코 칠 수 있는 명령들을
하드 차단한다. Claude가 텍스트 규칙을 무시해도 hook은 실행을 막는다.
"""

import json
import re
import sys
from typing import Iterable


# (정규식, 사람용 사유) 쌍의 리스트.
# 각 패턴은 RAW 명령 문자열에 매칭되며, 매칭되면 차단한다.
PATTERNS: list[tuple[re.Pattern, str]] = [
    # 파괴적 파일 시스템 — 시스템 경로 강제 삭제. /tmp, /home, /var/tmp, /opt, /Users는 명시 통과.
    (
        re.compile(r"(^|[ &|;])rm\s+-rf?\s+/(?!tmp/|tmp\b|home/|home\b|var/tmp/|var/tmp\b|Users/|opt/)"),
        "rm -rf / 또는 시스템 경로 강제 삭제",
    ),

    # git 위험 명령
    (re.compile(r"git\s+push\s+(--force\b|-f\b)"), "git push --force / -f"),
    (re.compile(r"git\s+push\s+--force-with-lease"), "git push --force-with-lease"),
    (re.compile(r"git\s+reset\s+--hard"), "git reset --hard (working tree 강제 폐기)"),
    (re.compile(r"git\s+clean\s+-[fd]+"), "git clean -fd (untracked 파일 강제 삭제)"),
    (re.compile(r"git\s+commit\b[^|;&]*\s--amend\b"), "git commit --amend (CLAUDE.md 정책 위반: 항상 새 커밋)"),
    (re.compile(r"git\s+rebase\b"), "git rebase (히스토리 변경 — 명시 요청 시에만)"),
    (re.compile(r"--no-verify\b"), "--no-verify (commit/push 훅 우회 — CLAUDE.md 정책 위반)"),
    (re.compile(r"--no-gpg-sign\b"), "--no-gpg-sign (서명 우회)"),

    # DB 통째로 파괴
    (re.compile(r"DROP\s+(TABLE|DATABASE|SCHEMA)\b", re.IGNORECASE), "DROP TABLE/DATABASE/SCHEMA"),
    (re.compile(r"TRUNCATE\s+TABLE\b", re.IGNORECASE), "TRUNCATE TABLE"),
    (re.compile(r"\bdropdb\b"), "dropdb (PostgreSQL 데이터베이스 삭제)"),
    (re.compile(r"goose\b[^|;&]*\b(reset|down-to|down\s+all|down\s+-all)\b"), "goose reset / down all (마이그레이션 통째 롤백)"),

    # docker compose 데이터 삭제
    (re.compile(r"docker\s+compose\s+down\s+(-v\b|--volumes\b)"), "docker compose down -v (DB 볼륨 삭제 — 데이터 유실)"),
    (re.compile(r"docker-compose\s+down\s+(-v\b|--volumes\b)"), "docker-compose down -v (DB 볼륨 삭제)"),
    (re.compile(r"docker\s+volume\s+rm\b"), "docker volume rm"),
    (re.compile(r"docker\s+volume\s+prune\b"), "docker volume prune"),

    # .env 파일 비우기/덮어쓰기
    (re.compile(r"(^|[ &|;])>\s*\.env(\s|$)"), "> .env (시크릿 파일 비우기)"),
    (re.compile(r"(^|[ &|;])>\s*\.env\.[a-zA-Z]"), "> .env.* (시크릿 파일 비우기)"),
    (re.compile(r"echo\s+[^|;&]*>\s*\.env(\s|$)"), "echo ... > .env (시크릿 덮어쓰기)"),
    (re.compile(r"cat\s+/dev/null\s*>\s*\.env"), "cat /dev/null > .env"),
    (re.compile(r"truncate\s+-s\s+0\s+\.env"), "truncate -s 0 .env"),

    # ADR 외 라이브러리 추가 (whitelist 비교 어려우니 모든 install/add 차단)
    (re.compile(r"\bpnpm\s+(add|install)\s+[^-\s]"), "pnpm add/install (ADR 외 라이브러리 추가 — shared step에서 ADR 갱신 후 사용자 직접)"),
    (re.compile(r"\bnpm\s+(install|i)\s+[^-\s]"), "npm install (ADR 외 라이브러리 추가)"),
    (re.compile(r"\byarn\s+add\b"), "yarn add"),
    (re.compile(r"\bgo\s+get\s+[^-\s]"), "go get (ADR 외 모듈 추가 — shared step에서 ADR 갱신 후 사용자 직접)"),
    (re.compile(r"\bgo\s+mod\s+(edit|tidy|download)\b\s+(-require|--require)"), "go mod edit -require"),
    (re.compile(r"\bpip\s+install\b"), "pip install"),
    (re.compile(r"\bbrew\s+install\b"), "brew install"),
    (re.compile(r"\bapt(-get)?\s+install\b"), "apt install"),

    # psql 직접 DROP/TRUNCATE
    (re.compile(r"psql\b[^|;&]*-c\s+['\"][^'\"]*\bDROP\b", re.IGNORECASE), "psql -c '... DROP ...'"),
    (re.compile(r"psql\b[^|;&]*-c\s+['\"][^'\"]*\bTRUNCATE\b", re.IGNORECASE), "psql -c '... TRUNCATE ...'"),

    # 외부 호스트로 데이터 전송 (curl POST/PUT, wget --post-data)
    (re.compile(r"curl\b[^|;&]*\s(-X\s+(POST|PUT|PATCH|DELETE)|--data-urlencode|--data-raw|--data-binary)\b"), "curl POST/PUT/PATCH/DELETE 또는 --data 전송 (외부 호스트로 데이터 전송 위험)"),
    (re.compile(r"wget\b[^|;&]*\s--post-data\b"), "wget --post-data"),
]


_GIT_COMMIT_RE = re.compile(r"^\s*git\s+commit\b")
_DANGEROUS_COMMIT_OPTS = re.compile(r"\s(--amend|--no-verify|--no-gpg-sign)\b")


def check(command: str) -> tuple[bool, str]:
    """주어진 명령 문자열을 검사. (blocked, reason) 반환.

    `git commit -m "..."` 명령은 메시지 본문 안의 텍스트가 패턴에 매칭되어도
    실제로 명령이 실행되는 게 아니므로 무시한다. 단, --amend / --no-verify /
    --no-gpg-sign 옵션은 메시지 외부라서 검사 대상.
    """
    if _GIT_COMMIT_RE.match(command):
        m = _DANGEROUS_COMMIT_OPTS.search(command)
        if m:
            opt = m.group(1)
            return True, f"git commit {opt} (CLAUDE.md 정책 위반)"
        return False, ""

    for pat, reason in PATTERNS:
        if pat.search(command):
            return True, reason
    return False, ""


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
    cmd = payload.get("tool_input", {}).get("command", "")
    if not cmd:
        return 0

    blocked, reason = check(cmd)
    if blocked:
        print(f"BLOCKED: 위험한 명령 패턴 감지 — {reason}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main())
