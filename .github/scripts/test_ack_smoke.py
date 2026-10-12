"""Real decision/cleanup paths using fake transports: no cloud, kubectl or Go."""
import importlib.util
import json
from pathlib import Path
import tempfile
import sys
from types import SimpleNamespace
import unittest
from unittest.mock import patch

FILE = Path(__file__).resolve().parents[2]/"test/hack/e2e/smoke/runner.py"
spec = importlib.util.spec_from_file_location("ack_smoke",FILE)
smoke = importlib.util.module_from_spec(spec)
old_bytecode = sys.dont_write_bytecode
try:
    sys.dont_write_bytecode = True
    spec.loader.exec_module(smoke)
finally:
    sys.dont_write_bytecode = old_bytecode


def data():
    run = "ack-"+"a"*24
    plan = {"region":"cn-hangzhou","cluster_id":"c-test","cluster_name":"test","smoke":{"ack_node_pool_id":"np-test","worker_role":"worker","vswitch_ids":["vsw-test"],"security_group_ids":["sg-test"],"image_id":"image-test","instance_types":["ecs.test"],"system_disk_category":"cloud_essd","system_disk_size":40,"pod_image":"pause@sha256:"+"a"*64}}
    manifest = {"run_id":run,"baseline_node_uids":["baseline-uid"],"baseline_instance_ids":["i-base"]}
    claims,nodes,pods,instances = [],[],[],[]
    for i,capacity in enumerate(("on-demand","on-demand","spot")):
        pool = run+("-spot" if capacity=="spot" else "-od")
        name,ident,uid = f"node-{i}",f"i-{i}",f"claim-uid-{i}"
        labels = {smoke.POOL:pool,smoke.CAPACITY:capacity,smoke.RUN_TAG:run,"topology.kubernetes.io/zone":"zone-test"}
        claims.append({"metadata":{"name":f"claim-{i}","uid":uid,"labels":labels},"spec":{"nodeClassRef":{"name":pool}},"status":{"nodeName":name,"providerID":"cn-hangzhou."+ident,"conditions":[{"type":c,"status":"True"} for c in ("Ready","Launched","Registered","Initialized")]}})
        nodes.append({"metadata":{"name":name,"uid":f"node-uid-{i}","labels":labels},"spec":{"providerID":"cn-hangzhou."+ident},"status":{"conditions":[{"type":"Ready","status":"True"}]}})
        pods.append({"metadata":{"name":f"pod-{i}"},"spec":{"nodeName":name},"status":{"phase":"Running","podIP":"10.0.0.1","conditions":[{"type":"Ready","status":"True"}]}})
        instances.append({"InstanceId":ident,"InstanceChargeType":"PostPaid","SpotStrategy":"SpotAsPriceGo" if capacity=="spot" else "NoSpot","Status":"Running","ZoneId":"zone-test","ImageId":"image-test","InstanceType":"ecs.test","VpcAttributes":{"VSwitchId":"vsw-test"},"SecurityGroupIds":{"SecurityGroupId":["sg-test"]},"Tags":{smoke.RUN_TAG:run,smoke.CLUSTER:"c-test",smoke.MANAGED:"karpenter",smoke.GROUP+"/nodepool":pool,smoke.GROUP+"/nodeclaim":f"claim-{i}",smoke.GROUP+"/nodeclaim-uid":uid}})
    return plan,manifest,claims,nodes,pods,instances


class SmokeContracts(unittest.TestCase):
    def test_fresh_cluster_without_custom_crds_does_not_query_missing_resources(self):
        runner = smoke.Runner.__new__(smoke.Runner)
        runner.crds = set()
        runner.kube = lambda *args,**kwargs: self.fail("missing custom resource must not reach kubectl")
        self.assertIsNone(runner.existing("ECSNodeClass","planned"))
        self.assertEqual(runner.list("nodeclaims"),[])

    def test_exact_cloud_capacity_and_unique_mapping(self):
        self.assertEqual(len(smoke.verify(*data())),3)
        for change in ("fallback","duplicate","baseline","wrong_cloud_uid","wrong_network","misplaced_pod","pending"):
            with self.subTest(change=change):
                args = data()
                if change=="fallback": args[5][2]["SpotStrategy"]="NoSpot"
                if change=="duplicate": args[5][2]["InstanceId"]="i-0"
                if change=="baseline": args[3][0]["metadata"]["uid"]="baseline-uid"
                if change=="wrong_cloud_uid": args[5][0]["Tags"][smoke.GROUP+"/nodeclaim-uid"]="foreign"
                if change=="wrong_network": args[5][0]["VpcAttributes"]["VSwitchId"]="foreign"
                if change=="misplaced_pod": args[4][2]["spec"]["nodeName"]="node-0"
                if change=="pending": args[2][0]["status"]["conditions"][0]["status"]="False"
                with self.assertRaises(smoke.Pending if change=="pending" else smoke.Failure):
                    smoke.verify(*args)

    def test_small_pods_force_distinct_nodes_with_no_capacity_fallback(self):
        plan,manifest,*_ = data()
        objects = smoke.fixtures(plan,manifest["run_id"],4)
        deployments = [o for o in objects if o["kind"]=="Deployment"]
        self.assertEqual([o["spec"]["replicas"] for o in deployments],[2,1])
        for d in deployments:
            pod = d["spec"]["template"]
            self.assertNotIn("nodeName",pod["spec"])
            self.assertIn("requiredDuringSchedulingIgnoredDuringExecution",pod["spec"]["affinity"]["podAntiAffinity"])
            self.assertEqual(d["spec"]["selector"]["matchLabels"],pod["metadata"]["labels"])
        pools = [o for o in objects if o["kind"]=="NodePool"]
        self.assertEqual([o["spec"]["limits"]["cpu"] for o in pools],["8","4"])
        self.assertEqual([o["spec"]["template"]["spec"]["requirements"][0]["values"] for o in pools],[["on-demand"],["spot"]])

    def test_cleanup_resets_timeout_and_rejects_foreign_manifest(self):
        with tempfile.TemporaryDirectory() as tmp:
            runner = smoke.Runner.__new__(smoke.Runner)
            runner.manifest_path = Path(tmp)/"run.json"
            runner.manifest_path.write_text(json.dumps({"version":1,"config_hash":"foreign"}))
            runner.hash = "correct"
            runner.plan = {"cluster_id":"c-test","region":"cn-hangzhou"}
            runner.deadline = -1
            with self.assertRaises(smoke.Failure):
                runner.cleanup()
            self.assertGreater(runner.deadline,0)

    def test_cleanup_deletion_checks_uid_and_run_not_just_name(self):
        runner = smoke.Runner.__new__(smoke.Runner)
        runner.manifest = {"run_id":"ack-"+"a"*24}
        runner.existing = lambda *args: {"metadata":{"uid":"replaced","labels":{smoke.RUN_TAG:runner.manifest["run_id"]}}}
        runner.call = lambda *args,**kwargs: self.fail("foreign object must never reach delete")
        with self.assertRaises(smoke.Failure):
            runner.delete_owned({"kind":"NodePool","name":"pool","uid":"original"})

    def test_duplicate_inventory_is_persisted_and_cleanup_can_still_read_it(self):
        plan,m,_,_,_,rows = data()
        rows = rows+[{**rows[0],"InstanceId":"i-extra"}]
        runner = smoke.Runner.__new__(smoke.Runner)
        runner.manifest = {**m,"instances":{},"resources":{"disks":{},"enis":{}},"baseline_resources":{"disks":[],"enis":[]}}
        runner.cloud = lambda op,**kwargs: rows if op=="instances" else []
        runner.save = lambda: None
        runner.write = lambda *args: None
        with self.assertRaises(smoke.Failure): runner.inventory()
        self.assertIn("i-extra",runner.manifest["instances"])
        self.assertEqual(len(runner.inventory(enforce_limit=False)),4)

    def cleanup_runner(self,tmp,stuck=False,cloud_denied=False):
        plan,m,claims,nodes,pods,rows = data()
        runner = smoke.Runner.__new__(smoke.Runner)
        runner.report = Path(tmp)
        runner.kubeconfig = Path(tmp)/"private-kubeconfig"
        runner.manifest_path = Path(tmp)/"run.json"
        runner.plan,runner.hash = plan,"correct"
        runner.deadline = -1
        records = [{"kind":"Deployment","name":m["run_id"]+"-od","namespace":m["run_id"],"uid":"dep-uid"}]
        manifest = {**m,"version":1,"config_hash":"correct","region":plan["region"],"cluster_id":plan["cluster_id"],"objects":records,"instances":{},"resources":{"disks":{},"enis":{}},"baseline_resources":{"disks":[],"enis":[]},"controller_owned":False}
        runner.manifest_path.write_text(json.dumps(manifest))
        state = {"claims":list(claims),"instances":list(rows),"workload":True,"released":[]}
        def cloud(op,**kwargs):
            if op=="instances":
                if cloud_denied and not state["claims"]:
                    raise smoke.Failure("cloud permission denied")
                return list(state["instances"])
            if op=="release":
                ident = kwargs["instance_id"]
                state["released"].append(ident)
                state["instances"] = [r for r in state["instances"] if r["InstanceId"]!=ident]
                return {}
            return {} if op=="kubeconfig" else []
        def call(argv,**kwargs):
            if argv[1]=="delete":
                if argv[2]=="Deployment": state["workload"]=False
                if argv[2]=="NodeClaim":
                    self.assertFalse(state["workload"],"demand must stop before claim deletion")
                    state["claims"] = [c for c in state["claims"] if c["metadata"]["name"]!=argv[3]]
                    if not stuck and not state["claims"]: state["instances"] = []
            return ""
        runner.cloud,runner.call = cloud,call
        runner.kube = lambda *argv: {"items":[{"metadata":{"name":name}} for name in smoke.CUSTOM.values()]}
        runner.list = lambda kind: list(state["claims"]) if kind=="nodeclaims" else []
        runner.existing = lambda kind,name,namespace=None: {"metadata":{"uid":"dep-uid" if kind=="Deployment" else next(c["metadata"]["uid"] for c in claims if c["metadata"]["name"]==name),"labels":{smoke.RUN_TAG:m["run_id"]}}} if (kind=="Deployment" and state["workload"]) or (kind=="NodeClaim" and any(c["metadata"]["name"]==name for c in state["claims"])) else None
        return runner,state

    def test_actual_cleanup_path_is_independent_idempotent_and_stops_demand(self):
        with tempfile.TemporaryDirectory() as tmp:
            runner,state = self.cleanup_runner(tmp)
            runner.cleanup()
            result = json.loads((Path(tmp)/"cleanup.json").read_text())
            self.assertEqual(result["result"],"passed")
            self.assertEqual(state["instances"],[])
            self.assertEqual(state["released"],[],"normal termination must not use fallback")
            runner.cleanup()
            self.assertEqual(json.loads((Path(tmp)/"cleanup.json").read_text())["result"],"passed")

    def test_fallback_cleanup_is_never_counted_as_normal_termination_pass(self):
        with tempfile.TemporaryDirectory() as tmp:
            runner,state = self.cleanup_runner(tmp,stuck=True)
            ticks = iter([0,1,500,501,502,503,504,505,506])
            with patch.object(smoke.time,"monotonic",side_effect=lambda: next(ticks)),patch.object(smoke.time,"sleep"):
                with self.assertRaises(smoke.Failure): runner.cleanup()
            self.assertEqual(set(state["released"]),{"i-0","i-1","i-2"})
            self.assertEqual(json.loads((Path(tmp)/"cleanup.json").read_text())["result"],"failed-normal-termination")

    def test_cloud_query_error_cannot_be_reported_as_zero_residue(self):
        with tempfile.TemporaryDirectory() as tmp:
            runner,state = self.cleanup_runner(tmp,cloud_denied=True)
            with self.assertRaises(smoke.Failure): runner.cleanup()
            self.assertFalse((Path(tmp)/"cleanup.json").exists())
            self.assertTrue((Path(tmp)/"run.json").exists())


if __name__ == "__main__":
    unittest.main()
