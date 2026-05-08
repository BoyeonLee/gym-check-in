#!/usr/bin/env python3
"""
Harness Step Executor — phase 내 step을 순차/병렬 실행하고 자가 교정한다.

step.md 첫 줄에 frontmatter(`---`)가 있으면 `agent`(backend|frontend|both|shared)
필드를 읽어 worktree와 가드레일을 분기한다. 같은 `parallel_group`을 가진 연속
step들은 worktree에서 동시 실행된다.

Usage:
    python3 scripts/execute.py <phase-dir> [--push]
"""

import argparse
import contextlib
import json
import os
import re
import subprocess
import sys
import threading
import time
import types
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone, timedelta
from itertools import groupby
from pathlib import Path
from typing import Optional

ROOT = Path(__file__).resolve().parent.parent

VALID_AGENTS = {"backend", "frontend", "both", "shared"}

WORKTREE_DIR_NAME = ".worktrees"
AGENT_TO_WORKTREE = {"backend": "backend", "frontend": "frontend"}
AGENT_TO_BRANCH_SUFFIX = {"backend": "be", "frontend": "fe"}


@contextlib.contextmanager
def progress_indicator(label: str):
    """터미널 진행 표시기. with 문으로 사용하며 .elapsed 로 경과 시간을 읽는다."""
    frames = "◐◓◑◒"
    stop = threading.Event()
    t0 = time.monotonic()

    def _animate():
        idx = 0
        while not stop.wait(0.12):
            sec = int(time.monotonic() - t0)
            sys.stderr.write(f"\r{frames[idx % len(frames)]} {label} [{sec}s]")
            sys.stderr.flush()
            idx += 1
        sys.stderr.write("\r" + " " * (len(label) + 20) + "\r")
        sys.stderr.flush()

    th = threading.Thread(target=_animate, daemon=True)
    th.start()
    info = types.SimpleNamespace(elapsed=0.0)
    try:
        yield info
    finally:
        stop.set()
        th.join()
        info.elapsed = time.monotonic() - t0


def _parse_frontmatter(text: str) -> tuple[dict, str]:
    """step.md 본문에서 YAML 풍 frontmatter를 추출한다.

    `---`로 둘러싸인 첫 블록만 본다. 지원 키:
        agent: backend|frontend|both|shared
        depends_on: [a, b, c]
        parallel_group: 1
    PyYAML 의존을 피하기 위해 단순 라인 파서 사용.
    잘못된 형식이면 빈 dict + 원문 반환.
    """
    if not text.startswith("---"):
        return {}, text
    lines = text.split("\n")
    end = None
    for i in range(1, len(lines)):
        if lines[i].strip() == "---":
            end = i
            break
    if end is None:
        return {}, text
    meta: dict = {}
    for raw in lines[1:end]:
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        m = re.match(r"^([A-Za-z_][A-Za-z0-9_]*)\s*:\s*(.+)$", line)
        if not m:
            continue
        key, val = m.group(1), m.group(2).strip()
        if val.startswith("[") and val.endswith("]"):
            inner = val[1:-1].strip()
            meta[key] = [v.strip().strip("'\"") for v in inner.split(",") if v.strip()]
        elif val.lower() in ("true", "false"):
            meta[key] = val.lower() == "true"
        elif val.isdigit() or (val.startswith("-") and val[1:].isdigit()):
            meta[key] = int(val)
        else:
            meta[key] = val.strip("'\"")
    body = "\n".join(lines[end + 1:])
    return meta, body


class StepExecutor:
    """Phase 디렉토리 안의 step들을 순차/병렬 실행하는 하네스."""

    MAX_RETRIES = 3
    MAX_TURNS = 60
    TIMEOUT_SEC = 1800
    FEAT_MSG = "feat({phase}): step {num} — {name}"
    CHORE_MSG = "chore({phase}): step {num} output"
    TZ = timezone(timedelta(hours=9))

    def __init__(self, phase_dir_name: str, *, auto_push: bool = False, dry_run: bool = False):
        self._root = str(ROOT)
        self._phases_dir = ROOT / "phases"
        self._phase_dir = self._phases_dir / phase_dir_name
        self._phase_dir_name = phase_dir_name
        self._top_index_file = self._phases_dir / "index.json"
        self._auto_push = auto_push
        self._dry_run = dry_run

        if not self._phase_dir.is_dir():
            print(f"ERROR: {self._phase_dir} not found")
            sys.exit(1)

        self._index_file = self._phase_dir / "index.json"
        if not self._index_file.exists():
            print(f"ERROR: {self._index_file} not found")
            sys.exit(1)

        idx = self._read_json(self._index_file)
        self._project = idx.get("project", "project")
        self._phase_name = idx.get("phase", phase_dir_name)
        self._total = len(idx["steps"])

    def run(self):
        self._print_header()
        if self._dry_run:
            self._validate_step_files()
            self._dry_run_report()
            return
        self._check_clean_tree()
        self._check_blockers()
        self._validate_step_files()
        self._run_preflight()
        agents_used = self._collect_agents()
        self._ensure_branches_and_worktrees(agents_used)
        self._ensure_created_at()
        self._execute_all_steps()
        self._finalize()

    def _run_preflight(self):
        """step.md ↔ docs/API.md 카탈로그 사전 정합성 검증.

        scripts/preflight_step.py를 호출해 step.md에 인용된 에러 코드가
        API.md 카탈로그에 모두 등록되어 있는지 확인. 누락 시 stderr에
        명시 + sys.exit(2)로 즉시 종료해 retry 루프에 빠지기 전에 사용자가
        명세를 정합화하도록 한다.

        스크립트가 없으면(외부 환경 등) 경고만 출력하고 진행 — 게이트는
        block이 아니라 best-effort.
        """
        preflight = ROOT / "scripts" / "preflight_step.py"
        if not preflight.exists():
            print("  WARN: scripts/preflight_step.py 없음 — 사전 정합성 검증 건너뜀")
            return
        r = subprocess.run(
            ["python3", str(preflight), str(self._phase_dir)],
            capture_output=True, text=True,
        )
        if r.returncode == 0:
            # PASS 줄 한 줄만 짧게 출력.
            if r.stdout.strip():
                print(f"  ✓ preflight: {r.stdout.strip().splitlines()[0]}")
            return
        # 미등록 코드 발견 또는 사용 오류.
        print()
        print("  ✗ preflight 실패 — 명세-카탈로그 불일치 발견:")
        for line in (r.stderr or "").strip().splitlines():
            print(f"    {line}")
        print()
        print("  해결: docs/API.md에 코드를 추가하거나 step.md에서 등록된 코드로 교체 후 재실행.")
        sys.exit(2)

    def _dry_run_report(self):
        """실제 Claude 호출 없이 실행 계획만 출력."""
        index = self._read_json(self._index_file)
        steps = index.get("steps", [])

        print("\n[DRY-RUN] 실제 Claude 호출 없이 실행 계획만 출력합니다.\n")

        # parallel_group 그룹화
        from itertools import groupby

        order_idx = 0
        for s in steps:
            num = s["step"]
            name = s["name"]
            agent = self._agent_for_step(s)
            status = s.get("status", "pending")
            pg = s.get("parallel_group")
            cwd = self._cwd_for_step(agent)
            wt_label = f".worktrees/{AGENT_TO_WORKTREE[agent]}" if agent in AGENT_TO_WORKTREE else "메인"

            # 매트릭스 영역
            if agent == "backend":
                allowed = "backend/, db/"
            elif agent == "frontend":
                allowed = "frontend/"
            elif agent == "both":
                allowed = "backend/, db/ + frontend/"
            else:
                allowed = "docs/, scripts/, .claude/, 모듈 CLAUDE.md, 루트 설정"

            tag = f"[{status:>9}] step{num} ({name})"
            if pg is not None:
                tag += f"  ⇉ parallel_group={pg}"
            print(f"  {tag}")
            print(f"      agent={agent}  cwd={wt_label}  허용 영역={allowed}")
            order_idx += 1

        # 가드레일 파일 목록
        agents_used = self._collect_agents()
        if agents_used:
            print(f"\n  worktree 생성 대상: {sorted(agents_used)}")
        print(f"\n  Phase 완료 시 BE→FE 순으로 main에 merge.")
        print(f"  hook 매트릭스: scripts/hooks/precheck_path.py 참조.")

        # 실행 명령 (참고)
        print(f"\n  실제 실행: python3 scripts/execute.py {self._phase_dir_name}")

    def _validate_step_files(self):
        """모든 step.md frontmatter 검증.

        - agent 값이 VALID_AGENTS 안에 있어야 함
        - depends_on 항목이 같은 phase 안에 실제로 존재하는 step name이어야 함
        - 같은 parallel_group 안의 step들은 agent가 서로 달라야 함
        """
        index = self._read_json(self._index_file)
        steps = index.get("steps", [])
        names = {s.get("name") for s in steps}

        errors: list[str] = []
        groups: dict[int, list[str]] = {}  # parallel_group → [agent, ...]

        for s in steps:
            num = s.get("step")
            name = s.get("name", f"step{num}")
            step_file = self._phase_dir / f"step{num}.md"

            # 1. agent 값 검증 (raw 값 기준 — fallback 우회)
            raw_agent = s.get("agent")
            if raw_agent is None and step_file.exists():
                meta, _ = _parse_frontmatter(step_file.read_text(encoding="utf-8"))
                raw_agent = meta.get("agent")
            if raw_agent is not None and raw_agent not in VALID_AGENTS:
                errors.append(
                    f"step{num} ({name}): agent='{raw_agent}'가 유효하지 않음 (허용: {sorted(VALID_AGENTS)})"
                )

            # _agent_for_step의 결과 (fallback 적용 후)는 parallel_group 검증에서 사용
            agent = self._agent_for_step(s)

            # 2. depends_on 검증 (frontmatter에서)
            if step_file.exists():
                meta, _ = _parse_frontmatter(step_file.read_text(encoding="utf-8"))
                deps = meta.get("depends_on", [])
                if isinstance(deps, list):
                    for d in deps:
                        if d not in names:
                            errors.append(
                                f"step{num} ({name}): depends_on '{d}'가 같은 phase 안에 존재하지 않음"
                            )

            # 3. parallel_group 수집
            pg = s.get("parallel_group")
            if pg is not None:
                groups.setdefault(pg, []).append(agent)

        # parallel_group 안의 agent 중복 검증
        for pg, agents in groups.items():
            non_shared = [a for a in agents if a in ("backend", "frontend")]
            if len(non_shared) != len(set(non_shared)):
                errors.append(
                    f"parallel_group={pg}: 같은 worktree agent가 중복 — "
                    f"agents={agents} (같은 worktree를 동시 사용하면 충돌)"
                )

        if errors:
            print("\n  ERROR: step 파일 검증 실패")
            for e in errors:
                print(f"    - {e}")
            sys.exit(1)

    # --- timestamps ---

    def _stamp(self) -> str:
        return datetime.now(self.TZ).strftime("%Y-%m-%dT%H:%M:%S%z")

    # --- JSON I/O ---

    @staticmethod
    def _read_json(p: Path) -> dict:
        return json.loads(p.read_text(encoding="utf-8"))

    @staticmethod
    def _write_json(p: Path, data: dict):
        p.write_text(json.dumps(data, indent=2, ensure_ascii=False), encoding="utf-8")

    # --- git ---

    def _run_git(self, *args, cwd: Optional[str] = None) -> subprocess.CompletedProcess:
        cmd = ["git"] + list(args)
        return subprocess.run(cmd, cwd=cwd or self._root, capture_output=True, text=True)

    def _check_clean_tree(self):
        """메인 worktree가 dirty면 즉시 종료. worktree 분기 전에 호출."""
        r = self._run_git("status", "--porcelain")
        if r.returncode != 0:
            print("  ERROR: git을 사용할 수 없거나 git repo가 아닙니다.")
            print(f"  {r.stderr.strip()}")
            sys.exit(1)
        if r.stdout.strip():
            print("\n  ERROR: working tree가 dirty합니다. commit 또는 stash 후 재실행하세요.")
            print(r.stdout)
            sys.exit(1)

    def _agent_for_step(self, step: dict) -> str:
        """step의 agent 결정: index.json의 agent 필드 우선, 없으면 step.md frontmatter.

        둘 다 없으면 'shared'.
        """
        if step.get("agent") in VALID_AGENTS:
            return step["agent"]
        step_file = self._phase_dir / f"step{step['step']}.md"
        if step_file.exists():
            meta, _ = _parse_frontmatter(step_file.read_text(encoding="utf-8"))
            agent = meta.get("agent")
            if agent in VALID_AGENTS:
                return agent
        return "shared"

    def _collect_agents(self) -> set[str]:
        index = self._read_json(self._index_file)
        agents = {self._agent_for_step(s) for s in index["steps"]}
        # both 는 backend·frontend 둘 다 worktree가 필요
        if "both" in agents:
            agents.update({"backend", "frontend"})
            agents.discard("both")
        agents.discard("shared")
        return agents

    def _worktree_path(self, agent: str) -> Path:
        return ROOT / WORKTREE_DIR_NAME / AGENT_TO_WORKTREE[agent]

    def _branch_for(self, agent: str) -> str:
        suffix = AGENT_TO_BRANCH_SUFFIX[agent]
        return f"feat/{self._phase_name}-{suffix}"

    def _ensure_branches_and_worktrees(self, agents: set[str]):
        if not agents:
            return  # 모든 step이 shared — worktree 불필요
        worktrees_root = ROOT / WORKTREE_DIR_NAME
        worktrees_root.mkdir(exist_ok=True)
        for agent in sorted(agents):
            wt_path = self._worktree_path(agent)
            branch = self._branch_for(agent)
            if wt_path.is_dir() and (wt_path / ".git").exists():
                # 이미 등록된 worktree — fast-forward 시도
                self._run_git("fetch", "origin", cwd=str(wt_path))
                continue
            # worktree 등록
            r = self._run_git("rev-parse", "--verify", branch)
            if r.returncode == 0:
                args = ("worktree", "add", str(wt_path), branch)
            else:
                args = ("worktree", "add", "-b", branch, str(wt_path))
            r = self._run_git(*args)
            if r.returncode != 0:
                print(f"  ERROR: worktree '{wt_path}' 생성 실패")
                print(f"  {r.stderr.strip()}")
                sys.exit(1)
            print(f"  Worktree: {wt_path} → {branch}")

        # 모든 worktree에 settings.local.json을 동기화 — main의 .claude/settings.json을
        # 그대로 복사해 hook(precheck_bash, precheck_path 등)이 worktree 안에서도 동작.
        self._sync_worktree_settings(agents)

    def _sync_worktree_settings(self, agents: set[str]):
        """각 worktree의 .claude/settings.local.json에 main settings를 복사."""
        src = ROOT / ".claude" / "settings.json"
        if not src.exists():
            return
        for agent in agents:
            if agent not in AGENT_TO_WORKTREE:
                continue
            wt_claude = self._worktree_path(agent) / ".claude"
            wt_claude.mkdir(parents=True, exist_ok=True)
            dest = wt_claude / "settings.local.json"
            dest.write_text(src.read_text(encoding="utf-8"), encoding="utf-8")

    def _commit_step(self, step_num: int, step_name: str, *, cwd: Optional[str] = None):
        cwd = cwd or self._root
        # output 파일과 index.json은 메인의 phases/ 에만 있고 worktree에는 동기화되지 않을 수 있다.
        # 하지만 worktree 안에서도 phases/{phase_dir}/index.json 경로가 의미 있도록 처리.
        output_rel = f"phases/{self._phase_dir_name}/step{step_num}-output.json"
        index_rel = f"phases/{self._phase_dir_name}/index.json"

        self._run_git("add", "-A", cwd=cwd)
        self._run_git("reset", "HEAD", "--", output_rel, cwd=cwd)
        self._run_git("reset", "HEAD", "--", index_rel, cwd=cwd)

        if self._run_git("diff", "--cached", "--quiet", cwd=cwd).returncode != 0:
            msg = self.FEAT_MSG.format(phase=self._phase_name, num=step_num, name=step_name)
            r = self._run_git("commit", "-m", msg, cwd=cwd)
            if r.returncode == 0:
                print(f"  Commit: {msg}  (in {cwd})")
            else:
                print(f"  WARN: 코드 커밋 실패: {r.stderr.strip()}")

        self._run_git("add", "-A", cwd=cwd)
        if self._run_git("diff", "--cached", "--quiet", cwd=cwd).returncode != 0:
            msg = self.CHORE_MSG.format(phase=self._phase_name, num=step_num)
            r = self._run_git("commit", "-m", msg, cwd=cwd)
            if r.returncode != 0:
                print(f"  WARN: housekeeping 커밋 실패: {r.stderr.strip()}")

    # --- top-level index ---

    def _update_top_index(self, status: str):
        if not self._top_index_file.exists():
            return
        top = self._read_json(self._top_index_file)
        ts = self._stamp()
        for phase in top.get("phases", []):
            if phase.get("dir") == self._phase_dir_name:
                phase["status"] = status
                ts_key = {"completed": "completed_at", "error": "failed_at", "blocked": "blocked_at"}.get(status)
                if ts_key:
                    phase[ts_key] = ts
                break
        self._write_json(self._top_index_file, top)

    # --- guardrails & context ---

    # agent별 주입 문서 매핑. None이면 fallback(루트 docs 전체).
    _GUARDRAIL_DOCS = {
        "backend": [
            "CLAUDE.md",
            "backend/CLAUDE.md",
            "db/CLAUDE.md",
            "docs/API.md",
            "docs/ARCHITECTURE.md",
            "docs/ADR.md",
        ],
        "frontend": [
            "CLAUDE.md",
            "frontend/CLAUDE.md",
            "docs/API.md",
            "docs/ARCHITECTURE.md",
            "docs/UI_GUIDE.md",
            "docs/ADR.md",
        ],
    }

    def _load_guardrails(self, agent: str = "shared") -> str:
        """agent에 맞는 가드레일 문서를 읽어 합친 문자열을 반환.

        backend/frontend는 슬림화된 화이트리스트, both는 둘 합집합, shared는
        루트 CLAUDE.md + 모든 docs/*.md.
        """
        if agent == "both":
            paths = list(dict.fromkeys(
                self._GUARDRAIL_DOCS["backend"] + self._GUARDRAIL_DOCS["frontend"]
            ))
        elif agent in self._GUARDRAIL_DOCS:
            paths = self._GUARDRAIL_DOCS[agent]
        else:  # shared 또는 알 수 없는 값 → 광범위 fallback
            paths = []
            claude_md = ROOT / "CLAUDE.md"
            if claude_md.exists():
                paths.append("CLAUDE.md")
            for sub in ("frontend", "backend", "db"):
                p = ROOT / sub / "CLAUDE.md"
                if p.exists():
                    paths.append(f"{sub}/CLAUDE.md")
            docs_dir = ROOT / "docs"
            if docs_dir.is_dir():
                for doc in sorted(docs_dir.glob("*.md")):
                    paths.append(f"docs/{doc.name}")

        sections = []
        for rel in paths:
            p = ROOT / rel
            if not p.exists():
                continue
            label = "프로젝트 규칙 (CLAUDE.md)" if rel == "CLAUDE.md" else rel
            sections.append(f"## {label}\n\n{p.read_text(encoding='utf-8')}")
        return "\n\n---\n\n".join(sections) if sections else ""

    @staticmethod
    def _build_step_context(index: dict) -> str:
        lines = [
            f"- Step {s['step']} ({s['name']}): {s['summary']}"
            for s in index["steps"]
            if s["status"] == "completed" and s.get("summary")
        ]
        if not lines:
            return ""
        return "## 이전 Step 산출물\n\n" + "\n".join(lines) + "\n\n"

    def _build_preamble(self, guardrails: str, step_context: str,
                        prev_error: Optional[str] = None,
                        agent: str = "shared") -> str:
        commit_example = self.FEAT_MSG.format(
            phase=self._phase_name, num="N", name="<step-name>"
        )
        retry_section = ""
        if prev_error:
            retry_section = (
                f"\n## ⚠ 이전 시도 실패 — 아래 에러를 반드시 참고하여 수정하라\n\n"
                f"{prev_error}\n\n---\n\n"
            )
        agent_role = {
            "backend": (
                "당신은 backend-engineer 역할이다. backend/, db/ 만 변경할 수 있다. "
                "frontend/ 는 read-only. SQL은 internal/repo만, branch_id 강제, "
                "단일 트랜잭션, soft delete, PII 비노출. docs/API.md는 변경 금지."
            ),
            "frontend": (
                "당신은 frontend-engineer 역할이다. frontend/ 만 변경할 수 있다. "
                "backend/, db/ 는 read-only. Gin API만 호출, Web Speech API 외 STT SDK "
                "금지, 키오스크 PII 마스킹, 임시 비번 localStorage 저장 금지. "
                "docs/API.md는 변경 금지."
            ),
            "both": (
                "이 step은 backend·frontend 양쪽을 다룬다. ## Backend 작업 / "
                "## Frontend 작업 섹션을 각각 처리하되, 책임 영역(backend/, frontend/)을 "
                "엄격히 지켜라. SQL은 internal/repo만, 클라는 Gin API만."
            ),
            "shared": (
                "이 step은 공유 영역(docs/, .env.example, docker-compose.yml, "
                "루트 CLAUDE.md, scripts/)을 다룬다. backend/·frontend/ 코드는 변경 금지."
            ),
        }.get(agent, "")
        return (
            f"당신은 {self._project} 프로젝트의 개발자입니다. 아래 step을 수행하세요.\n\n"
            f"{agent_role}\n\n"
            f"{guardrails}\n\n---\n\n"
            f"{step_context}{retry_section}"
            f"## 작업 규칙\n\n"
            f"1. 이전 step에서 작성된 코드를 확인하고 일관성을 유지하라.\n"
            f"2. 이 step에 명시된 작업만 수행하라. 추가 기능이나 파일을 만들지 마라.\n"
            f"3. 기존 테스트를 깨뜨리지 마라.\n"
            f"4. AC(Acceptance Criteria) 검증을 직접 실행하라.\n"
            f"5. /phases/{self._phase_dir_name}/index.json의 해당 step status를 업데이트하라:\n"
            f"   - AC 통과 → \"completed\" + \"summary\" 필드에 이 step의 산출물을 한 줄로 요약\n"
            f"   - {self.MAX_RETRIES}회 수정 시도 후에도 실패 → \"error\" + \"error_message\" 기록\n"
            f"   - 사용자 개입이 필요한 경우 (API 키, 인증, 수동 설정 등) → \"blocked\" + \"blocked_reason\" 기록 후 즉시 중단\n"
            f"6. 모든 변경사항을 커밋하라:\n"
            f"   {commit_example}\n\n---\n\n"
        )

    # --- Claude 호출 ---

    def _invoke_claude(self, step: dict, preamble: str,
                       cwd: Optional[str] = None,
                       agent: str = "shared") -> dict:
        step_num, step_name = step["step"], step["name"]
        step_file = self._phase_dir / f"step{step_num}.md"

        if not step_file.exists():
            print(f"  ERROR: {step_file} not found")
            sys.exit(1)

        # frontmatter는 메타데이터일 뿐 — 본문만 prompt에 넘긴다
        raw = step_file.read_text(encoding="utf-8")
        _, body = _parse_frontmatter(raw)
        prompt = preamble + body

        # prompt를 명령 인자로 넘기면 OS ARG_MAX(보통 ~128KB)를 초과해
        # "Argument list too long"으로 실패한다. stdin으로 파이프해 한도 회피.
        # claude CLI는 인자 자리의 prompt가 비면 stdin을 읽는다.
        result = subprocess.run(
            [
                "claude", "-p", "--dangerously-skip-permissions",
                "--max-turns", str(self.MAX_TURNS),
                "--output-format", "json",
            ],
            input=prompt,
            cwd=cwd or self._root, capture_output=True, text=True,
            timeout=self.TIMEOUT_SEC,
        )

        if result.returncode != 0:
            print(f"\n  WARN: Claude가 비정상 종료됨 (code {result.returncode})")
            if result.stderr:
                print(f"  stderr: {result.stderr[:500]}")

        output = {
            "step": step_num, "name": step_name, "agent": agent,
            "exitCode": result.returncode,
            "stdout": result.stdout, "stderr": result.stderr,
        }
        # 메인 phases/ 에 출력 저장(.gitignore에 등록되어 추적 안 됨)
        out_path = self._phase_dir / f"step{step_num}-output.json"
        with open(out_path, "w") as f:
            json.dump(output, f, indent=2, ensure_ascii=False)

        return output

    # --- 헤더 & 검증 ---

    def _print_header(self):
        print(f"\n{'='*60}")
        print(f"  Harness Step Executor")
        print(f"  Phase: {self._phase_name} | Steps: {self._total}")
        if self._auto_push:
            print(f"  Auto-push: enabled")
        print(f"{'='*60}")

    def _check_blockers(self):
        index = self._read_json(self._index_file)
        for s in reversed(index["steps"]):
            if s["status"] == "error":
                print(f"\n  ✗ Step {s['step']} ({s['name']}) failed.")
                print(f"  Error: {s.get('error_message', 'unknown')}")
                print(f"  Fix and reset status to 'pending' to retry.")
                sys.exit(1)
            if s["status"] == "blocked":
                print(f"\n  ⏸ Step {s['step']} ({s['name']}) blocked.")
                print(f"  Reason: {s.get('blocked_reason', 'unknown')}")
                print(f"  Resolve and reset status to 'pending' to retry.")
                sys.exit(2)
            if s["status"] != "pending":
                break

    def _ensure_created_at(self):
        index = self._read_json(self._index_file)
        if "created_at" not in index:
            index["created_at"] = self._stamp()
            self._write_json(self._index_file, index)

    # --- 실행 루프 ---

    def _cwd_for_step(self, agent: str) -> str:
        if agent in ("backend", "frontend"):
            return str(self._worktree_path(agent))
        return self._root

    # --- post-completion gates (acceptance + code-reviewer) ---

    ACCEPTANCE_CMDS = {
        "backend": [
            ["go", "build", "./..."],
            ["go", "test", "-race", "./..."],
        ],
        "frontend": [
            ["pnpm", "lint"],
            ["pnpm", "build"],
            ["pnpm", "test", "--run"],
        ],
    }

    ACCEPTANCE_MANIFEST = {
        "backend": "go.mod",
        "frontend": "package.json",
    }

    def _which(self, binary: str) -> bool:
        r = subprocess.run(["which", binary], capture_output=True, text=True)
        return r.returncode == 0 and bool(r.stdout.strip())

    def _run_acceptance(self, agent: str, cwd: str) -> tuple[bool, str]:
        """agent별 lint/build/test 명령을 실제로 실행. (passed, message) 반환.

        message가 'TOOL_MISSING:<binary>'로 시작하면 blocked로 분류.
        manifest(go.mod / package.json)가 아직 없는 모듈은 스캐폴드 이전 단계
        (예: Phase 1 DB-only)이므로 빌드 대상이 없어 acceptance를 건너뛴다.
        """
        agents_to_run = []
        if agent == "backend":
            agents_to_run = ["backend"]
        elif agent == "frontend":
            agents_to_run = ["frontend"]
        elif agent == "both":
            agents_to_run = ["backend", "frontend"]
        else:  # shared
            return True, ""

        for a in agents_to_run:
            sub_cwd = str(Path(cwd) / a) if (Path(cwd) / a).is_dir() else cwd
            manifest = Path(sub_cwd) / self.ACCEPTANCE_MANIFEST[a]
            if not manifest.exists():
                continue
            cmds = self.ACCEPTANCE_CMDS[a]
            primary = cmds[0][0]
            if not self._which(primary):
                return False, f"TOOL_MISSING:{primary}"
            for cmd in cmds:
                r = subprocess.run(cmd, cwd=sub_cwd, capture_output=True, text=True)
                if r.returncode != 0:
                    tail = (r.stdout + r.stderr).strip()[-1500:]
                    return False, f"acceptance 실패 ({' '.join(cmd)}, cwd={sub_cwd}):\n{tail}"
        return True, ""

    def _run_review(self, cwd: str, step_num: Optional[int] = None) -> tuple[bool, str]:
        """code-reviewer 서브에이전트를 별도 단발 세션으로 호출해 PASS 검증.

        결과(PASS/BLOCK 사유)는 progress_indicator 때문에 stdout buffering으로
        실시간 추적이 어렵다. step_num이 주어지면 phase_dir에 step{N}-review.txt
        파일로 전체 응답을 dump해 사후 진단·학습이 가능하게 한다.
        """
        prompt = (
            "현재 worktree의 변경 사항을 code-reviewer 서브에이전트에 위임해 검증하라. "
            "출력은 PASS 또는 BLOCK으로 시작해야 한다."
        )
        try:
            # _invoke_claude와 동일한 이유로 prompt는 stdin으로 전달.
            r = subprocess.run(
                [
                    "claude", "-p", "--dangerously-skip-permissions",
                    "--max-turns", "30",
                    "--output-format", "text",
                ],
                input=prompt,
                cwd=cwd, capture_output=True, text=True,
                timeout=600,
            )
        except subprocess.TimeoutExpired:
            if step_num is not None:
                self._dump_review(step_num, "[TIMEOUT]\nreview가 10분 안에 끝나지 않았다.")
            return False, "review 타임아웃 (10분 초과)"

        # 응답 dump — 사후 진단·재시도 시 prev_error로 활용 가능.
        full_dump = (
            f"--- exit code ---\n{r.returncode}\n\n"
            f"--- stdout ---\n{(r.stdout or '').strip()}\n\n"
            f"--- stderr ---\n{(r.stderr or '').strip()}\n"
        )
        if step_num is not None:
            self._dump_review(step_num, full_dump)

        out = (r.stdout or "") + "\n" + (r.stderr or "")
        # PASS / BLOCK 판정
        if re.search(r"\bPASS\b", r.stdout or "", re.MULTILINE):
            return True, ""
        if re.search(r"\bBLOCK\b", r.stdout or "", re.MULTILINE):
            return False, f"code-reviewer BLOCK:\n{(r.stdout or '').strip()[-1500:]}"
        # 명시적 PASS/BLOCK이 없으면 안전하게 실패로 분류
        return False, f"code-reviewer가 PASS/BLOCK을 반환하지 않음:\n{out.strip()[-1500:]}"

    def _dump_review(self, step_num: int, content: str) -> None:
        """step별 review 결과를 phase_dir/step{N}-review.txt에 저장(.gitignore 처리)."""
        try:
            (self._phase_dir / f"step{step_num}-review.txt").write_text(
                content, encoding="utf-8"
            )
        except OSError as e:
            print(f"  WARN: review dump 실패: {e}")

    def _post_completion_gate(self, agent: str, cwd: str,
                               step_num: Optional[int] = None) -> tuple[str, str]:
        """completed 직후 acceptance + review를 실행.

        반환:
            ("pass", "")               — 양쪽 모두 통과
            ("retry", error_message)   — 실패, retry로
            ("block", reason)          — 도구 부재 등 사용자 개입 필요
        """
        ok, msg = self._run_acceptance(agent, cwd)
        if not ok:
            if msg.startswith("TOOL_MISSING:"):
                tool = msg.split(":", 1)[1]
                return "block", f"필수 도구 미설치: {tool} (acceptance 실행 불가)"
            return "retry", msg
        ok, msg = self._run_review(cwd, step_num=step_num)
        if not ok:
            if "claude" in msg and "command not found" in msg.lower():
                return "block", "code-reviewer 호출 실패: claude CLI를 찾을 수 없음"
            return "retry", msg
        return "pass", ""

    def _read_step_summary(self, step_num: int) -> str:
        """step.md frontmatter에서 summary 값을 읽는다.

        B 방안(책임 분리): 자식 Claude는 main worktree의 phases/ 인덱스에 status·
        summary를 박지 않는다. 대신 step 작성자가 step.md frontmatter의
        `summary:` 필드에 산출물 한 줄을 미리 적어두면, gate 통과 시 execute.py가
        그 값을 main 인덱스에 기록한다.
        """
        step_file = self._phase_dir / f"step{step_num}.md"
        if not step_file.exists():
            return ""
        meta, _ = _parse_frontmatter(step_file.read_text(encoding="utf-8"))
        s = meta.get("summary", "")
        return s.strip() if isinstance(s, str) else ""

    def _execute_single_step(self, step: dict) -> bool:
        """단일 step 실행 (재시도 포함). 완료되면 True, 실패/차단이면 False.

        ## 동작 (B 방안 — 책임 분리)
        - 자식 Claude의 책임: 코드 작성 + 테스트 + commit. 끝.
        - status 결정 책임은 execute.py — 자식이 박은 status는 무시한다.
          (worktree 안의 phases 인덱스 commit은 main worktree에 보이지 않아
          예전 코드는 max-turns 사고 시 영원히 retry로 빠졌다.)
        - 단 자식이 명시적으로 `blocked`로 박은 경우는 인간 개입 신호로 존중.
        - status는 acceptance + code-reviewer gate 결과로 직접 main에 박는다.
        - summary는 step.md frontmatter `summary:` 필드에서 읽는다.
        """
        step_num, step_name = step["step"], step["name"]
        agent = self._agent_for_step(step)
        cwd = self._cwd_for_step(agent)
        guardrails = self._load_guardrails(agent)
        done = sum(1 for s in self._read_json(self._index_file)["steps"] if s["status"] == "completed")
        prev_error = None
        declared_summary = self._read_step_summary(step_num)

        for attempt in range(1, self.MAX_RETRIES + 1):
            index = self._read_json(self._index_file)
            step_context = self._build_step_context(index)
            preamble = self._build_preamble(guardrails, step_context, prev_error, agent=agent)

            tag = f"Step {step_num}/{self._total - 1} ({done} done, {agent}): {step_name}"
            if attempt > 1:
                tag += f" [retry {attempt}/{self.MAX_RETRIES}]"

            with progress_indicator(tag) as pi:
                self._invoke_claude(step, preamble, cwd=cwd, agent=agent)
                elapsed = int(pi.elapsed)

            ts = self._stamp()
            index = self._read_json(self._index_file)

            # 자식이 명시적으로 status를 'blocked'로 박은 경우만 그대로 존중한다.
            # (인간 개입 신호: API 키 부재, ADR 갱신 필요 등.) 자식은 일반적으로
            # main 인덱스에 쓸 수 없지만 — 만약 sync 사고로 박혔거나 메인 세션에서
            # 미리 두었다면 무시하지 말고 종료한다.
            cur_status = next(
                (s.get("status", "pending") for s in index["steps"] if s["step"] == step_num),
                "pending",
            )
            if cur_status == "blocked":
                for s in index["steps"]:
                    if s["step"] == step_num:
                        s["blocked_at"] = ts
                self._write_json(self._index_file, index)
                reason = next(
                    (s.get("blocked_reason", "") for s in index["steps"] if s["step"] == step_num),
                    "",
                )
                print(f"  ⏸ Step {step_num}: {step_name} blocked [{elapsed}s]")
                print(f"    Reason: {reason}")
                self._update_top_index("blocked")
                sys.exit(2)

            # acceptance(빌드/테스트) + code-reviewer gate.
            # 자식이 정상 종료(exit 0) 했어도 빌드 깨지거나 reviewer BLOCK이면 retry.
            # 자식이 비정상 종료(max-turns 등) 했어도 코드 commit이 살아 있으면 PASS 가능.
            gate, gate_msg = self._post_completion_gate(agent, cwd, step_num=step_num)

            if gate == "block":
                for s in index["steps"]:
                    if s["step"] == step_num:
                        s["status"] = "blocked"
                        s["blocked_reason"] = gate_msg
                        s["blocked_at"] = ts
                self._write_json(self._index_file, index)
                print(f"  ⏸ Step {step_num}: {step_name} blocked [{elapsed}s]")
                print(f"    Reason: {gate_msg}")
                self._update_top_index("blocked")
                sys.exit(2)

            if gate == "pass":
                for s in index["steps"]:
                    if s["step"] == step_num:
                        s["status"] = "completed"
                        s["completed_at"] = ts
                        if declared_summary and not s.get("summary"):
                            s["summary"] = declared_summary
                        s.pop("error_message", None)
                        s.pop("blocked_reason", None)
                self._write_json(self._index_file, index)
                self._commit_step(step_num, step_name, cwd=cwd)
                print(f"  ✓ Step {step_num}: {step_name} [{elapsed}s, {agent}]")
                return True

            # gate == "retry"
            review_dump = self._phase_dir / f"step{step_num}-review.txt"
            dump_hint = (
                f" — review dump: {review_dump.relative_to(ROOT) if review_dump.exists() else '(없음)'}"
            )
            if attempt < self.MAX_RETRIES:
                for s in index["steps"]:
                    if s["step"] == step_num:
                        s["status"] = "pending"
                        s.pop("error_message", None)
                self._write_json(self._index_file, index)
                prev_error = gate_msg
                print(f"  ↻ Step {step_num}: gate 실패 — retry {attempt}/{self.MAX_RETRIES}{dump_hint}")
                continue

            for s in index["steps"]:
                if s["step"] == step_num:
                    s["status"] = "error"
                    s["error_message"] = f"[{self.MAX_RETRIES}회 시도 후 gate 실패] {gate_msg}"
                    s["failed_at"] = ts
            self._write_json(self._index_file, index)
            print(f"  ✗ Step {step_num}: {step_name} {self.MAX_RETRIES}회 시도 후 gate 실패{dump_hint}")
            self._commit_step(step_num, step_name, cwd=cwd)
            print(f"  ✗ Step {step_num}: {step_name} gate failed after {self.MAX_RETRIES} attempts")
            self._update_top_index("error")
            sys.exit(1)

        return False  # unreachable

    def _execute_parallel_group(self, steps: list[dict]):
        """같은 parallel_group을 가진 step들을 ThreadPoolExecutor로 동시 실행.

        한쪽이 실패해도 다른 쪽은 끝까지 실행한다. 모두 성공해야 그룹 통과.
        """
        if len(steps) == 1:
            self._execute_single_step(steps[0])
            return

        names = ", ".join(f"step{s['step']}({self._agent_for_step(s)})" for s in steps)
        print(f"  ⇉ Parallel dispatch: {names}")

        # started_at 일괄 기록
        index = self._read_json(self._index_file)
        for step in steps:
            for s in index["steps"]:
                if s["step"] == step["step"] and "started_at" not in s:
                    s["started_at"] = self._stamp()
        self._write_json(self._index_file, index)

        with ThreadPoolExecutor(max_workers=len(steps)) as ex:
            futures = {ex.submit(self._execute_single_step, s): s for s in steps}
            for fut in futures:
                # _execute_single_step 내부에서 실패 시 sys.exit 하므로
                # 여기까지 오면 모두 성공 또는 sys.exit으로 인한 예외 전파.
                fut.result()

    def _execute_all_steps(self):
        while True:
            index = self._read_json(self._index_file)
            pending = [s for s in index["steps"] if s["status"] == "pending"]
            if not pending:
                print("\n  All steps completed!")
                return

            # 첫 번째 pending step의 parallel_group을 기준으로 그룹 모으기
            head = pending[0]
            group_id = head.get("parallel_group")
            if group_id is None:
                # 단일 step
                if "started_at" not in head:
                    for s in index["steps"]:
                        if s["step"] == head["step"]:
                            s["started_at"] = self._stamp()
                    self._write_json(self._index_file, index)
                self._execute_single_step(head)
            else:
                group = [s for s in pending if s.get("parallel_group") == group_id]
                self._execute_parallel_group(group)

    def _finalize(self):
        index = self._read_json(self._index_file)
        # 부분 진행 가드: 검토 게이트 패턴(미래 step을 'deferred' 등 비-pending status로 두고
        # 라운드별로 풀어가는 패턴)을 지원. 모든 step이 완료된 시점에만 phase finalize·merge·
        # top-index 갱신을 수행한다.
        all_completed = all(s.get("status") == "completed" for s in index.get("steps", []))
        if not all_completed:
            remaining = [
                f"step{s['step']}({s.get('status', 'pending')})"
                for s in index.get("steps", [])
                if s.get("status") != "completed"
            ]
            print(f"\n  ⏸ 부분 진행 — phase finalize 보류 (남은 step: {', '.join(remaining)}).")
            print(f"  남은 step의 status를 'pending'으로 풀고 다시 실행하면 이어집니다.")
            return
        index["completed_at"] = self._stamp()
        self._write_json(self._index_file, index)
        self._update_top_index("completed")

        # 메인 worktree에서 phases/ 메타데이터 마무리 커밋
        self._run_git("add", "-A")
        if self._run_git("diff", "--cached", "--quiet").returncode != 0:
            msg = f"chore({self._phase_name}): mark phase completed"
            r = self._run_git("commit", "-m", msg)
            if r.returncode == 0:
                print(f"  ✓ {msg}")

        # worktree 브랜치를 메인으로 merge (BE 먼저, FE 다음)
        agents_used = self._collect_agents()
        for agent in ("backend", "frontend"):
            if agent not in agents_used:
                continue
            branch = self._branch_for(agent)
            r = self._run_git("merge", "--no-ff", "-m",
                              f"merge({self._phase_name}): {agent} worktree → main",
                              branch)
            if r.returncode != 0:
                print(f"\n  WARN: {branch} merge 실패 — 충돌 발생 시 수동 해결 필요")
                print(f"  {r.stderr.strip()}")

        if self._auto_push:
            r = self._run_git("push", "origin", "HEAD")
            if r.returncode != 0:
                print(f"\n  ERROR: git push 실패: {r.stderr.strip()}")
                sys.exit(1)
            print(f"  ✓ Pushed")

        print(f"\n{'='*60}")
        print(f"  Phase '{self._phase_name}' completed!")
        print(f"  worktree({WORKTREE_DIR_NAME}/)는 다음 phase에서 재사용을 위해 보존합니다.")
        print(f"  필요 시 `git worktree remove .worktrees/<name>`으로 정리하세요.")
        print(f"{'='*60}")


def main():
    parser = argparse.ArgumentParser(description="Harness Step Executor")
    parser.add_argument("phase_dir", help="Phase directory name (e.g. 0-mvp)")
    parser.add_argument("--push", action="store_true", help="Push branch after completion")
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Claude 호출 없이 실행 계획·가드레일 매트릭스만 출력",
    )
    args = parser.parse_args()

    StepExecutor(args.phase_dir, auto_push=args.push, dry_run=args.dry_run).run()


if __name__ == "__main__":
    main()
