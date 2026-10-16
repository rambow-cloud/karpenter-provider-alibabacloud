#!/usr/bin/env python3
"""Manual, run-owned three-worker ACK smoke. Cloud calls live in the STS ackctl."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import time
import uuid

RUN_TAG = "testing/karpenter-run"
GROUP = "karpenter.alibabacloud.com"
# ECS ownership tags use the provider's karpenter.sh contract, independently
# of the ECSNodeClass Kubernetes API group.
TAG_PREFIX = "karpenter.sh"
MANAGED = TAG_PREFIX + "/managed-by"
CLUSTER = TAG_PREFIX + "/cluster-id"
POOL = "karpenter.sh/nodepool"
CAPACITY = "karpenter.sh/capacity-type"
CUSTOM = {"NodePool":"nodepools.karpenter.sh","NodeClaim":"nodeclaims.karpenter.sh","ECSNodeClass":"ecsnodeclasses.karpenter.alibabacloud.com"}
RESOURCE_KINDS = {"nodepools":"NodePool","nodeclaims":"NodeClaim","ecsnodeclasses":"ECSNodeClass"}
CAPACITY_STABILITY_SECONDS = 300


class Failure(Exception):
    pass


class Pending(Exception):
    pass


def ready(obj, names=("Ready",)):
    conditions = {c["type"]: c for c in obj.get("status", {}).get("conditions", [])}
    for name in names:
        c = conditions.get(name, {})
        if c.get("status") != "True":
            return False
        generation = c.get("observedGeneration")
        if generation is not None and generation != obj["metadata"].get("generation", generation):
            return False
    return True


def instance_id(provider, region):
    match = re.fullmatch(r"(?:alibabacloud://)?([^./]+)\.(i-[a-zA-Z0-9-]+)", provider or "")
    if not match or match[1] != region:
        raise Failure("missing or foreign ProviderID")
    return match[2]


def fixtures(plan, run, cpu):
    smoke = plan["smoke"]
    labels = {RUN_TAG: run}
    objects = [{"apiVersion":"v1", "kind":"Namespace", "metadata":{"name":run,"labels":labels}}]
    for capacity, replicas in (("on-demand",2),("spot",1)):
        name = run + ("-od" if capacity == "on-demand" else "-spot")
        spec = {"clusterID":plan["cluster_id"],"clusterName":plan["cluster_name"],
                "ackNodePoolID":smoke["ack_node_pool_id"],"role":smoke["worker_role"],
                "vSwitchSelectorTerms":[{"id":v} for v in smoke["vswitch_ids"]],
                "securityGroupSelectorTerms":[{"id":v} for v in smoke["security_group_ids"]],
                "imageSelectorTerms":[{"id":smoke["image_id"]}],"imageCompatibilityPolicy":"ACK",
                "systemDisk":{"category":smoke["system_disk_category"],"size":smoke["system_disk_size"]},
                "tags":{RUN_TAG:run,"testing/type":"e2e"}}
        if capacity == "spot":
            spec["spotStrategy"] = "SpotAsPriceGo"
        objects.append({"apiVersion":GROUP+"/v1alpha1","kind":"ECSNodeClass","metadata":{"name":name,"labels":labels},"spec":spec})
        objects.append({"apiVersion":"karpenter.sh/v1","kind":"NodePool","metadata":{"name":name,"labels":labels},"spec":{
            "template":{"metadata":{"labels":labels},"spec":{"nodeClassRef":{"group":GROUP,"kind":"ECSNodeClass","name":name},
            "expireAfter":"Never","taints":[{"key":RUN_TAG,"value":run,"effect":"NoSchedule"}],
            "requirements":[{"key":CAPACITY,"operator":"In","values":[capacity]},
                            {"key":"node.kubernetes.io/instance-type","operator":"In","values":smoke["instance_types"]},
                            {"key":"kubernetes.io/arch","operator":"In","values":["amd64"]},
                            {"key":"kubernetes.io/os","operator":"In","values":["linux"]}]}},
            "limits":{"cpu":str(cpu*replicas)},"disruption":{"consolidateAfter":"Never","budgets":[{"nodes":"0"}]}}})
        pod = {"metadata":{"labels":labels},"spec":{"nodeSelector":{POOL:name},
            "tolerations":[{"key":RUN_TAG,"operator":"Equal","value":run,"effect":"NoSchedule"}],
            "terminationGracePeriodSeconds":0,
            "affinity":{"podAntiAffinity":{"requiredDuringSchedulingIgnoredDuringExecution":[{
                "labelSelector":{"matchLabels":labels},"topologyKey":"kubernetes.io/hostname"}]}},
            "containers":[{"name":"pause","image":smoke["pod_image"],"resources":{"requests":{"cpu":"100m","memory":"128Mi"}}}]}}
        objects.append({"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":name,"namespace":run,"labels":labels},
                        "spec":{"replicas":replicas,"selector":{"matchLabels":{**labels,"app":name}},"template":pod}})
        # Distinct Deployment selectors are required even though affinity matches
        # the shared run label across both capacity types.
        pod["metadata"]["labels"] = {**labels,"app":name}
    return objects


def verify(plan, manifest, claims, nodes, pods, instances):
    if any(len(rows)>3 for rows in (claims,nodes,pods,instances)):
        raise Failure("more than three run-owned workers/instances/Pods")
    if any(len(rows)<3 for rows in (claims,nodes,pods,instances)):
        raise Pending("waiting for three new workers and Pods")
    region, run, smoke = plan["region"],manifest["run_id"],plan["smoke"]
    expected = {run+"-od":"on-demand",run+"-spot":"spot"}
    cloud = {row["InstanceId"]:row for row in instances}
    by_node = {row["metadata"]["name"]:row for row in nodes}
    if len(cloud)!=3 or len(by_node)!=3:
        raise Failure("duplicate cloud or node identity")
    placement, capacity_counts, used = {}, {"on-demand":0,"spot":0}, set()
    for claim in claims:
        if not ready(claim,("Launched","Registered","Initialized","Ready")):
            raise Pending("NodeClaim lifecycle is not ready")
        pool = claim["metadata"]["labels"].get(POOL)
        capacity = expected.get(pool)
        if not capacity or claim["spec"]["nodeClassRef"]["name"] != pool:
            raise Failure("foreign NodeClaim pool/class")
        node = by_node.get(claim["status"].get("nodeName"))
        if not node or not ready(node):
            raise Pending("Node is not Ready")
        if node["metadata"]["uid"] in manifest["baseline_node_uids"]:
            raise Failure("baseline node cannot count as newly provisioned")
        provider = claim["status"].get("providerID")
        if node["spec"].get("providerID") != provider:
            raise Failure("Node/NodeClaim ProviderID mismatch")
        ident = instance_id(provider,region)
        if ident in used or ident in manifest["baseline_instance_ids"]:
            raise Failure("instance identity reused or predates run")
        used.add(ident)
        row = cloud.get(ident)
        if not row:
            raise Failure("Kubernetes instance is missing from authenticated cloud inventory")
        tags = row.get("Tags",{})
        if any(tags.get(k)!=v for k,v in {RUN_TAG:run,CLUSTER:plan["cluster_id"],MANAGED:"karpenter",TAG_PREFIX+"/nodepool":pool,TAG_PREFIX+"/nodeclaim":claim["metadata"]["name"],TAG_PREFIX+"/nodeclaim-uid":claim["metadata"]["uid"]}.items()):
            raise Failure("cloud ownership/NodeClaim identity mismatch")
        if row.get("Status") != "Running":
            raise Pending("ECS is not Running")
        strategy = "NoSpot" if capacity == "on-demand" else "SpotAsPriceGo"
        if row.get("InstanceChargeType")!="PostPaid" or row.get("SpotStrategy")!=strategy or node["metadata"]["labels"].get(CAPACITY)!=capacity or claim["metadata"]["labels"].get(CAPACITY)!=capacity:
            raise Failure("cloud capacity type mismatch; no Spot fallback allowed")
        if row.get("ImageId")!=smoke["image_id"] or row.get("InstanceType") not in smoke["instance_types"]:
            raise Failure("cloud image or instance type mismatch")
        if row.get("VpcAttributes",{}).get("VSwitchId") not in smoke["vswitch_ids"]:
            raise Failure("cloud vSwitch mismatch")
        groups = set(row.get("SecurityGroupIds",{}).get("SecurityGroupId",[]))
        if groups != set(smoke["security_group_ids"]):
            raise Failure("cloud security groups mismatch")
        if row.get("ZoneId")!=node["metadata"]["labels"].get("topology.kubernetes.io/zone"):
            raise Failure("cloud/node zone mismatch")
        placement[node["metadata"]["name"]] = {"instance_id":ident,"capacity_type":capacity,"nodeclaim":claim["metadata"]["name"],"nodeclaim_uid":claim["metadata"]["uid"],"provider_id":provider,"pods":[]}
        capacity_counts[capacity] += 1
    if capacity_counts != {"on-demand":2,"spot":1}:
        raise Failure("expected exactly two on-demand and one Spot")
    for pod in pods:
        if pod.get("status",{}).get("phase")!="Running" or not ready(pod) or not pod.get("status",{}).get("podIP"):
            raise Pending("Pod is not running/Ready with PodIP")
        name = pod.get("spec",{}).get("nodeName")
        if name not in placement:
            raise Failure("Pod scheduled outside newly provisioned workers")
        placement[name]["pods"].append(pod["metadata"]["name"])
    if any(len(row["pods"])!=1 for row in placement.values()):
        raise Failure("required one Pod per worker")
    return placement


class Runner:
    def __init__(self,args):
        self.args = args
        self.report = Path(args.report).resolve()
        self.private = Path(args.private).resolve()
        if self.report==self.private or self.report in self.private.parents or self.private in self.report.parents:
            raise Failure("private credentials and public report directories must not overlap")
        self.report.mkdir(parents=True,exist_ok=True,mode=0o700)
        self.private.mkdir(parents=True,exist_ok=True,mode=0o700)
        self.kubeconfig = self.private/"kubeconfig"
        self.deadline = time.monotonic()+25*60
        self.plan = self.call([args.ackctl,"smoke-config","--config",args.config])
        self.hash = hashlib.sha256(json.dumps(self.plan,sort_keys=True).encode()).hexdigest()
        self.manifest_path = self.report/"run.json"
        self.manifest = None

    def call(self,argv,stdin=None,json_result=True):
        left = self.deadline-time.monotonic()
        if left<=0:
            raise Failure("stage deadline exceeded")
        env = {**os.environ,"KUBECONFIG":str(self.kubeconfig)}
        command_timeout = 300 if Path(argv[0]).name in ("helm","kubectl") else 90
        result = subprocess.run(argv,input=stdin,capture_output=True,text=True,env=env,timeout=min(left,command_timeout))
        if result.returncode:
            # Commands may echo rejected API objects. Never forward raw stderr
            # or full arguments into a public artifact or Actions log.
            detail = ""
            if argv[1] in ("smoke-config","smoke-cloud","rrsa-values","check-rrsa-pod"):
                detail = result.stderr.strip()[:512]
                for key in ("ALIBABA_CLOUD_ACCESS_KEY_ID","ALIBABA_CLOUD_ACCESS_KEY_SECRET","ALIBABA_CLOUD_SECURITY_TOKEN"):
                    secret = os.environ.get(key)
                    if secret:
                        detail = detail.replace(secret,"<redacted>")
            raise Failure(f"{Path(argv[0]).name} {argv[1]} failed, exit={result.returncode}: {detail}")
        if json_result:
            return json.loads(result.stdout) if result.stdout.strip() else None
        return result.stdout

    def cloud(self,op,**flags):
        argv = [self.args.ackctl,"smoke-cloud","--config",self.args.config,"--operation",op]
        for k,v in flags.items():
            argv += ["--"+k.replace("_","-"),json.dumps(v) if isinstance(v,list) else str(v)]
        return self.call(argv)

    def kube(self,*args,stdin=None):
        return self.call(["kubectl",*args],stdin=stdin)

    def write(self,name,value):
        path = self.report/name
        temporary = path.with_suffix(path.suffix+".tmp")
        temporary.write_text(json.dumps(value,indent=2,sort_keys=True)+"\n",encoding="utf-8")
        os.chmod(temporary,0o600)
        temporary.replace(path)

    def save(self):
        self.write("run.json",self.manifest)

    def list(self,kind):
        if kind in RESOURCE_KINDS and CUSTOM[RESOURCE_KINDS[kind]] not in self.crds:
            return []
        return self.kube("get",kind,"-A","-l",RUN_TAG+"="+self.manifest["run_id"],"-o","json")["items"]

    def inventory(self,enforce_limit=True):
        rows = self.cloud("instances",run_id=self.manifest["run_id"])
        for row in rows:
            ident = row["InstanceId"]
            if ident in self.manifest["baseline_instance_ids"]:
                raise Failure("refusing to adopt a baseline instance")
            self.manifest["instances"][ident] = row
            # Persist identity first: a later disk/ENI API failure must not lose it.
            self.save()
            for op,key in (("disks","DiskId"),("enis","NetworkInterfaceId")):
                records = self.cloud(op,instance_id=ident)
                for record in records:
                    resource_id = record.get(key)
                    if not resource_id:
                        raise Failure("missing associated resource identity")
                    if resource_id not in self.manifest["baseline_resources"][op]:
                        self.manifest["resources"][op][resource_id] = ident
            self.save()
        self.write("instances.json",rows)
        if enforce_limit and (len(rows)>3 or len(self.manifest["instances"])>3):
            raise Failure("duplicate/extra/replacement run instances; stopping provisioning")
        if enforce_limit and self.manifest.get("vpc_id"):
            all_rows = self.cloud("vpc-instances",vpc_id=self.manifest["vpc_id"])
            new_ids = {r["InstanceId"] for r in all_rows}-set(self.manifest["baseline_vpc_instance_ids"])
            if len(new_ids)>3 or new_ids-set(self.manifest["instances"]):
                self.write("unexpected-vpc-instances.json",sorted(new_ids))
                raise Failure("unexpected untagged/native VPC capacity; no broad deletion authorized")
        return rows

    def existing(self,kind,name,namespace=None):
        if kind in CUSTOM and CUSTOM[kind] not in self.crds:
            return None
        argv = ["get",kind,name,"--ignore-not-found","-o","json"]
        if namespace:
            argv += ["-n",namespace]
        return self.kube(*argv)

    def capacity_matches_baseline(self):
        m = self.manifest
        if not m.get("baseline_native_capacity") or not m.get("vpc_id") or "baseline_vpc_instance_ids" not in m:
            raise Failure("cleanup lacks native/VPC capacity baseline; cannot claim complete zero residue")
        native = self.cloud("native-capacity")
        vpc = self.cloud("vpc-instances",vpc_id=m["vpc_id"])
        ids = sorted(r["InstanceId"] for r in vpc)
        matches = native==m["baseline_native_capacity"] and ids==m["baseline_vpc_instance_ids"]
        self.write("capacity-residue.json",{"matches_baseline":matches,"native":native,"vpc_instance_ids":ids})
        return matches

    def provision(self):
        if os.environ.get("ACK_E2E_ENABLED")!="true":
            raise Failure("ACK_E2E_ENABLED must explicitly equal true")
        if self.manifest_path.exists():
            raise Failure("run manifest already exists; use cleanup-only")
        cluster = self.cloud("cluster")
        if cluster.get("cluster_id")!=self.plan["cluster_id"] or cluster.get("name")!=self.plan["cluster_name"] or cluster.get("region_id")!=self.plan["region"]:
            raise Failure("ACK cluster identity/region mismatch")
        access = self.cloud("kubeconfig",output=self.kubeconfig)
        self.endpoint = access["endpoint"]
        self.crds = {r["metadata"]["name"] for r in self.kube("get","crds","-o","json")["items"]}
        version_info = self.kube("version","-o","json")
        version = version_info["serverVersion"]["gitVersion"]
        if not re.match(r"^v?1\.(?:3[4-9]|[4-9][0-9]|[1-9][0-9]{2,})\.",version):
            raise Failure("actual Kubernetes version is below 1.34 or invalid")
        configured = re.match(r"^v?(\d+)\.(\d+)",self.plan["kubernetes_version"])
        actual = re.match(r"^v?(\d+)\.(\d+)",version)
        ack = re.match(r"^v?(\d+)\.(\d+)",cluster["current_version"])
        if not configured or not ack or configured.groups()!=actual.groups() or ack.groups()!=actual.groups():
            raise Failure("configured, ACK and API-server Kubernetes minor versions differ")
        client = re.match(r"^v?(\d+)\.(\d+)",version_info["clientVersion"]["gitVersion"])
        if not client or client[1]!=actual[1] or abs(int(client[2])-int(actual[2]))>1:
            raise Failure("kubectl must be within one minor of the target API server")
        nodes = self.kube("get","nodes","-o","json")["items"]
        selector = self.plan["system_selector"]
        system = [n for n in nodes if ready(n) and all(n["metadata"]["labels"].get(k)==v for k,v in selector.items()) and POOL not in n["metadata"].get("labels",{})]
        if not system:
            raise Failure("no Ready fixed system worker matching controller selector")
        if self.call(["helm","list","-n","karpenter","-o","json"]):
            raise Failure("fresh deployment requires no pre-existing Helm release in karpenter")
        if self.kube("get","deployments","-A","-l","app.kubernetes.io/name=karpenter","-o","json")["items"]:
            raise Failure("existing Karpenter controller detected")
        baseline_ids = [instance_id(n["spec"].get("providerID"),self.plan["region"]) for n in nodes]
        baseline = self.cloud("instances",ids=baseline_ids)
        if {r["InstanceId"] for r in baseline} != set(baseline_ids):
            raise Failure("runner cannot observe every baseline ECS instance")
        self.cloud("native-pool")
        vpc_id = cluster.get("vpc_id")
        if not vpc_id:
            raise Failure("ACK VPC identity is required for complete capacity accounting")
        native_capacity = self.cloud("native-capacity")
        vpc_baseline = self.cloud("vpc-instances",vpc_id=vpc_id)
        if not native_capacity or "member_ids" not in native_capacity:
            raise Failure("native ACK capacity baseline is incomplete")
        baseline_resources = {"disks":[],"enis":[]}
        for op,key in (("disks","DiskId"),("enis","NetworkInterfaceId")):
            # Full pre-creation snapshot distinguishes pooled/pre-existing
            # secondary resources from resources created by this run.
            baseline_resources[op] = [r[key] for r in self.cloud("baseline-"+op)]
        system_ids = {instance_id(n["spec"]["providerID"],self.plan["region"]) for n in system}
        if any(r.get("InstanceChargeType")!="PostPaid" or r.get("SpotStrategy")!="NoSpot" for r in baseline if r["InstanceId"] in system_ids):
            raise Failure("system worker must use fixed on-demand capacity")
        types = self.cloud("types")
        if {r["InstanceTypeId"] for r in types}!=set(self.plan["smoke"]["instance_types"]):
            raise Failure("candidate type metadata incomplete")
        cpus = {r.get("CpuCoreCount") for r in types}
        if len(cpus)!=1 or not next(iter(cpus)) or any(str(r.get("CpuArchitecture")).lower() not in ("x86","x86_64","amd64") for r in types):
            raise Failure("candidates must have identical CPU counts and amd64 architecture")
        run = "ack-"+uuid.uuid4().hex[:24]
        self.manifest = {"version":1,"run_id":run,"config_hash":self.hash,"cluster_id":self.plan["cluster_id"],"region":self.plan["region"],
            "started_at":time.time(),"baseline_node_uids":[n["metadata"]["uid"] for n in nodes],"baseline_instance_ids":baseline_ids,
            "baseline_resources":baseline_resources,"instances":{},"resources":{"disks":{},"enis":{}},"objects":[],"controller_owned":False,
            "vpc_id":vpc_id,"baseline_native_capacity":native_capacity,"baseline_vpc_instance_ids":sorted(r["InstanceId"] for r in vpc_baseline),
            "kubernetes_version":version,"ack_version":cluster["current_version"],"controller_image":self.plan["controller_image"]}
        self.save()
        objects = fixtures(self.plan,run,int(next(iter(cpus))))
        for obj in objects:
            m = obj["metadata"]
            if self.existing(obj["kind"],m["name"],m.get("namespace")):
                raise Failure("planned resource already exists")
            self.manifest["objects"].append({"kind":obj["kind"],"name":m["name"],"namespace":m.get("namespace"),"uid":None})
        self.save()
        values = self.private/"rrsa-values.yaml"
        self.call([self.args.ackctl,"rrsa-values","--config",self.args.config,"--endpoint",self.endpoint,"--output",str(values)],json_result=False)
        self.call(["kubectl","apply","-f","charts/karpenter/crds"],json_result=False)
        self.crds = {r["metadata"]["name"] for r in self.kube("get","crds","-o","json")["items"]}
        self.call(["kubectl","wait","--for=condition=Established","crd/ecsnodeclasses.karpenter.alibabacloud.com","crd/nodepools.karpenter.sh","crd/nodeclaims.karpenter.sh","--timeout=60s"],json_result=False)
        # Record installation intent before Helm. A partial/time-out install is
        # still ours to clean; preflight proved the release did not exist.
        self.manifest["controller_owned"] = True
        self.save()
        self.call(["helm","upgrade","--install","karpenter","charts/karpenter","-n","karpenter","--create-namespace","--reset-values","--description","ACK-smoke:"+run,"--values",str(values)],json_result=False)
        self.call(["kubectl","rollout","status","deployment/karpenter","-n","karpenter","--timeout=5m"],json_result=False)
        controllers = self.kube("get","pods","-n","karpenter","-l","app.kubernetes.io/name=karpenter","-o","json")["items"]
        if len(controllers)!=1 or controllers[0]["spec"]["nodeName"] not in {n["metadata"]["name"] for n in system}:
            raise Failure("controller must run on the fixed system node")
        if controllers[0]["spec"]["containers"][0]["image"]!=self.plan["controller_image"]:
            raise Failure("admitted controller image is not the tested digest")
        pod_path = self.private/"controller-pod.json"
        pod_path.write_text(json.dumps(controllers[0]),encoding="utf-8")
        self.call([self.args.ackctl,"check-rrsa-pod","--config",self.args.config,"--input",str(pod_path)],json_result=False)
        self.write("identity.json",{"mode":"ACK RRSA","role_arn":self.plan["controller_role_arn"],"oidc_provider_arn":self.plan["controller_provider_arn"],"controller_image":self.plan["controller_image"]})
        for obj,record in zip(objects,self.manifest["objects"]):
            if obj["kind"]=="Deployment":
                continue
            if obj["kind"]=="ECSNodeClass":
                obj["spec"]["clusterEndpoint"] = self.endpoint
            self.apply(obj,record)
        for record in self.manifest["objects"]:
            if record["kind"] in ("ECSNodeClass","NodePool"):
                self.call(["kubectl","wait","--for=condition=Ready",record["kind"]+"/"+record["name"],"--timeout=3m"],json_result=False)
        nc = self.list("ecsnodeclasses")[0]
        zones = {r["zoneID"] for r in nc["status"]["vSwitches"]}
        for strategy in ("NoSpot","SpotAsPriceGo"):
            available = set()
            for zone in zones:
                inventory = self.cloud("capacity",zone=zone,strategy=strategy)
                for z in inventory.get("AvailableZone",[]):
                    for resource in z.get("AvailableResources",{}).get("AvailableResource",[]):
                        for candidate in resource.get("SupportedResources",{}).get("SupportedResource",[]):
                            if candidate.get("Status")=="Available":
                                available.add(candidate.get("Value"))
            if not available.intersection(self.plan["smoke"]["instance_types"]):
                raise Failure("no confirmed candidate capacity for "+strategy)
        for obj,record in zip(objects,self.manifest["objects"]):
            if obj["kind"]=="Deployment":
                self.apply(obj,record)
        while time.monotonic()<self.deadline:
            cloud = self.inventory()
            claims,nodes,pods = self.list("nodeclaims"),self.list("nodes"),self.list("pods")
            seen = set(self.manifest.get("seen_claim_uids",[]))
            seen.update(c["metadata"]["uid"] for c in claims)
            self.manifest["seen_claim_uids"] = sorted(seen)
            self.save()
            if len(seen)>3:
                raise Failure("replacement or extra NodeClaim observed; do not mask the first lifecycle failure")
            # Status evidence uses only selected fields; never upload raw specs
            # (which can include bootstrap or future credential extensions).
            try:
                placement = verify(self.plan,self.manifest,claims,nodes,pods,cloud)
                self.write("summary.json",{"result":"provisioned","workers":3,"on_demand":2,"spot":1,"ready_pods":3,"placement":placement,"elapsed_seconds":round(time.time()-self.manifest["started_at"],2)})
                return
            except Pending as waiting:
                print(f"Provisioning: {waiting}; elapsed={int(time.time()-self.manifest['started_at'])}s",flush=True)
                time.sleep(min(15,max(0,self.deadline-time.monotonic())))
        raise Failure("provisioning deadline exceeded")

    def apply(self,obj,record):
        created = self.kube("apply","-f","-","-o","json",stdin=json.dumps(obj))
        record["uid"] = created["metadata"]["uid"]
        self.save()

    def delete_owned(self,record):
        obj = self.existing(record["kind"],record["name"],record.get("namespace"))
        if not obj:
            return
        if obj["metadata"].get("labels",{}).get(RUN_TAG)!=self.manifest["run_id"] or (record.get("uid") and obj["metadata"]["uid"]!=record["uid"]):
            raise Failure("refusing deletion of foreign or recreated Kubernetes object")
        argv = ["kubectl","delete",record["kind"],record["name"],"--wait=false"]
        if record.get("namespace"):
            argv += ["-n",record["namespace"]]
        self.call(argv,json_result=False)

    def cleanup(self):
        self.deadline = time.monotonic()+10*60  # independent of provisioning timeout
        self.manifest = json.loads(self.manifest_path.read_text(encoding="utf-8"))
        m = self.manifest
        if m.get("version")!=1 or m.get("config_hash")!=self.hash or m.get("cluster_id")!=self.plan["cluster_id"] or m.get("region")!=self.plan["region"] or not re.fullmatch(r"ack-[a-f0-9]{24}",m.get("run_id","")):
            raise Failure("cleanup manifest/config identity mismatch")
        self.cloud("kubeconfig",output=self.kubeconfig)
        self.crds = {r["metadata"]["name"] for r in self.kube("get","crds","-o","json")["items"]}
        # Remove workload demand before deleting claims; no global deletions.
        for record in m["objects"]:
            if record["kind"]=="Deployment":
                self.delete_owned(record)
        self.inventory(enforce_limit=False)
        claims = self.list("nodeclaims")
        pools = {m["run_id"]+"-od",m["run_id"]+"-spot"}
        for claim in claims:
            if claim["metadata"]["labels"].get(POOL) not in pools or claim["spec"]["nodeClassRef"]["name"] not in pools:
                raise Failure("foreign NodeClaim in run selector")
            ident = instance_id(claim["status"].get("providerID"),self.plan["region"]) if claim["status"].get("providerID") else None
            if ident in m["baseline_instance_ids"]:
                raise Failure("refusing baseline instance termination")
            self.delete_owned({"kind":"NodeClaim","name":claim["metadata"]["name"],"uid":claim["metadata"]["uid"]})
        fallback = False
        stable_since = None
        normal_until = self.deadline-120
        while time.monotonic()<self.deadline:
            rows = self.inventory(enforce_limit=False)
            known = self.cloud("instances",ids=list(m["instances"])) if m["instances"] else []
            residue = {}
            for op in ("disks","enis"):
                residue[op] = self.cloud(op,ids=list(m["resources"][op])) if m["resources"][op] else []
                # After instances disappear, a resource may have lost its
                # attachment/tag before we observed it. Refuse a zero-residue
                # claim if any new regional resource cannot be accounted for;
                # never delete those unproven resources as a fallback.
                if not rows and not known:
                    key = "DiskId" if op=="disks" else "NetworkInterfaceId"
                    new = [r for r in self.cloud("baseline-"+op) if r[key] not in m["baseline_resources"][op]]
                    residue[op] += new
            claims,nodes = self.list("nodeclaims"),self.list("nodes")
            if not rows and not known and not any(residue.values()) and not claims and not nodes:
                if not self.capacity_matches_baseline():
                    # Untagged native replacements are not ours to release by
                    # guessing. Preserve evidence and fail the cost acceptance.
                    raise Failure("native ACK/VPC capacity changed after tagged workers disappeared; no broad deletion attempted")
                if stable_since is None:
                    stable_since = time.monotonic()
                if time.monotonic()-stable_since < CAPACITY_STABILITY_SECONDS:
                    print("Cleanup: tagged resources absent; observing native/VPC capacity stability",flush=True)
                    time.sleep(min(30,max(0,self.deadline-time.monotonic())))
                    continue
                for record in reversed(m["objects"]):
                    if record["kind"]!="Deployment":
                        self.delete_owned(record)
                while any(self.existing(r["kind"],r["name"],r.get("namespace")) for r in m["objects"]):
                    if time.monotonic()>=self.deadline:
                        raise Failure("owned Kubernetes objects remain after deletion")
                    time.sleep(min(5,max(0,self.deadline-time.monotonic())))
                if m["controller_owned"]:
                    release = self.call(["helm","list","-n","karpenter","--filter","^karpenter$","-o","json"])
                    if release:
                        status = self.call(["helm","status","karpenter","-n","karpenter","-o","json"])
                        if status.get("info",{}).get("description")!="ACK-smoke:"+m["run_id"]:
                            raise Failure("refusing to uninstall an unowned/replaced controller release")
                    self.call(["helm","uninstall","karpenter","-n","karpenter","--ignore-not-found"],json_result=False)
                self.write("cleanup.json",{"result":"failed-normal-termination" if fallback else "passed","instances":0,"disks":0,"enis":0,"nodeclaims":0,"nodes":0,"fallback_used":fallback,"native_vpc_baseline_restored":True,"capacity_stability_seconds":CAPACITY_STABILITY_SECONDS})
                if fallback:
                    raise Failure("fallback released ECS; normal termination acceptance failed")
                return
            stable_since = None
            if time.monotonic()>=normal_until:
                for row in rows:
                    ident = row["InstanceId"]
                    if ident not in m["instances"] or ident in m["baseline_instance_ids"]:
                        raise Failure("refusing fallback release outside recorded run")
                    self.cloud("release",run_id=m["run_id"],instance_id=ident)
                    fallback = True
            print(f"Cleanup: instances={len(known)}, claims={len(claims)}, nodes={len(nodes)}, disks={len(residue['disks'])}, enis={len(residue['enis'])}",flush=True)
            time.sleep(min(15,max(0,self.deadline-time.monotonic())))
        self.write("cleanup.json",{"result":"failed-residue-or-timeout","fallback_used":fallback})
        raise Failure("cleanup deadline exceeded; preserve manifest for cleanup-only")

    def diagnostics(self):
        if not self.manifest:
            return
        try:
            events = self.kube("get","events","-n",self.manifest["run_id"],"-o","json")["items"]
            self.write("events.json",[{"reason":e.get("reason"),"type":e.get("type"),"count":e.get("count"),"object":e.get("involvedObject",{}).get("name")} for e in events])
        except Exception:
            # Diagnostics must not replace the primary failure or expose raw
            # Kubernetes objects, userdata or unstructured controller logs.
            pass


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("mode",choices=("provision","cleanup"))
    parser.add_argument("--config",required=True)
    parser.add_argument("--ackctl",required=True)
    parser.add_argument("--report",required=True)
    parser.add_argument("--private",required=True)
    args = parser.parse_args()
    if os.environ.get("GITHUB_ACTIONS")=="true" and os.environ.get("GITHUB_EVENT_NAME")!="workflow_dispatch":
        raise SystemExit("E2E requires manual workflow dispatch")
    def interrupted(signum,frame):
        raise Failure("interrupted; cleanup-only must use the preserved manifest")
    signal.signal(signal.SIGTERM,interrupted)
    signal.signal(signal.SIGINT,interrupted)
    runner = None
    try:
        runner = Runner(args)
        getattr(runner,args.mode)()
    except (Failure,subprocess.TimeoutExpired,OSError,ValueError,KeyError) as error:
        if runner:
            runner.write(args.mode+"-failure.json",{"result":"failed","reason":str(error) if isinstance(error,Failure) else type(error).__name__})
            if args.mode=="provision":
                runner.diagnostics()
        print("ACK smoke failed: "+(str(error) if isinstance(error,Failure) else type(error).__name__),flush=True)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
