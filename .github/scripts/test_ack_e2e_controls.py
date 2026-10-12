"""Run the actual workflow shell guards without Go, credentials or cloud calls."""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

WORKFLOW = Path(__file__).resolve().parents[1] / "workflows/e2e.yaml"
INPUTS = ("ALIBABA_CLOUD_E2E_CONFIG", "ALIBABA_CLOUD_ACCESS_KEY_ID",
          "ALIBABA_CLOUD_ACCESS_KEY_SECRET")


def step_script(name):
    lines = WORKFLOW.read_text(encoding="utf-8").splitlines()
    start = next(i for i, line in enumerate(lines)
                 if line.strip() in (f"- name: {name}", f"name: {name}"))
    run = next(i for i in range(start, len(lines)) if lines[i].strip() == "run: |")
    indent = len(lines[run]) - len(lines[run].lstrip()) + 2
    script = []
    for line in lines[run + 1:]:
        if line.strip() and len(line) - len(line.lstrip()) < indent:
            break
        script.append(line[indent:])
    return "\n".join(script)


class ACKWorkflowGuards(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.bash = ("C:/Program Files/Git/bin/bash.exe" if os.name == "nt"
                    else shutil.which("bash"))
        if not cls.bash or not Path(cls.bash).exists():
            raise unittest.SkipTest("Bash is needed for workflow shell contracts")

    def run_step(self, name, env):
        return subprocess.run([self.bash, "-c", step_script(name)],
                              env={**os.environ, **env}, capture_output=True,
                              text=True, timeout=10)

    def test_missing_and_blank_inputs_fail_without_secret_values(self):
        for missing in INPUTS:
            for value in ("", " \t\n"):
                with self.subTest(missing=missing, value=value):
                    env = dict.fromkeys(INPUTS, "offline-value-must-not-be-printed")
                    env[missing] = value
                    result = self.run_step("Check required ACK E2E inputs", env)
                    self.assertEqual(result.returncode, 1)
                    self.assertIn(missing, result.stdout)
                    self.assertNotIn("offline-value-must-not-be-printed",
                                     result.stdout + result.stderr)

    def test_complete_inputs_pass_silently(self):
        result = self.run_step("Check required ACK E2E inputs",
                               dict.fromkeys(INPUTS, "offline-nonempty-value"))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "")

    def test_only_existing_manifest_authorizes_failure_hooks(self):
        # No setup outcome or composite outputs are required: partial setup must
        # still be cleaned up if it persisted a manifest before failing.
        with tempfile.TemporaryDirectory(prefix="ack-workflow-guards-") as directory:
            output = Path(directory) / "outputs"
            manifest = Path(directory) / "manifest.yaml"
            env = {"RUN_MANIFEST": manifest.as_posix(), "GITHUB_OUTPUT": output.as_posix()}
            result = self.run_step("Locate ACK run manifest", env)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertFalse(output.exists(), "pre-setup failure has no manifest to clean")
            manifest.write_text("clusterID: offline-cluster\n", encoding="utf-8")
            result = self.run_step("Locate ACK run manifest", env)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(output.read_text(encoding="utf-8").strip(),
                             f"manifest={manifest.as_posix()}")


if __name__ == "__main__":
    unittest.main()
