"""Render and verify the bounded multi-worker KWOK smoke using only Python stdlib."""

import argparse
import copy
import json
from pathlib import Path
import sys

POOL_LABEL = "karpenter.sh/nodepool"
INSTANCE_TYPE = "c-2x-amd64-linux"


def expected_nodes(replicas):
    if not isinstance(replicas, int) or isinstance(replicas, bool) or not 1 <= replicas <= 20:
        raise ValueError("KWOK smoke replicas must be an integer between 1 and 20")
    return (replicas + 1) // 2


def render(pool, replicas):
    expected = expected_nodes(replicas)
    pool = copy.deepcopy(pool)
    name = pool["metadata"]["name"]
    spec = pool["spec"]["template"]["spec"]
    requirements = spec.setdefault("requirements", [])
    if any(r["key"] == "node.kubernetes.io/instance-type" for r in requirements):
        raise ValueError("smoke owns the instance-type constraint; use the default pool fixture")
    requirements.append({"key": "node.kubernetes.io/instance-type", "operator": "In", "values": [INSTANCE_TYPE]})
    pool["spec"].setdefault("limits", {})["cpu"] = str(2 * expected)
    workload = {
        "apiVersion": "apps/v1", "kind": "Deployment",
        "metadata": {"name": "kwok-scale", "namespace": "kwok-scale"},
        "spec": {"replicas": replicas, "selector": {"matchLabels": {"app": "kwok-scale"}},
                 "template": {"metadata": {"labels": {"app": "kwok-scale"}},
                              "spec": {"terminationGracePeriodSeconds": 0,
                                       "nodeSelector": {POOL_LABEL: name},
                                       "containers": [{"name": "pause", "image": "registry.k8s.io/pause:3.9",
                                                       "resources": {"requests": {"cpu": "800m", "memory": "64Mi"}}}]}}}}
    return pool, workload


def ready(obj):
    return any(c.get("type") == "Ready" and c.get("status") == "True"
               for c in obj.get("status", {}).get("conditions", []))


def verify(nodes, claims, pods, replicas, pool_name):
    expected = expected_nodes(replicas)
    workers = {n["metadata"]["name"]: n for n in nodes["items"]
               if n.get("metadata", {}).get("labels", {}).get(POOL_LABEL) == pool_name
               and n.get("spec", {}).get("providerID", "").startswith("kwok://")}
    owned_claims = [c for c in claims["items"]
                    if c.get("metadata", {}).get("labels", {}).get(POOL_LABEL) == pool_name]
    if len(workers) != expected or len(owned_claims) != expected:
        raise ValueError(f"expected {expected} simulated workers/NodeClaims; got {len(workers)}/{len(owned_claims)}")
    if any(n["metadata"]["labels"].get("node.kubernetes.io/instance-type") != INSTANCE_TYPE for n in workers.values()):
        raise ValueError("simulated worker uses an unexpected instance type")
    claim_nodes = []
    for claim in owned_claims:
        status = claim.get("status", {})
        node = workers.get(status.get("nodeName"))
        if not ready(claim) or node is None or not ready(node) or status.get("providerID") != node["spec"]["providerID"]:
            raise ValueError("NodeClaim/worker readiness or ProviderID does not match")
        claim_nodes.append(status["nodeName"])
    if len(set(claim_nodes)) != expected:
        raise ValueError("multiple NodeClaims refer to the same worker")
    workload_pods = [p for p in pods["items"] if p.get("metadata", {}).get("labels", {}).get("app") == "kwok-scale"]
    placement = dict.fromkeys(workers, 0)
    if len(workload_pods) != replicas:
        raise ValueError("unexpected test Pod count")
    for pod in workload_pods:
        node = pod.get("spec", {}).get("nodeName")
        if pod.get("status", {}).get("phase") != "Running" or not ready(pod) or node not in placement:
            raise ValueError("test Pod is not Ready on a simulated worker")
        placement[node] += 1
    if any(count not in (1, 2) for count in placement.values()):
        raise ValueError("test Pods must use every worker with at most two Pods each")
    return {"replicas": replicas, "ready": replicas, "nodes": len(nodes["items"]),
            "simulated_nodes": len(workers), "expected_simulated_nodes": expected,
            "nodeclaims": len(owned_claims), "placement": placement}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("command", choices=("expected-nodes", "render", "verify"))
    parser.add_argument("--replicas", type=int, default=5)
    parser.add_argument("--directory", type=Path)
    args = parser.parse_args()
    try:
        expected = expected_nodes(args.replicas)
        if args.command == "expected-nodes":
            print(expected)
            return
        if args.directory is None:
            raise ValueError("--directory is required")
        args.directory.mkdir(parents=True, exist_ok=True)
        if args.command == "render":
            pool, workload = render(json.load(sys.stdin), args.replicas)
            for name, value in (("smoke-nodepool.json", pool), ("smoke-deployment.json", workload)):
                (args.directory / name).write_text(json.dumps(value), encoding="utf-8")
        else:
            load = lambda name: json.loads((args.directory / name).read_text(encoding="utf-8"))
            result = verify(load("smoke-nodes.json"), load("smoke-nodeclaims.json"), load("smoke-pods.json"),
                            args.replicas, load("smoke-nodepool.json")["metadata"]["name"])
            print(json.dumps(result))
            lines = ["kwok_large_scale=true", "suite=smoke"] + [f"{key}={value}" for key, value in result.items() if key != "placement"]
            (args.directory / "summary.txt").write_text("\n".join(lines) + "\n", encoding="utf-8")
            (args.directory / "placement.json").write_text(json.dumps(result["placement"], indent=2), encoding="utf-8")
    except (ValueError, KeyError) as error:
        parser.error(str(error))


if __name__ == "__main__":
    main()
