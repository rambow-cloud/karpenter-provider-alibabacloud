"""Validate issue PRs to dev and the final dev-to-main integration gate."""

import json
import os
from pathlib import Path
import re
import urllib.request

DEVELOPMENT_BATCH = (5, 15, 16, 18, 20, 23, 29, 51, 52)
SECTIONS = ("Problem and result", "Linked issues", "Validation", "Compatibility and rollout", "Checklist")


def validate_metadata(pr):
    branch, base = pr["head"]["ref"], pr["base"]["ref"]
    assert re.match(r"^(feat|fix|chore|docs|test|ci|refactor|perf)(\([^)]+\))?!?: .+", pr["title"]), "Use a conventional PR title"
    body = pr.get("body") or ""
    for section in SECTIONS:
        assert "## " + section in body, "Missing PR template section: " + section
    if branch == "dev":
        assert base == "main", "The dev integration PR must target main"
        return None
    match = re.fullmatch(r"feat/([1-9][0-9]*)-[a-z0-9]+(?:[.-][a-z0-9]+)*", branch)
    assert match, "Branch must be feat/<issue-number>-<short-description>"
    assert base == "dev", "Individual issue PRs must target dev"
    issue = int(match.group(1))
    assert re.search(r"\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s+#" + str(issue) + r"\b", body, re.I), "Add Closes #<branch issue number>"
    return issue


def validate_issues(primary, fetch_issue):
    if primary is not None:
        issue = fetch_issue(primary)
        assert "pull_request" not in issue, "Primary reference must be an issue, not a PR"
        assert issue["state"] == "open", "Primary issue must be open"
        return
    for number in DEVELOPMENT_BATCH:
        issue = fetch_issue(number)
        assert "pull_request" not in issue, "Development batch references must be issues"
        assert issue["state"] == "closed" and issue.get("state_reason") == "completed", f"Integration blocked: issue #{number} has not completed acceptance"


def main():
    event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text(encoding="utf-8"))
    primary = validate_metadata(event["pull_request"])
    repo = event["repository"]["full_name"]

    def fetch_issue(number):
        request = urllib.request.Request(
            os.environ["GITHUB_API_URL"] + f"/repos/{repo}/issues/{number}",
            headers={"Authorization": "Bearer " + os.environ["GH_TOKEN"], "Accept": "application/vnd.github+json"},
        )
        with urllib.request.urlopen(request, timeout=20) as response:
            return json.load(response)

    validate_issues(primary, fetch_issue)
    print("PR policy passed for " + (f"issue #{primary} -> dev" if primary else "completed development batch -> main"))


if __name__ == "__main__":
    main()
