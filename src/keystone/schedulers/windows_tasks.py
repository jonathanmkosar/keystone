r"""Idempotent Windows Task Scheduler installer.

Wraps `schtasks` to register tasks that run our scheduled job entrypoints.
Each task is installed under Task Scheduler Library > Keystone\<name>.

Why schtasks (not the WinRT API): zero extra dependencies and easy to inspect
in the Task Scheduler GUI. We pass /F to force overwrite so re-running is safe.
"""

from __future__ import annotations

import logging
import shutil
import subprocess
from dataclasses import dataclass
from pathlib import Path

log = logging.getLogger(__name__)


@dataclass(frozen=True)
class TaskSpec:
    name: str                  # e.g. "Keystone\\financial_nightly"
    schedule: str              # "DAILY" | "WEEKLY"
    start_time: str            # "HH:MM" 24h
    days: str | None = None    # for WEEKLY, comma-separated MON,TUE,...
    description: str = ""


REPO_ROOT = Path(__file__).resolve().parents[3]


# Stones register their own tasks. Populate this list with (TaskSpec, ["-m",
# "<stone_name>.scheduler"]) tuples for each stone that should run on a
# schedule, or leave empty if no stone needs a scheduler. Example:
#
# TASKS = [
#     (
#         TaskSpec(
#             name=r"Keystone\example_stone_nightly",
#             schedule="DAILY",
#             start_time="03:00",
#             description="Nightly refresh for the example stone.",
#         ),
#         ["-m", "example_stone.scheduler"],
#     ),
# ]
TASKS: list[tuple[TaskSpec, list[str]]] = []


def _uv_path() -> str:
    """Resolve the uv executable so schtasks gets an absolute path."""
    found = shutil.which("uv")
    if found:
        return found
    # Common Windows install location.
    fallback = Path.home() / ".local" / "bin" / "uv.exe"
    return str(fallback)


def install_task(spec: TaskSpec, python_args: list[str], repo_root: Path | None = None) -> None:
    repo_root = (repo_root or REPO_ROOT).resolve()
    uv = _uv_path()
    # Build the command line that schtasks should run. uv handles venv activation.
    inner = (
        f'"{uv}" --directory "{repo_root}" run python ' + " ".join(python_args)
    )

    cmd = [
        "schtasks",
        "/Create",
        "/TN",
        spec.name,
        "/TR",
        inner,
        "/SC",
        spec.schedule,
        "/ST",
        spec.start_time,
        "/F",  # overwrite if exists
        "/RL",
        "LIMITED",
    ]
    if spec.schedule == "WEEKLY" and spec.days:
        cmd.extend(["/D", spec.days])

    log.info("Installing task %s -> %s", spec.name, inner)
    subprocess.run(cmd, check=True)


def install_all(repo_root: Path | None = None) -> None:
    for spec, args in TASKS:
        install_task(spec, args, repo_root=repo_root)


def uninstall_all() -> None:
    for spec, _ in TASKS:
        try:
            subprocess.run(["schtasks", "/Delete", "/TN", spec.name, "/F"], check=True)
        except subprocess.CalledProcessError:
            log.warning("Could not delete task %s (may not exist)", spec.name)


if __name__ == "__main__":
    import argparse

    p = argparse.ArgumentParser()
    p.add_argument("action", choices=("install", "uninstall"))
    p.add_argument("--repo-root", type=Path, default=None)
    args = p.parse_args()
    logging.basicConfig(level=logging.INFO, format="%(levelname)s %(name)s: %(message)s")
    if args.action == "install":
        install_all(args.repo_root)
    else:
        uninstall_all()
