# Bounded KWOK smoke

`make kwok-core-e2e` and the E2EKWOK workflow default to **5 Pods**. The smoke
preset selects the pinned core catalog's `c-2x-amd64-linux` (2 CPUs, 100m CPU
reserve), with each test Pod requesting 800m CPU and 64Mi. Two Pods fit per
worker; three do not. Five Pods therefore target **3 simulated workers** with
placement 2/2/1. The rendered smoke NodePool limits CPU to six, and test Pods
select that pool. The source fixture is not modified.

Smoke replicas are bounded to 1–20, with a worker target of `ceil(replicas/2)`.
The input is checked before workflow tool installation. Regression/performance
remain opt-in and retain the general NodePool fixture and their upstream scope.

After deployment rollout and NodeClaim Ready, the check validates actual
worker/NodeClaim counts, matching ProviderIDs, and all test Pods Ready on those
workers. Control-plane and other pools do not count as simulated workers.
`summary.txt` reports both total nodes and `simulated_nodes`; `placement.json`
records the distribution. Unexpected counts or placements fail the check.

The E2EKWOK workflow runs **only after manual dispatch**, including after harness
changes. PRs, pushes and schedules do not start a cluster. The selected manual
run uses dedicated `run_kwok` routing and may build the pinned core controller;
ordinary provider CI keeps its independent change routing. Use `suite=smoke`
and `replicas=5` for the bounded check; regression/performance require a separate
explicit selection. Workflow registrations using older main definitions remain
disabled until main receives the manual-only change; merging dev alone does not
restore the Actions manual entry point.

This smoke exercises core provisioning using simulated nodes. It does not run
real ECS/ACK APIs; live acceptance stays in #5/#23. The upstream implementation
and catalog are pinned with core:
[KWOK resources](https://github.com/kubernetes-sigs/karpenter/blob/v1.14.1/kwok/cloudprovider/helpers.go),
[instance catalog](https://github.com/kubernetes-sigs/karpenter/blob/v1.14.1/kwok/cloudprovider/instance_types.json).
