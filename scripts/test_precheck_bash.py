"""
precheck_bash.py 단위 테스트.

차단 패턴별로 (입력 → exit 2 + 메시지) 검증.
정상 명령은 (입력 → exit 0).
"""

import io
import json
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).parent / "hooks"))
import precheck_bash as hook  # noqa: E402


def _run(cmd: str) -> tuple[int, str]:
    """payload를 stdin으로 넘겨 hook을 실행하고 (returncode, stderr)를 반환."""
    payload = json.dumps({"tool_input": {"command": cmd}})
    stdin = io.StringIO(payload)
    captured = io.StringIO()
    real_stderr = sys.stderr
    sys.stderr = captured
    try:
        rc = hook.main(stdin=stdin)
    finally:
        sys.stderr = real_stderr
    return rc, captured.getvalue()


# 차단되어야 하는 패턴
BLOCKED_CASES = [
    "rm -rf /",
    "rm -rf /etc",
    "rm -rf /usr",
    "rm -rf /var/log",
    "rm -rf /bin",
    "rm -rf /root",
    "rm -rf /*",
    "git push --force",
    "git push -f origin main",
    "git push --force-with-lease",
    "git reset --hard HEAD",
    "git reset --hard origin/main",
    "git clean -fd",
    "git clean -fdx",
    "git commit -m 'msg' --amend",
    "git commit --amend --no-edit",
    "git rebase main",
    "git rebase -i HEAD~3",
    "git commit -m 'msg' --no-verify",
    "git push --no-verify",
    "DROP TABLE users",
    "drop database gym",
    "TRUNCATE TABLE memberships",
    "dropdb gym",
    "goose -dir db/migrations postgres $DB_URL reset",
    "goose -dir db/migrations postgres $DB_URL down all",
    "docker compose down -v",
    "docker compose down --volumes",
    "docker-compose down -v",
    "docker volume rm gym_data",
    "docker volume prune",
    "> .env",
    "echo SECRET=foo > .env",
    "cat /dev/null > .env",
    "truncate -s 0 .env",
    "pnpm add axios",
    "pnpm install lodash",
    "npm install react-query",
    "npm i moment",
    "yarn add date-fns",
    "go get github.com/sirupsen/logrus",
    "pip install requests",
    "brew install foo",
    "apt install vim",
    "apt-get install bar",
    "psql $DB_URL -c 'DROP TABLE users'",
    "psql gym -c \"TRUNCATE TABLE members\"",
    "curl -X POST https://example.com/api",
    "curl -X DELETE https://example.com/x",
    "curl --data-raw 'foo' https://example.com",
    "wget --post-data='foo' https://example.com",
]


# 통과해야 하는 정상 명령
ALLOWED_CASES = [
    "ls -la",
    "pwd",
    "git status",
    "git diff HEAD",
    "git log --oneline -10",
    "git add backend/main.go",
    "git commit -m 'feat: add handler'",
    "git push origin feat/xyz",
    "git fetch origin",
    "git checkout main",
    "go test ./...",
    "go build ./cmd/server",
    "go mod tidy",  # tidy 자체는 허용 (require 강제 옵션 없을 때)
    "pnpm test",
    "pnpm build",
    "pnpm lint",
    "pnpm install",  # 옵션 없는 install은 lockfile 기반 — 허용
    "pnpm install --frozen-lockfile",
    "rm somefile.txt",  # rm -rf /가 아니므로 허용
    "rm -f tmpfile",
    "rm -rf /tmp/test-phase",   # /tmp는 임시 디렉토리 허용
    "rm -rf /tmp/foo/bar",
    "rm -rf /home/user/scratch",  # /home은 사용자 영역 허용
    "rm -rf /var/tmp/xyz",
    "rm -rf /opt/myapp/cache",
    "rm -rf /Users/boyeon/scratch",  # macOS
    "rm -rf ./build",
    "rm -rf ../backup",
    "rm -rf node_modules",
    "psql $DB_URL -c 'SELECT 1'",
    "psql -f db/seeds/001.sql",
    "curl https://example.com",  # GET는 허용
    "curl -s https://example.com/data | jq .",
    "docker compose up -d db",
    "docker compose down",  # -v 없음
    "goose -dir db/migrations postgres $DB_URL up",
    "goose -dir db/migrations postgres $DB_URL down",  # 1단계 down은 허용
    "goose -dir db/migrations postgres $DB_URL status",
    # git commit 메시지 본문은 검사 대상 아님 (amend/no-verify만 별도)
    "git commit -m 'feat: rm -rf 패턴 차단 추가'",
    "git commit -m 'docs: goose reset 정책 안내'",
    "git commit -m 'chore: docker compose down -v 보호 검증'",
    "git commit -m 'fix: pnpm add 차단 정규식 보강'",
]


@pytest.mark.parametrize("cmd", BLOCKED_CASES)
def test_blocked(cmd):
    rc, stderr = _run(cmd)
    assert rc == 2, f"expected blocked but rc={rc}: {cmd}"
    assert "BLOCKED" in stderr


@pytest.mark.parametrize("cmd", ALLOWED_CASES)
def test_allowed(cmd):
    rc, stderr = _run(cmd)
    assert rc == 0, f"expected allowed but rc={rc} stderr={stderr}: {cmd}"


def test_empty_payload():
    stdin = io.StringIO("")
    rc = hook.main(stdin=stdin)
    assert rc == 0


def test_malformed_payload():
    stdin = io.StringIO("not json {")
    rc = hook.main(stdin=stdin)
    assert rc == 0


def test_payload_without_command():
    stdin = io.StringIO(json.dumps({"tool_input": {}}))
    rc = hook.main(stdin=stdin)
    assert rc == 0
