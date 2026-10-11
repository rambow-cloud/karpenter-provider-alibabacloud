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


def needs_go(path: str) -> bool:
    # Source/modules always win, including files moved into a light-only tree.
    if path.lower().endswith(".go") or Path(path).name in {"go.mod", "go.sum", "go.work", "go.work.sum"}:
        return True
    if path.startswith(("website/", "docs/", ".github/")):
        return False
    if path.lower().endswith((".md", ".rst")):
        return False
    # Build files, charts, fixtures and any unclassified changes retain full CI.
    return True


def valid_sha(value: str) -> bool:
    return bool(re.fullmatch(r"[0-9a-f]{40}(?:[0-9a-f]{24})?", value)) and set(value) != {"0"}


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
    run_go = any(needs_go(path) for path in files)
    reason = ("Code, dependency, build or unclassified files changed" if run_go
              else "Only website, documentation or GitHub Actions files changed")
    return Decision(run_go, reason, files, diff_range)


def main():
    event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text(encoding="utf-8"))
    decision = classify(os.environ["GITHUB_EVENT_NAME"], event)
    with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as output:
        output.write(f"run_go={str(decision.run_go).lower()}\n")
        output.write(f"diff_range={decision.diff_range}\n")
    if summary := os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(summary, "a", encoding="utf-8") as output:
            output.write(f"### Validation scope\n\n{decision.reason}.\n\n")
            output.write(f"Changed paths: {len(decision.files)}. Go work: {'enabled' if decision.run_go else 'skipped'}.\n")
    print(json.dumps(vars(decision), ensure_ascii=True))


if __name__ == "__main__":
    main()
