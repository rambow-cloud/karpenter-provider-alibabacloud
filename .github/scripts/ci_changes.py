"""Route docs/site/Actions-only changes without installing or running Go."""

from dataclasses import dataclass
import json
import os
from pathlib import Path
import re
import subprocess


@dataclass
class Decision:
    run_go: bool
    reason: str
    files: list[str]
    diff_range: str = ""

    @property
    def run_kwok(self) -> bool:
        return self.run_go or any(kwok_path(path) for path in self.files)


def kwok_path(path: str) -> bool:
    if path.lower().endswith((".md", ".rst")):
        return False
    return path in (".github/workflows/e2e-kwok.yaml", "Makefile") or path.startswith(
        ("test/hack/kwok/", "test/pkg/environment/kwok/"))


def needs_go(path: str) -> bool:
    # Source/modules always win, including files moved into a light-only tree.
    if path.lower().endswith(".go") or Path(path).name in {"go.mod", "go.sum", "go.work", "go.work.sum"}:
        return True
    if path.startswith(("website/", "docs/", ".github/")):
        return False
    if kwok_path(path) and path != "Makefile" and Path(path).suffix in (".sh", ".py", ".yaml", ".yml", ".json"):
        return False
    if path.lower().endswith((".md", ".rst")):
        return False
    # Build files, charts, fixtures and any unclassified changes retain full CI.
    return True


def valid_sha(value: str) -> bool:
    return bool(re.fullmatch(r"[0-9a-f]{40}(?:[0-9a-f]{24})?", value)) and set(value) != {"0"}


def only_kwok_make_defaults(repo: str, diff_range: str) -> bool:
    """Only numeric replica defaults and that target's help text bypass Go CI."""
    result = subprocess.run(["git", "diff", "--unified=0", "--no-ext-diff", diff_range, "--", "Makefile"],
                            cwd=repo, capture_output=True, text=True, check=True)
    changes = [line[1:] for line in result.stdout.splitlines()
               if line.startswith(("+", "-")) and not line.startswith(("+++", "---"))]
    return bool(changes) and all(re.fullmatch(
        r"KWOK_SCALE_REPLICAS \?= [1-9][0-9]*|kwok-core-e2e: core-module ## [^\r\n]+", line)
        for line in changes)


def classify(event_name: str, event: dict, repo: str = ".") -> Decision:
    if event_name == "workflow_dispatch":
        return Decision(True, "Manual full validation requested", [])
    if event_name == "pull_request":
        pr = event.get("pull_request", {})
        base = pr.get("base", {}).get("sha", "")
        head = pr.get("head", {}).get("sha", "")
        separator = "..."  # Entire PR relative to its merge base, not just the last commit.
    elif event_name == "push":
        base, head = event.get("before", ""), event.get("after", "")
        separator = ".."
    else:
        return Decision(True, "Unclassified event; retain full validation", [])
    if not valid_sha(base) or not valid_sha(head):
        return Decision(True, "Missing comparison commit; retain full validation", [])
    diff_range = base + separator + head
    try:
        result = subprocess.run(
            ["git", "diff", "--name-only", "--no-renames", "-z", diff_range, "--"],
            cwd=repo, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True,
        )
    except subprocess.CalledProcessError:
        return Decision(True, "Comparison unavailable; retain full validation", [])
    # Disabling rename detection includes BOTH old and new paths, plus deletions.
    files = [os.fsdecode(path) for path in result.stdout.split(b"\0") if path]
    try:
        run_go = any(needs_go(path) and not (
            path == "Makefile" and only_kwok_make_defaults(repo, diff_range)) for path in files)
    except subprocess.CalledProcessError:
        run_go = True
    reason = ("Code, dependency, build or unclassified files changed" if run_go
              else "Only documentation, Actions or dedicated KWOK harness settings changed")
    return Decision(run_go, reason, files, diff_range)


def main():
    event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text(encoding="utf-8"))
    decision = classify(os.environ["GITHUB_EVENT_NAME"], event)
    with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as output:
        output.write(f"run_go={str(decision.run_go).lower()}\n")
        output.write(f"run_kwok={str(decision.run_kwok).lower()}\n")
        output.write(f"diff_range={decision.diff_range}\n")
    if summary := os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(summary, "a", encoding="utf-8") as output:
            output.write(f"### Validation scope\n\n{decision.reason}.\n\n")
            output.write(f"Changed paths: {len(decision.files)}. Provider Go checks: {'enabled' if decision.run_go else 'skipped'}. KWOK harness: {'enabled' if decision.run_kwok else 'skipped'}.\n")
    print(json.dumps({**vars(decision), "run_kwok": decision.run_kwok}, ensure_ascii=True))


if __name__ == "__main__":
    main()
