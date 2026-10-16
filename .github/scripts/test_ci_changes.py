"""Protect lightweight routing from mixed changes, renames and wrong diff ranges."""

from pathlib import Path
import subprocess
import tempfile
import unittest

from ci_changes import classify, needs_go


class RoutingTests(unittest.TestCase):
    def test_light_paths(self):
        for path in ("README.md", "UPGRADING.md", "test/hack/kwok/README.md",
                     "docs/guide.rst", "website/build.py", "website/assets/logo.png",
                     ".github/workflows/ci.yaml", ".github/actions/e2e/setup-cluster/action.yaml",
                     ".github/scripts/ci_changes.py", ".github/ISSUE_TEMPLATE/docs_website.yml",
                     "test/hack/e2e/smoke/runner.py"):
            with self.subTest(path=path):
                self.assertFalse(needs_go(path))

    def test_source_and_unknown_paths(self):
        for path in ("pkg/provider.go", "website/example.go", ".github/tools/check.go",
                     "website/go.mod", "go.mod", "go.sum", "go.work", "go.work.sum",
                     "Makefile", "Dockerfile", "charts/karpenter/Chart.yaml",
                     "test/fixtures/config.yaml", "new-unclassified-file",
                     "test/hack/e2e/smoke/check.go", "test/hack/e2e/smoke/go.mod",
                     "test/hack/e2e/smoke/config.yaml"):
            with self.subTest(path=path):
                self.assertTrue(needs_go(path))

    def test_manual_and_unclassified_events(self):
        for event in ("workflow_dispatch", "schedule", "unknown"):
            self.assertTrue(classify(event, {}).run_go)

    def test_missing_or_zero_comparison_falls_back_to_full(self):
        for before in ("", "0" * 40, "--help", "invalid"):
            self.assertTrue(classify("push", {"before": before, "after": "a" * 40}).run_go)


class GitDiffTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="karpenter-ci-routing-")
        self.addCleanup(self.temp.cleanup)
        self.repo = Path(self.temp.name)
        self.git("init", "-b", "main")
        self.git("config", "user.name", "CI routing test")
        self.git("config", "user.email", "ci@example.invalid")
        self.git("config", "commit.gpgsign", "false")
        self.git("config", "core.autocrlf", "false")
        self.write("README.md", "initial docs\n")
        self.write("pkg/source.go", "package example\n")
        self.base = self.commit()

    def git(self, *args):
        return subprocess.run(["git", *args], cwd=self.repo, check=True,
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE).stdout.decode().strip()

    def write(self, path, text):
        target = self.repo / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(text, encoding="utf-8", newline="\n")

    def commit(self):
        self.git("add", "-A")
        self.git("commit", "-m", "fixture")
        return self.git("rev-parse", "HEAD")

    def push(self, head, base=None):
        return classify("push", {"before": base or self.base, "after": head}, str(self.repo))

    def test_light_push_including_workflow(self):
        self.write(".github/workflows/ci.yaml", "name: fixture\n")
        self.write("website/index.md", "site\n")
        self.assertFalse(self.push(self.commit()).run_go)

    def test_mixed_change(self):
        self.write("README.md", "updated\n")
        self.write("pkg/source.go", "package changed\n")
        self.assertTrue(self.push(self.commit()).run_go)

    def test_ack_python_harness_only_skips_provider_checks(self):
        self.write("test/hack/e2e/smoke/runner.py", "print('fixture')\n")
        self.assertFalse(self.push(self.commit()).run_go)
        self.write("test/hack/e2e/ackctl/smoke.go", "package main\n")
        self.assertTrue(self.push(self.commit()).run_go)

    def test_deleted_source(self):
        (self.repo / "pkg/source.go").unlink()
        result = self.push(self.commit())
        self.assertTrue(result.run_go)
        self.assertIn("pkg/source.go", result.files)

    def test_rename_source_into_light_tree(self):
        self.write("website/notes.md", (self.repo / "pkg/source.go").read_text())
        (self.repo / "pkg/source.go").unlink()
        result = self.push(self.commit())
        self.assertTrue(result.run_go)
        self.assertEqual(set(result.files), {"pkg/source.go", "website/notes.md"})

    def test_light_rename_and_delete(self):
        self.write("website/renamed.md", (self.repo / "README.md").read_text())
        (self.repo / "README.md").unlink()
        self.assertFalse(self.push(self.commit()).run_go)

    def test_pull_request_uses_entire_branch(self):
        self.write("pkg/source.go", "package changed\n")
        self.commit()
        self.write("README.md", "last commit only docs\n")
        head = self.commit()
        event = {"pull_request": {"base": {"sha": self.base}, "head": {"sha": head}}}
        self.assertTrue(classify("pull_request", event, str(self.repo)).run_go)

    def test_base_branch_changes_do_not_leak_into_pr(self):
        self.git("checkout", "-b", "docs")
        self.write("README.md", "branch docs\n")
        head = self.commit()
        self.git("checkout", "main")
        self.write("pkg/source.go", "package base_changed\n")
        base = self.commit()
        event = {"pull_request": {"base": {"sha": base}, "head": {"sha": head}}}
        self.assertFalse(classify("pull_request", event, str(self.repo)).run_go)

    def test_missing_git_object_runs_full(self):
        self.assertTrue(self.push("a" * 40).run_go)

    def test_kwok_harness_runs_kwok_without_provider_go(self):
        self.write("test/hack/kwok/smoke.py", "print('fixture')\n")
        result = self.push(self.commit())
        self.assertFalse(result.run_go)
        self.assertTrue(result.run_kwok)
        self.write("pkg/source.go", "package changed\n")
        self.assertTrue(self.push(self.commit()).run_go)

    def test_numeric_kwok_make_default_keeps_build_changes_conservative(self):
        self.write("Makefile", "KWOK_SCALE_REPLICAS ?= 10\n")
        before = self.commit()
        self.write("Makefile", "KWOK_SCALE_REPLICAS ?= 5\n")
        result = self.push(self.commit(), before)
        self.assertFalse(result.run_go)
        self.assertTrue(result.run_kwok)
        self.write("Makefile", "KWOK_SCALE_REPLICAS ?= $(shell go build ./cmd/controller)\n")
        self.assertTrue(self.push(self.commit(), before).run_go)

    def test_go_file_inside_kwok_tree_requires_provider_go(self):
        self.write("test/hack/kwok/extra.go", "package kwok\n")
        self.assertTrue(self.push(self.commit()).run_go)

    def test_kwok_docs_remain_light_without_runtime(self):
        self.write("test/hack/kwok/README.md", "fixture docs\n")
        result = self.push(self.commit())
        self.assertFalse(result.run_go)
        self.assertFalse(result.run_kwok)


if __name__ == "__main__":
    unittest.main()
