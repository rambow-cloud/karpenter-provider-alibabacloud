"""Prevent automatic E2E events/callers without Go, YAML dependencies or cloud calls."""

from pathlib import Path
import re
import unittest


WORKFLOWS = Path(__file__).resolve().parents[1] / "workflows"
MANUAL_GUARD = "github.event_name == 'workflow_dispatch'"
ALLOWED_GUARDS = {MANUAL_GUARD, MANUAL_GUARD + " && vars.ACK_E2E_ENABLED == 'true'"}


def children(text, key):
    # Repository workflows use block mappings. Reject unrecognized/inline event
    # syntax rather than silently accepting a form the contract cannot inspect.
    lines = text.splitlines()
    start = next(i for i, line in enumerate(lines) if line == key + ":")
    block = []
    for line in lines[start + 1:]:
        if line and not line.startswith((" ", "#")):
            break
        block.append(line)
    return block


def events(text):
    return {match.group(1) for line in children(text, "on")
            if (match := re.fullmatch(r"  ([a-z_]+):.*", line))}


def jobs(text):
    result = {}
    current = None
    for line in children(text, "jobs"):
        if match := re.fullmatch(r"  ([a-zA-Z0-9_-]+):", line):
            current = match.group(1)
            result[current] = []
        elif current:
            result[current].append(line)
    return result


def validate_manual(text):
    triggers = events(text)
    assert triggers and triggers <= {"workflow_dispatch", "workflow_call"}, triggers
    execution_jobs = jobs(text)
    assert execution_jobs, "No guarded execution jobs"
    for name, block in execution_jobs.items():
        guards = [line.removeprefix("    if: ") for line in block
                  if line.startswith("    if: ")]
        assert len(guards) == 1 and guards[0] in ALLOWED_GUARDS, (name, guards)


class ManualE2EContracts(unittest.TestCase):
    def test_all_e2e_workflows_are_manual_and_guarded(self):
        for path in WORKFLOWS.glob("*.y*ml"):
            text = path.read_text(encoding="utf-8")
            if path.name.startswith("e2e") or re.search(r"^name: E2E", text, re.M):
                with self.subTest(workflow=path.name):
                    validate_manual(text)

    def test_any_local_e2e_caller_is_also_manual_and_guarded(self):
        for path in WORKFLOWS.glob("*.y*ml"):
            text = path.read_text(encoding="utf-8")
            if re.search(r"uses: \./\.github/workflows/e2e[^\s]*\.ya?ml", text):
                with self.subTest(caller=path.name):
                    validate_manual(text)

    def test_cleanup_does_not_require_creation_opt_in(self):
        block = jobs((WORKFLOWS / "e2e-cleanup.yaml").read_text(encoding="utf-8"))
        self.assertIn("    if: " + MANUAL_GUARD, block["cleanup"])

    def test_rejects_automatic_events_and_missing_or_bypassable_guard(self):
        base = "on:\n  workflow_dispatch: {}\njobs:\n  execute:\n    if: " + MANUAL_GUARD + "\n    runs-on: ubuntu-latest\n"
        validate_manual(base)
        for event in ("pull_request", "push", "schedule", "workflow_run", "repository_dispatch"):
            with self.subTest(event=event), self.assertRaises(AssertionError):
                validate_manual(base.replace("  workflow_dispatch: {}", "  " + event + ": {}"))
        for changed in (base.replace("    if: " + MANUAL_GUARD + "\n", ""),
                        base.replace(MANUAL_GUARD, MANUAL_GUARD + " || true")):
            with self.assertRaises(AssertionError):
                validate_manual(changed)
        with self.assertRaises(StopIteration):
            validate_manual(base.replace("on:\n  workflow_dispatch: {}", "on: [push, workflow_dispatch]"))


if __name__ == "__main__":
    unittest.main()
