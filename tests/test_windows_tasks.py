from __future__ import annotations

from pathlib import Path


def test_install_task_builds_schtasks_command(monkeypatch, tmp_path: Path):
    from keystone.schedulers import windows_tasks

    captured: list[list[str]] = []

    def fake_run(cmd, check):
        captured.append(cmd)

        class R:
            returncode = 0

        return R()

    monkeypatch.setattr(windows_tasks.subprocess, "run", fake_run)

    spec = windows_tasks.TaskSpec(
        name=r"Keystone\test_task",
        schedule="DAILY",
        start_time="04:30",
        description="t",
    )
    windows_tasks.install_task(spec, ["-c", "print('x')"], repo_root=tmp_path)

    assert len(captured) == 1
    cmd = captured[0]
    assert cmd[0] == "schtasks"
    assert "/Create" in cmd
    assert "/TN" in cmd
    assert r"Keystone\test_task" in cmd
    assert "/SC" in cmd and "DAILY" in cmd
    assert "/ST" in cmd and "04:30" in cmd
    # The /TR string should reference uv, the repo root, and the python args.
    tr_index = cmd.index("/TR") + 1
    tr = cmd[tr_index]
    assert "uv" in tr.lower()
    assert str(tmp_path) in tr
    assert "print('x')" in tr


def test_install_all_installs_every_registered_task(monkeypatch, tmp_path: Path):
    """``install_all`` should call schtasks once per entry in ``TASKS``.

    The core framework ships with an empty TASKS list; stones populate it.
    """

    from keystone.schedulers import windows_tasks

    captured: list[list[str]] = []
    monkeypatch.setattr(
        windows_tasks.subprocess, "run", lambda cmd, check: captured.append(cmd)
    )

    expected_task_names = [spec.name for spec, _ in windows_tasks.TASKS]
    windows_tasks.install_all(repo_root=tmp_path)

    assert len(captured) == len(expected_task_names)
    installed = [c[c.index("/TN") + 1] for c in captured]
    assert installed == expected_task_names


def test_install_all_with_injected_tasks(monkeypatch, tmp_path: Path):
    """Verify the install loop wires one schtasks call per TASK spec."""

    from keystone.schedulers import windows_tasks

    captured: list[list[str]] = []
    monkeypatch.setattr(
        windows_tasks.subprocess, "run", lambda cmd, check: captured.append(cmd)
    )

    spec_a = windows_tasks.TaskSpec(
        name=r"Keystone\a_nightly",
        schedule="DAILY",
        start_time="03:00",
        description="test",
    )
    spec_b = windows_tasks.TaskSpec(
        name=r"Keystone\b_morning",
        schedule="DAILY",
        start_time="07:00",
        description="test",
    )
    monkeypatch.setattr(
        windows_tasks,
        "TASKS",
        [(spec_a, ["-m", "a.scheduler"]), (spec_b, ["-m", "b.scheduler"])],
    )

    windows_tasks.install_all(repo_root=tmp_path)
    names = [c[c.index("/TN") + 1] for c in captured]
    assert r"Keystone\a_nightly" in names
    assert r"Keystone\b_morning" in names
