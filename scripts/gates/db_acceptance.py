#!/usr/bin/env python3
"""
DB acceptance gate — 격리 DB에서 마이그레이션 왕복 + 스모크 + 시드 멱등을 검증한다.

Usage:
    python3 scripts/gates/db_acceptance.py phase1

전제:
- docker postgres가 떠 있고 .env의 DATABASE_URL로 superuser 접속 가능 (CREATE/DROP DATABASE 권한)
- goose, psql이 PATH에 있음
- .env의 SEED_* 값이 채워져 있음 (SEED_ADMIN_PASSWORD_HASH가 비어있으면 임시 더미 hash로 진행)

종료 코드:
    0 — PASS
    1 — FAIL (어떤 단계에서 실패)
    2 — 환경 문제 (미설치, .env 누락 등 — 사용자 개입 필요)
"""
from __future__ import annotations

import os
import re
import sys
import shutil
import secrets
import subprocess
from pathlib import Path
from urllib.parse import urlparse, urlunparse

ROOT = Path(__file__).resolve().parents[2]


# ---------- 출력 헬퍼 ----------

def _color(s: str, code: str) -> str:
    return f"\033[{code}m{s}\033[0m" if sys.stdout.isatty() else s


def ok(msg: str) -> None:
    print(_color(f"  ✓ {msg}", "32"))


def err(msg: str) -> None:
    print(_color(f"  ✗ {msg}", "31"), file=sys.stderr)


def info(msg: str) -> None:
    print(f"    {msg}")


def section(msg: str) -> None:
    print(_color(f"\n[{msg}]", "36"))


# ---------- env 로더 ----------

def load_env() -> dict[str, str]:
    """
    루트 .env를 단순 파싱. 큰따옴표·작은따옴표만 처리.
    `set -a; source .env`와 동등한 결과를 의도하지만 셸 스크립팅은 지원하지 않는다.
    """
    env_file = ROOT / ".env"
    if not env_file.exists():
        err(".env 없음 — 루트에 생성 후 재시도")
        sys.exit(2)

    out: dict[str, str] = {}
    for raw in env_file.read_text().splitlines():
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        if "=" not in line:
            continue
        k, v = line.split("=", 1)
        k = k.strip()
        v = v.strip()
        if (v.startswith('"') and v.endswith('"')) or (v.startswith("'") and v.endswith("'")):
            v = v[1:-1]
        out[k] = v
    return out


def replace_db(url: str, new_db: str) -> str:
    p = urlparse(url)
    return urlunparse(p._replace(path=f"/{new_db}"))


# ---------- 명령 실행 헬퍼 ----------

def run(cmd: list[str], capture: bool = True) -> subprocess.CompletedProcess:
    return subprocess.run(cmd, capture_output=capture, text=True, cwd=ROOT)


def psql_sql(url: str, sql: str) -> subprocess.CompletedProcess:
    """단일 SQL을 -tAc로 실행. stdout만 결과."""
    return run(["psql", url, "-v", "ON_ERROR_STOP=on", "-X", "-tAc", sql])


def psql_file(url: str, file: Path, vars: dict[str, str] | None = None) -> subprocess.CompletedProcess:
    cmd = ["psql", url, "-v", "ON_ERROR_STOP=on", "-X"]
    for k, v in (vars or {}).items():
        cmd += ["-v", f"{k}={v}"]
    cmd += ["-f", str(file)]
    return run(cmd)


def goose(url: str, action: str) -> subprocess.CompletedProcess:
    return run(["goose", "-dir", "db/migrations", "postgres", url, action])


# ---------- 사전 검사 ----------

def ensure_tools() -> None:
    missing = [t for t in ("psql", "goose") if not shutil.which(t)]
    if missing:
        err(f"PATH에 없음: {', '.join(missing)}")
        info('goose: export PATH="$HOME/go/bin:$PATH" 후 재시도')
        info("psql:  sudo apt install postgresql-client (또는 docker exec로 컨테이너 안에서 실행)")
        sys.exit(2)


def ensure_db_reachable(admin_url: str) -> None:
    r = psql_sql(admin_url, "select 1")
    if r.returncode != 0:
        err(f"DB 접속 실패 ({admin_url.split('@')[-1]}): {r.stderr.strip() or r.stdout.strip()}")
        info("docker compose up -d db 가 떠 있는지, .env의 DATABASE_URL이 맞는지 확인")
        sys.exit(2)


# ---------- 단계별 ----------

def step_create_iso_db(admin_url: str, acc_db: str) -> bool:
    r = psql_sql(admin_url, f'create database "{acc_db}"')
    if r.returncode != 0:
        err(f"CREATE DATABASE 실패: {r.stderr.strip() or r.stdout.strip()}")
        return False
    ok(f"격리 DB '{acc_db}' 생성")
    return True


def step_drop_iso_db(admin_url: str, acc_db: str) -> None:
    # 다른 세션이 붙어있을 수 있어 강제 종료 시도
    psql_sql(
        admin_url,
        f"select pg_terminate_backend(pid) from pg_stat_activity where datname = '{acc_db}'",
    )
    r = psql_sql(admin_url, f'drop database if exists "{acc_db}"')
    if r.returncode == 0:
        ok(f"격리 DB '{acc_db}' 드롭")
    else:
        err(f"DROP DATABASE 실패 (수동 정리 필요): {r.stderr.strip() or r.stdout.strip()}")
        info(f'정리: psql "$DATABASE_URL".../postgres -c \'drop database "{acc_db}"\'')


def step_migrations_roundtrip(acc_url: str) -> bool:
    n_migrations = len(list((ROOT / "db" / "migrations").glob("[0-9]*.sql")))
    if n_migrations == 0:
        err("db/migrations 안에 마이그레이션 파일 없음")
        return False

    r = goose(acc_url, "up")
    if r.returncode != 0:
        err(f"goose up 1회차 실패:\n{r.stdout}\n{r.stderr}")
        return False
    ok("up 1회차 성공")

    for i in range(n_migrations):
        r = goose(acc_url, "down")
        if r.returncode != 0:
            err(f"goose down {i+1}/{n_migrations} 실패:\n{r.stdout}\n{r.stderr}")
            return False
    ok(f"down {n_migrations}회 성공 (모든 마이그레이션 롤백)")

    r = goose(acc_url, "up")
    if r.returncode != 0:
        err(f"goose up 2회차 실패 (왕복 검증 fail):\n{r.stdout}\n{r.stderr}")
        return False
    ok("up 2회차 성공 (왕복 검증 통과)")
    return True


def step_smoke(acc_url: str) -> bool:
    smoke_file = ROOT / "scripts" / "gates" / "sql" / "phase1_smoke.sql"
    if not smoke_file.exists():
        err(f"{smoke_file} 없음")
        return False

    r = psql_file(acc_url, smoke_file)
    if r.returncode != 0:
        err("스모크 실패")
        if r.stdout.strip():
            print(r.stdout)
        if r.stderr.strip():
            print(r.stderr, file=sys.stderr)
        return False

    pass_lines = re.findall(r"NOTICE:\s+case \d+ PASS\b.*", r.stderr or "")
    if not pass_lines:
        err(f"PASS NOTICE 0건 — psql stderr를 확인:\n{r.stderr}")
        return False
    ok(f"스모크 {len(pass_lines)}/14 케이스 통과")
    for line in pass_lines:
        # "NOTICE:  case 1 PASS - 10 tables exist" → "case 1 PASS - 10 tables exist"
        info(line.split("NOTICE:", 1)[1].strip())

    if len(pass_lines) < 14:
        err(f"기대 14건이지만 {len(pass_lines)}건만 PASS")
        return False
    return True


def step_seed_idempotent(acc_url: str, env: dict[str, str]) -> bool:
    seed_file = ROOT / "db" / "seeds" / "001_admin_and_branch.sql"
    if not seed_file.exists():
        err(f"{seed_file} 없음")
        return False

    # 스모크에서 BEGIN/ROLLBACK 밖으로 commit된 row(예: case 14)를 청소.
    # 시드는 "빈 DB에서 row 1개씩 생성"을 검증하므로 사전 truncate 필요.
    cleanup_sql = (
        "truncate table "
        "check_ins, payments, membership_events, memberships, members, admins, branches, "
        "revoked_refresh_tokens, admin_audit_logs, idempotency_keys "
        "restart identity cascade"
    )
    r = psql_sql(acc_url, cleanup_sql)
    if r.returncode != 0:
        err(f"시드 전 truncate 실패: {r.stderr.strip() or r.stdout.strip()}")
        return False
    ok("시드 전 격리 DB 청소 (스모크 잔여 row 제거)")

    seed_vars = {
        "admin_username": env.get("SEED_ADMIN_USERNAME", ""),
        "admin_password_hash": env.get("SEED_ADMIN_PASSWORD_HASH", ""),
        "branch_name": env.get("SEED_BRANCH_NAME", ""),
        "branch_address": env.get("SEED_BRANCH_ADDRESS", ""),
    }

    if not seed_vars["admin_password_hash"]:
        # admins.password_hash CHECK 없음 → 더미 문자열로 진행 (게이트는 시드 SQL 적용 검증만)
        seed_vars["admin_password_hash"] = "smoke-dummy-not-real-hash"
        info("SEED_ADMIN_PASSWORD_HASH 비어있음 → 더미 문자열로 진행 (실 로그인 검증은 범위 밖)")

    missing = [k for k, v in seed_vars.items() if not v]
    if missing:
        err(f"시드 환경변수 누락: {', '.join(missing)}")
        return False

    for pass_n in (1, 2):
        r = psql_file(acc_url, seed_file, vars=seed_vars)
        if r.returncode != 0:
            err(f"시드 {pass_n}차 적용 실패:\n{r.stdout}\n{r.stderr}")
            return False
    ok("시드 1차 + 2차 적용 (psql 에러 없음)")

    checks = [
        (
            "select count(*) from admins where role='global' and deleted_at is null",
            "1",
            "global admin 1명",
        ),
        (
            "select count(*) from branches where deleted_at is null",
            "1",
            "active branch 1개",
        ),
        (
            "select must_change_password::text || '|' || (temp_password_expires_at is not null)::text "
            "from admins where role='global'",
            "true|true",
            "must_change_password=true & temp_password_expires_at 세팅",
        ),
    ]
    for sql, expected, label in checks:
        r = psql_sql(acc_url, sql)
        got = (r.stdout or "").strip()
        if got != expected:
            err(f"{label} 검증 실패: expected '{expected}', got '{got}'")
            return False
        ok(f"{label} (= {expected})")
    return True


# ---------- main ----------

def main(argv: list[str]) -> int:
    if len(argv) < 2 or argv[1] not in ("phase1",):
        err("Usage: python3 scripts/gates/db_acceptance.py phase1")
        return 2

    ensure_tools()
    env = load_env()

    base_url = env.get("DATABASE_URL", "")
    if not base_url:
        err("DATABASE_URL이 .env에 없음")
        return 2

    suffix = secrets.token_hex(4)
    acc_db = f"gym_acc_{suffix}"
    admin_url = replace_db(base_url, "postgres")
    acc_url = replace_db(base_url, acc_db)

    print(_color("== DB Acceptance Gate (phase1) ==", "1;36"))
    info(f"base     = {base_url.split('@')[-1]}")
    info(f"isolated = {acc_db}")

    section("0/4 사전 검사 (DB 접속)")
    ensure_db_reachable(admin_url)
    ok("DB 접속 OK")

    section("1/4 격리 DB 생성")
    if not step_create_iso_db(admin_url, acc_db):
        return 1

    rc = 1  # default fail
    try:
        section("2/4 goose up→down→up 왕복")
        if not step_migrations_roundtrip(acc_url):
            return 1

        section("3/4 스모크 검증 (14개 케이스)")
        if not step_smoke(acc_url):
            return 1

        section("4/4 시드 1차/2차 멱등성")
        if not step_seed_idempotent(acc_url, env):
            return 1

        rc = 0
    finally:
        section("정리")
        step_drop_iso_db(admin_url, acc_db)
        print()
        print(_color("== PASS ==", "1;32") if rc == 0 else _color("== FAIL ==", "1;31"))
    return rc


if __name__ == "__main__":
    sys.exit(main(sys.argv))
