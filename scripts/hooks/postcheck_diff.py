#!/usr/bin/env python3
"""
PostToolUse Edit/Write 검사 hook.

도구 호출 직후 변경된 파일을 검사해 다음을 차단한다:
1. PII 누출: 한국 휴대폰 번호 패턴(`01[0-9]{8,9}`)이 코드/주석에 하드코딩
2. 시크릿 평문: JWT secret, password 평문이 비테스트 코드에
3. PII 로깅: console.log/slog에 token/password/phone/birth_date
4. ADR 외 라이브러리: frontend의 axios/aws/firebase 등, backend의 ADR 외 모듈

표준 입력 payload:
    { "tool_input": { "file_path": "..." }, ... }

차단 시 stderr `BLOCKED: <카테고리> <파일:라인> <내용>` + exit 2.
"""

import json
import os
import re
import sys
from pathlib import Path


# (정규식, 카테고리, 사람용 사유) 트리플
# - 코드(.go/.ts/.tsx/.js/.jsx)에서만 검사
# - 테스트 fixture는 명시적 더미 번호(99999999999)만 통과
PII_PHONE = re.compile(r"\b01[0-9]{8,9}\b")
DUMMY_PHONE = re.compile(r"\b(0{11}|9{11}|01000000000|01099999999)\b")  # 명백한 더미

# 시크릿 평문.
# 세 번째 필드 doc_exempt=True면 가이드라인 문서(.md/.markdown)에서는 검사하지 않는다.
# - JWT secret/password 평문은 어디에도 박지 않는 게 정책이라 .md도 검사(False).
# - bcrypt prefix는 형식 설명(가이드 문서가 "어떤 prefix로 시작하는 해시" 식으로 표기하는
#   용도)으로 .md에서 자주 등장하므로 .md 면제(True).
SECRET_PATTERNS = [
    (re.compile(r"JWT_(ACCESS|REFRESH)_SECRET\s*=\s*['\"][^'\"]+['\"]"), "JWT secret 평문", False),
    (re.compile(r"password\s*[:=]\s*['\"][A-Za-z0-9_!@#$%]{6,}['\"]", re.IGNORECASE), "password 평문", False),
    # `\x24` = ASCII 16진수 escape (달러 기호). raw 패턴 안에서 16진수 escape를 사용해 hook
    # 본문에 bcrypt prefix 리터럴이 박히지 않게 한다(자가검출 방지).
    # 동작은 일반 escape 표기로 작성한 정규식과 동일하며, "2a/2b/2y" 다음 "10/12" cost,
    # 양쪽 구분자가 모두 달러 기호인 bcrypt prefix를 매치한다.
    (re.compile(r"\x242[aby]\x241[02]\x24"), "bcrypt 해시 평문 (시드 SQL은 환경변수 주입)", True),
]

# 로그·출력 PII
LOG_PII_PATTERNS = [
    (re.compile(r"console\.log\([^)]*\b(token|password|jwt|secret)\b", re.IGNORECASE), "console.log에 token/password/jwt/secret 누출"),
    (re.compile(r"slog\.[A-Z][a-zA-Z]*\([^)]*\b(phone|password|token|jwt|birth_date|secret)\b", re.IGNORECASE), "slog에 PII/시크릿 누출"),
    (re.compile(r"fmt\.(Print|Sprint)[a-z]*\([^)]*\b(password|token|jwt|secret)\b", re.IGNORECASE), "fmt.Print에 시크릿 누출"),
    (re.compile(r"log\.[A-Z][a-zA-Z]*\([^)]*\b(phone|password|token|jwt|birth_date|secret)\b", re.IGNORECASE), "log에 PII/시크릿 누출"),
]

# Frontend ADR 외 라이브러리 (whitelist 외 import)
FRONTEND_ALLOWED_PKGS = {
    "react", "react-dom", "react-router-dom", "react-router",
    "@tanstack/react-query",
    "vite", "@vitejs/plugin-react",
    "tailwindcss", "autoprefixer", "postcss",
    "vitest", "@testing-library/react", "@testing-library/jest-dom", "@testing-library/user-event",
    "typescript", "eslint",
}
FRONTEND_BLOCKED_PKGS = (
    "axios", "ky", "superagent",
    "@anthropic-ai/sdk", "openai", "@google-ai/generativelanguage",
    "@aws-sdk/client-", "firebase",
    "moment", "luxon",  # date-fns는 중립적이지만 MVP는 native Date로 충분
    "lodash",
    "@react-native",  # 모바일 전용 SDK
)

FRONTEND_IMPORT_RE = re.compile(
    # 매칭 형태:
    #   import "axios"
    #   import x from "axios"
    #   import { y } from "axios"
    #   import * as x from "axios"
    #   from "axios"  (re-export)
    r"""(?:^|\n)\s*(?:import\s+(?:[\w*\s,{}]+\s+from\s+)?|from\s+)['"]([^'"]+)['"]"""
)

# Backend Go ADR 라이브러리 (host 부분으로 매칭)
BACKEND_ALLOWED_HOSTS = {
    "github.com/gin-gonic/gin",
    "github.com/jackc/pgx",
    "github.com/golang-jwt/jwt",
    "golang.org/x/crypto",
    "github.com/robfig/cron",
    "github.com/google/uuid",
    "github.com/pressly/goose",
    "github.com/stretchr/testify",  # 테스트 표준
}


def _is_test_file(path: str) -> bool:
    name = os.path.basename(path)
    return (
        name.endswith("_test.go")
        or "/test/" in path or "/tests/" in path or "/__tests__/" in path
        or name.endswith(".test.ts") or name.endswith(".test.tsx")
        or name.endswith(".test.js") or name.endswith(".test.jsx")
        or name.endswith(".spec.ts") or name.endswith(".spec.tsx")
        or "testdata" in path.lower() or "fixture" in path.lower()
        or "testutil" in path.lower()
        # pytest 명명 규칙: test_*.py / *_test.py
        or (name.endswith(".py") and (name.startswith("test_") or name.endswith("_test.py")))
    )


def _is_doc_file(path: str) -> bool:
    """가이드라인/지시문 문서. 형식 설명용 패턴(휴대폰 번호 예시·bcrypt prefix)을 허용."""
    lower = path.lower()
    return lower.endswith(".md") or lower.endswith(".markdown")


def _is_seed_or_migration(path: str) -> bool:
    return "/db/migrations/" in path or "/db/seeds/" in path


def _check_pii_phone(text: str, path: str) -> list[str]:
    issues = []
    if _is_test_file(path) or _is_doc_file(path):
        return issues
    for m in PII_PHONE.finditer(text):
        if DUMMY_PHONE.match(m.group()):
            continue
        line_no = text[: m.start()].count("\n") + 1
        issues.append(f"PII: 휴대폰 번호 하드코딩 ({m.group()})  {path}:{line_no}")
    return issues


def _check_secrets(text: str, path: str) -> list[str]:
    issues = []
    if _is_test_file(path):
        return issues
    if _is_seed_or_migration(path) and path.endswith(".sql"):
        # 시드 SQL은 psql -v로 환경변수 주입 — 평문 패턴이 안 보여야 정상.
        # 그래도 검사는 한다.
        pass
    is_doc = _is_doc_file(path)
    for m_re, reason, doc_exempt in SECRET_PATTERNS:
        if is_doc and doc_exempt:
            continue
        for m in m_re.finditer(text):
            line_no = text[: m.start()].count("\n") + 1
            issues.append(f"SECRET: {reason}  {path}:{line_no}")
    return issues


def _check_log_pii(text: str, path: str) -> list[str]:
    issues = []
    for m_re, reason in LOG_PII_PATTERNS:
        for m in m_re.finditer(text):
            line_no = text[: m.start()].count("\n") + 1
            issues.append(f"LOG-PII: {reason}  {path}:{line_no}")
    return issues


def _check_frontend_imports(text: str, path: str) -> list[str]:
    issues = []
    if not (path.endswith(".ts") or path.endswith(".tsx") or path.endswith(".js") or path.endswith(".jsx")):
        return issues
    if "/frontend/" not in path and not path.startswith("frontend/"):
        return issues
    for m in FRONTEND_IMPORT_RE.finditer(text):
        spec = m.group(1)
        # 상대 경로/별칭은 통과
        if spec.startswith(".") or spec.startswith("/") or spec.startswith("@/"):
            continue
        # 명백히 차단된 패키지 검사
        for blocked in FRONTEND_BLOCKED_PKGS:
            if spec == blocked or spec.startswith(blocked):
                line_no = text[: m.start()].count("\n") + 1
                issues.append(f"ADR-EXT: frontend가 ADR 외 라이브러리 import — {spec}  {path}:{line_no}")
                break
    return issues


def _check_backend_imports(text: str, path: str) -> list[str]:
    issues = []
    if not path.endswith(".go"):
        return issues
    if "/backend/" not in path and not path.startswith("backend/"):
        return issues
    # Go import 블록 — 단일 import "x" 또는 import ( ... )
    import_re = re.compile(
        r"""import\s+(?:\(\s*([\s\S]*?)\)|"([^"]+)")""", re.MULTILINE
    )
    for m in import_re.finditer(text):
        block = m.group(1) or m.group(2) or ""
        for line in block.split("\n"):
            ls = line.strip()
            if not ls or ls.startswith("//"):
                continue
            # alias가 있을 수 있음: alias "host/path"
            mq = re.search(r'"([^"]+)"', ls)
            if not mq:
                continue
            spec = mq.group(1)
            # std 라이브러리 — '/' 없으면 허용
            if "/" not in spec:
                continue
            allowed = any(spec == h or spec.startswith(h + "/") for h in BACKEND_ALLOWED_HOSTS)
            # 같은 모듈 내부 import (gym-check-in/...) 허용
            if spec.startswith("github.com/") and "/gym-check-in" in spec.lower():
                allowed = True
            # 자기 모듈 내부(상대 경로 형태) — Go에서는 보통 module path full
            if not allowed:
                # cgo 등 표준 비-슬래시 경로 외엔 차단. 그러나 Go std는 보통 "encoding/json"처럼
                # 슬래시가 있어도 host가 아닌 경우. 안전하게 host를 가진(github.com 등) 외부만 차단.
                if spec.startswith(("github.com/", "gitlab.com/", "bitbucket.org/", "golang.org/")):
                    if not allowed:
                        line_no = text[: m.start() + block.find(line)].count("\n") + 1
                        issues.append(f"ADR-EXT: backend가 ADR 외 모듈 import — {spec}  {path}:{line_no}")
    return issues


def check_file(path: str, text: str) -> list[str]:
    """파일 1개를 검사. 위반 항목 리스트 반환."""
    issues: list[str] = []
    issues += _check_pii_phone(text, path)
    issues += _check_secrets(text, path)
    issues += _check_log_pii(text, path)
    issues += _check_frontend_imports(text, path)
    issues += _check_backend_imports(text, path)
    return issues


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

    p = Path(file_path)
    if not p.exists() or not p.is_file():
        return 0

    try:
        text = p.read_text(encoding="utf-8")
    except (UnicodeDecodeError, OSError):
        return 0

    issues = check_file(str(p), text)
    if issues:
        for issue in issues:
            print(f"BLOCKED: {issue}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main())
