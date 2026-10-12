"""Exercise the composite's actual guards without requesting an OIDC token."""
import os
from pathlib import Path
import shutil
import subprocess
import unittest

ACTION = Path(__file__).resolve().parents[1] / "actions/e2e/oidc/action.yaml"


def scripts():
    lines = ACTION.read_text(encoding="utf-8").splitlines()
    result = []
    for i, line in enumerate(lines):
        if line.strip() != "run: |":
            continue
        indent = len(line) - len(line.lstrip()) + 2
        block = []
        for child in lines[i+1:]:
            if child.strip() and len(child)-len(child.lstrip()) < indent:
                break
            block.append(child[indent:])
        result.append("\n".join(block))
    return result


class OIDCGuards(unittest.TestCase):
    def run_guard(self, index, extra):
        bash = "C:/Program Files/Git/bin/bash.exe" if os.name == "nt" else shutil.which("bash")
        if not bash:
            self.skipTest("Bash required")
        env = {**os.environ, "GITHUB_EVENT_NAME":"workflow_dispatch", "RUNNER_ROLE":"acs:ram::123:role/runner", "RUNNER_PROVIDER":"acs:ram::123:oidc-provider/github"}
        env.update(extra)
        return subprocess.run([bash,"-c",scripts()[index]],env=env,capture_output=True,text=True,timeout=10)

    def test_identity_guard_and_nonmanual_rejection(self):
        self.assertEqual(self.run_guard(0,{}).returncode,0)
        for extra in ({"GITHUB_EVENT_NAME":"pull_request"},{"RUNNER_ROLE":""},{"RUNNER_PROVIDER":""},{"RUNNER_PROVIDER":"acs:ram::456:oidc-provider/foreign"}):
            with self.subTest(extra=extra):
                self.assertNotEqual(self.run_guard(0,extra).returncode,0)

    def test_sts_guard_requires_all_three_fields_and_does_not_print_them(self):
        values = dict.fromkeys(("ALIBABA_CLOUD_ACCESS_KEY_ID","ALIBABA_CLOUD_ACCESS_KEY_SECRET","ALIBABA_CLOUD_SECURITY_TOKEN"),"synthetic-secret-not-for-output")
        result = self.run_guard(1,values)
        self.assertEqual(result.returncode,0)
        self.assertEqual(result.stdout,"")
        for field in values:
            result = self.run_guard(1,{**values,field:""})
            self.assertNotEqual(result.returncode,0)
            self.assertNotIn("synthetic-secret-not-for-output",result.stdout+result.stderr)


if __name__ == "__main__":
    unittest.main()
