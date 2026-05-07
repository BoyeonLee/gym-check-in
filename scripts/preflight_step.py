#!/usr/bin/env python3
"""
preflight_step.py — step.md 명세가 docs/API.md 카탈로그와 정합한지 사전 검증.

목적: code-reviewer 게이트가 retry 루프를 만드는 가장 흔한 원인은 step.md에
적힌 에러 코드가 docs/API.md의 카탈로그에 등록되지 않은 경우다. 이 스크립트는
step 실행 전에 그 불일치를 잡아서 사용자가 명세부터 정합화하도록 한다.

검사 항목:
  1. step.md 본문의 백틱 인용(`CODE_LIKE_THIS`) 중 SCREAMING_SNAKE_CASE
     식별자를 에러 코드 후보로 추출.
  2. SQL 키워드·환경변수·HTTP 헤더명 등 명백한 비-에러-코드는 제외.
  3. docs/API.md "에러 코드 카탈로그" 표(`| `CODE` | HTTP | 설명 |`)에서
     코드 셋을 적재.
  4. 차집합(step.md에는 있는데 카탈로그에 없는 코드)을 stderr에 출력.

사용:
  python3 scripts/preflight_step.py phases/phase2-backend-scaffold
  python3 scripts/preflight_step.py phases/phase2-backend-scaffold/step3.md

종료 코드:
  0 = 모든 step이 카탈로그와 정합
  1 = 사용 오류(인자 누락, 파일 없음 등)
  2 = 미등록 코드 발견 (BLOCKED)

향후: execute.py가 step 시작 전에 자동 호출하도록 통합 가능. 현재는 수동.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

# 명백히 에러 코드가 아닌 토큰들. step.md에 SCREAMING_SNAKE 형태로 자주
# 등장하지만 카탈로그 검사 대상이 아닌 것들.
NON_ERROR_TOKENS = {
    # 환경변수 prefix들
    "JWT_ACCESS_SECRET", "JWT_REFRESH_SECRET", "DATABASE_URL", "TEST_DATABASE_URL",
    "CORS_ORIGIN", "SEED_ADMIN_USERNAME", "SEED_ADMIN_PASSWORD",
    "SEED_ADMIN_PASSWORD_HASH", "SEED_BRANCH_NAME", "SEED_BRANCH_ADDRESS",
    "APP_ENV", "PORT_NAME",
    # SQL 키워드/식별자
    "CURRENT_DATE", "TIME_ZONE", "ASIA_SEOUL", "SET_TIME_ZONE",
    "NOT_NULL_VIOLATION", "CHECK_VIOLATION", "FOREIGN_KEY_VIOLATION",
    "EXCLUSION_VIOLATION", "UNIQUE_VIOLATION",
    "FOR_UPDATE", "FOR_SHARE", "AT_TIME_ZONE",
    "PG_ADVISORY_LOCK", "MODE_CONDITION",
    # HTTP 헤더
    "X_REQUEST_ID", "X_FORWARDED_FOR", "STRICT_TRANSPORT_SECURITY",
    "ACCESS_CONTROL_ALLOW_ORIGIN", "ACCESS_CONTROL_ALLOW_METHODS",
    "ACCESS_CONTROL_ALLOW_HEADERS", "ACCESS_CONTROL_ALLOW_CREDENTIALS",
    "ACCESS_CONTROL_MAX_AGE",
    # Go 표준
    "GOOSE_MIGRATION", "BUILD_TAG",
}

# 백틱 안의 SCREAMING_SNAKE_CASE 추출. 길이 5 이상 + 언더스코어 1개 이상이면
# 에러 코드 후보로 본다(짧은 약어와 SQL 작은 키워드 제외).
ERROR_CODE_RE = re.compile(r"`([A-Z][A-Z0-9_]*)`")
CATALOG_ROW_RE = re.compile(r"^\|\s*`([A-Z][A-Z0-9_]+)`\s*\|", re.MULTILINE)


def extract_step_codes(text: str) -> set[str]:
    """step.md 본문에서 에러 코드 후보를 추출."""
    found: set[str] = set()
    for m in ERROR_CODE_RE.finditer(text):
        code = m.group(1)
        if len(code) < 5 or "_" not in code:
            continue
        if code in NON_ERROR_TOKENS:
            continue
        # 환경변수 패턴 prefix 제외
        if code.startswith(("JWT_", "SEED_", "DATABASE_", "TEST_", "CORS_",
                            "PORT_", "APP_", "GOOSE_")):
            continue
        found.add(code)
    return found


def extract_catalog_codes(api_md_text: str) -> set[str]:
    """docs/API.md의 카탈로그 표에서 에러 코드 셋을 추출."""
    return set(CATALOG_ROW_RE.findall(api_md_text))


def find_step_files(target: Path) -> list[Path]:
    if target.is_file():
        return [target]
    if target.is_dir():
        return sorted(target.glob("step*.md"))
    return []


def main(argv: list[str]) -> int:
    if len(argv) < 2:
        print("사용: preflight_step.py <phase 디렉토리 또는 step.md 경로>",
              file=sys.stderr)
        return 1

    target = Path(argv[1]).resolve()
    if not target.exists():
        print(f"경로를 찾을 수 없음: {target}", file=sys.stderr)
        return 1

    # 프로젝트 루트는 target 위로 올라가며 phases/ 디렉토리를 가진 곳.
    root = target
    while root.parent != root:
        if (root / "phases").is_dir() and (root / "docs" / "API.md").exists():
            break
        root = root.parent
    else:
        print("프로젝트 루트(phases/, docs/API.md)를 찾을 수 없음", file=sys.stderr)
        return 1

    api_md = root / "docs" / "API.md"
    catalog = extract_catalog_codes(api_md.read_text(encoding="utf-8"))

    steps = find_step_files(target)
    if not steps:
        print(f"검사할 step.md가 없음: {target}", file=sys.stderr)
        return 1

    issues: list[tuple[Path, str]] = []
    for step_path in steps:
        step_text = step_path.read_text(encoding="utf-8")
        for code in sorted(extract_step_codes(step_text)):
            if code not in catalog:
                issues.append((step_path, code))

    if issues:
        print("BLOCKED: step.md에 인용된 에러 코드가 docs/API.md 카탈로그에 없음",
              file=sys.stderr)
        for step_path, code in issues:
            rel = step_path.relative_to(root)
            print(f"  {rel}: `{code}` 미등록", file=sys.stderr)
        print(file=sys.stderr)
        print("해결: docs/API.md 카탈로그에 코드를 추가하거나 step.md에서 등록된 코드로 교체.",
              file=sys.stderr)
        return 2

    print(f"PASS: {len(steps)}개 step.md, {len(catalog)}개 카탈로그 코드 정합 확인.")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
