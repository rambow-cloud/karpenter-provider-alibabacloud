"""Protect immutable candidate identity and fail-closed publishing without Go."""

import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import release_bundle as bundle
from ci_changes import needs_go
from test_e2e_manual_only import events


class ReleaseContracts(unittest.TestCase):
    def test_manual_packaging_does_not_trigger_e2e_or_regular_go_ci(self):
        workflow = (bundle.ROOT / ".github/workflows/package.yaml").read_text(encoding="utf-8")
        self.assertEqual(events(workflow), {"workflow_dispatch"})
        self.assertNotIn("workflow_run", workflow)
        self.assertNotIn("uses: ./.github/workflows/e2e", workflow)
        self.assertIn("if: inputs.publish", workflow)
        self.assertIn("persist-credentials: false", workflow)
        for path in (".github/workflows/package.yaml", ".github/scripts/release_bundle.py", ".github/release.ko.yaml"):
            self.assertFalse(needs_go(path))

    def test_packaged_chart_pins_image_and_leaves_source_untouched(self):
        source = bundle.ROOT / "charts/karpenter/values.yaml"
        before = source.read_bytes()
        with tempfile.TemporaryDirectory() as directory:
            chart = Path(directory) / "chart"
            sha, digest = "a" * 40, "sha256:" + "b" * 64
            version = bundle.stage_chart(chart, sha, "ghcr.io/rambow-cloud/test", digest)
            self.assertTrue(version.endswith("-dev.sha-" + sha))
            text = (chart / "Chart.yaml").read_text()
            self.assertIn('appVersion: "' + sha + '"', text)
            self.assertNotIn("kubernetes@alibaba-inc.com", text)
            self.assertIn(bundle.SOURCE, text)
            values = (chart / "values.yaml").read_text()
            self.assertIn('tag: "' + sha + "@" + digest + '"', values)
            for crd in (bundle.ROOT / "charts/karpenter/crds").glob("*.yaml"):
                self.assertEqual(crd.read_bytes(), (chart / "crds" / crd.name).read_bytes())
        self.assertEqual(source.read_bytes(), before)

    def test_deny_registry_errors_instead_of_treating_them_as_missing(self):
        for error in ("UNAUTHORIZED", "DENIED", "connection timeout", "TLS handshake failure"):
            with self.subTest(error=error), patch.object(subprocess, "run", return_value=subprocess.CompletedProcess([], 1, "", error)):
                with self.assertRaises(ValueError):
                    bundle.existing_digest("crane", "ghcr.io/example/test:sha")
        with patch.object(subprocess, "run", return_value=subprocess.CompletedProcess([], 1, "", "MANIFEST_UNKNOWN")):
            self.assertIsNone(bundle.existing_digest("crane", "ghcr.io/example/test:sha"))

    def test_reject_tagged_or_traversing_repository(self):
        for value in ("oci://ghcr.io/o/r", "ghcr.io/o/r:latest", "ghcr.io/o/r@sha256:a", "ghcr.io/o/../r", "ghcr.io//r"):
            self.assertFalse(bundle.valid_repository(value), value)

    def test_ko_destination_uses_posix_relative_path_on_windows(self):
        self.assertEqual(bundle.ko_layout_path(bundle.ROOT / ".e2e/release/image-layout"), ".e2e/release/image-layout")

    def test_corrupted_oci_blob_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            layout = Path(directory)
            blob = layout / "blobs/sha256" / hashlib.sha256(b"original").hexdigest()
            blob.parent.mkdir(parents=True)
            blob.write_bytes(b"changed!")
            with self.assertRaises(ValueError):
                bundle.read_blob(layout, {"digest": "sha256:" + blob.name, "size": 8})

    def test_wrong_revision_or_cgo_binary_cannot_be_packaged(self):
        import struct
        with tempfile.TemporaryDirectory() as directory:
            binary = Path(directory) / "controller"
            data = bytearray(64)
            data[:6] = b"\x7fELF\x02\x01"
            struct.pack_into("<H", data, 18, 62)
            binary.write_bytes(data)
            for info in ("CGO_ENABLED=1\nvcs.revision=" + "a" * 40 + "\nvcs.modified=false", "CGO_ENABLED=0\nvcs.revision=" + "b" * 40 + "\nvcs.modified=false", "CGO_ENABLED=0\nvcs.revision=" + "a" * 40 + "\nvcs.modified=true"):
                with self.subTest(info=info), patch.object(bundle, "call", return_value=info):
                    with self.assertRaises(ValueError):
                        bundle.binary_evidence(binary, "amd64", "a" * 40, "go")


if __name__ == "__main__":
    unittest.main()
