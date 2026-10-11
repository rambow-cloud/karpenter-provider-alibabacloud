import unittest

from pr_policy import DEVELOPMENT_BATCH, SECTIONS, validate_issues, validate_metadata


class PolicyTests(unittest.TestCase):
    def pr(self, branch="feat/15-ecs-create-idempotency", base="dev"):
        return {"head": {"ref": branch}, "base": {"ref": base}, "title": "fix: recover instance creation", "body": "Closes #15\n" + "\n".join("## " + s for s in SECTIONS)}

    def test_feature_targets_dev_with_exact_primary_reference(self):
        self.assertEqual(validate_metadata(self.pr()), 15)
        with self.assertRaisesRegex(AssertionError, "target dev"):
            validate_metadata(self.pr(base="main"))
        pr = self.pr()
        pr["body"] = pr["body"].replace("#15", "#150")
        with self.assertRaisesRegex(AssertionError, "Closes"):
            validate_metadata(pr)

    def test_integration_direction_and_every_issue_gate(self):
        self.assertIsNone(validate_metadata(self.pr(branch="dev", base="main")))
        with self.assertRaisesRegex(AssertionError, "target main"):
            validate_metadata(self.pr(branch="dev"))
        visited = []
        def completed(number):
            visited.append(number)
            return {"state": "closed", "state_reason": "completed"}
        validate_issues(None, completed)
        self.assertEqual(tuple(visited), DEVELOPMENT_BATCH)
        for unresolved in DEVELOPMENT_BATCH:
            with self.subTest(issue=unresolved), self.assertRaisesRegex(AssertionError, f"#{unresolved}"):
                validate_issues(None, lambda n: {"state": "open" if n == unresolved else "closed", "state_reason": "completed"})
        with self.assertRaisesRegex(AssertionError, "not completed"):
            validate_issues(None, lambda _: {"state": "closed", "state_reason": "not_planned"})

    def test_issue_identity_open_state_and_query_failure(self):
        validate_issues(15, lambda _: {"state": "open"})
        for value in ({"state": "closed"}, {"state": "open", "pull_request": {}}):
            with self.subTest(value=value), self.assertRaises(AssertionError):
                validate_issues(15, lambda _: value)
        def denied(_):
            raise OSError("API denied")
        with self.assertRaises(OSError):
            validate_issues(None, denied)


if __name__ == "__main__":
    unittest.main()
