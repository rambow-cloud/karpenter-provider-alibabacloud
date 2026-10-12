import copy
import importlib.util
from pathlib import Path
import sys
import unittest

path = Path(__file__).resolve().parents[2] / "test/hack/kwok/smoke.py"
spec = importlib.util.spec_from_file_location("kwok_smoke", path)
smoke = importlib.util.module_from_spec(spec)
previous_bytecode_setting = sys.dont_write_bytecode
try:
    sys.dont_write_bytecode = True
    spec.loader.exec_module(smoke)
finally:
    sys.dont_write_bytecode = previous_bytecode_setting


def fixture():
    conditions = [{"type": "Ready", "status": "True"}]
    nodes = {"items": [{"metadata": {"name": "control-plane"}, "spec": {"providerID": "kind://control"}}]}
    claims, pods = {"items": []}, {"items": []}
    for i in range(3):
        name, provider = f"worker-{i}", f"kwok://worker-{i}"
        nodes["items"].append({"metadata": {"name": name, "labels": {smoke.POOL_LABEL: "default", "node.kubernetes.io/instance-type": smoke.INSTANCE_TYPE}},
                               "spec": {"providerID": provider}, "status": {"conditions": conditions}})
        claims["items"].append({"metadata": {"labels": {smoke.POOL_LABEL: "default"}},
                                "status": {"nodeName": name, "providerID": provider, "conditions": conditions}})
    for i in range(5):
        pods["items"].append({"metadata": {"labels": {"app": "kwok-scale"}},
                              "spec": {"nodeName": f"worker-{i // 2}"},
                              "status": {"phase": "Running", "conditions": conditions}})
    return nodes, claims, pods


class SmokeTests(unittest.TestCase):
    def test_small_bounded_packing_and_preserved_fixture(self):
        pool = {"metadata": {"name": "default"}, "spec": {"limits": {"cpu": "1000"},
                 "template": {"spec": {"nodeClassRef": {"name": "custom"}, "requirements": []}}}}
        original = copy.deepcopy(pool)
        rendered, workload = smoke.render(pool, 5)
        self.assertEqual(pool, original)
        self.assertEqual(rendered["spec"]["limits"]["cpu"], "6")
        self.assertEqual(rendered["spec"]["template"]["spec"]["nodeClassRef"]["name"], "custom")
        self.assertEqual(workload["spec"]["replicas"], 5)
        self.assertEqual(workload["spec"]["template"]["spec"]["containers"][0]["resources"]["requests"]["cpu"], "800m")
        for value in (0, -1, 21, "5", True):
            with self.assertRaises(ValueError):
                smoke.expected_nodes(value)

    def test_counts_actual_workers_and_claim_identity(self):
        result = smoke.verify(*fixture(), 5, "default")
        self.assertEqual((result["ready"], result["nodes"], result["simulated_nodes"], result["nodeclaims"]), (5, 4, 3, 3))
        self.assertEqual(sorted(result["placement"].values()), [1, 2, 2])

    def test_bad_counts_wrong_placement_and_unready_claims_fail(self):
        for change in (lambda n, c, p: n["items"].pop(),
                       lambda n, c, p: p["items"][0]["spec"].update(nodeName="control-plane"),
                       lambda n, c, p: c["items"][0]["status"].update(providerID="kwok://other"),
                       lambda n, c, p: c["items"][0]["status"].update(conditions=[])):
            nodes, claims, pods = fixture()
            change(nodes, claims, pods)
            with self.assertRaises(ValueError):
                smoke.verify(nodes, claims, pods, 5, "default")


if __name__ == "__main__":
    unittest.main()
