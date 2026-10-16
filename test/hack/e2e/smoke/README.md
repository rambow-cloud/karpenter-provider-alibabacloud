# Manual three-worker ACK smoke

This implements #23 using #88's separate GitHub OIDC runner and ACK RRSA
controller. Success requires **three new Karpenter workers: two on-demand and
one Spot**, with three lightweight Pods placed one per worker. A fixed system
worker remains outside the run and hosts the controller; it is not part of the
three new workers. This is lifecycle/Spot creation evidence, not #20's Spot
interruption/notification/drain acceptance.

Local live execution began on 2026-10-16; see
[the environment status](../../../../.github/ACK_OIDC_SETUP.md) for evidence and
remaining acceptance. Local maintainer OAuth STS is distinct from the workflow's
GitHub runner OIDC. The workflow is manual-only;
ordinary PR/CI does not launch it. It does not create ACK, VPC, vSwitch, security
groups, native node pools, RAM roles or OIDC providers. It uses a dedicated,
existing ACK and installs a fresh test controller. Shared CRDs and the controller
namespace are retained as cluster test infrastructure after cleanup.

## Prepare once after an ACK exists

1. Select a dedicated conventional ACK with ECS workers, actual Kubernetes
   >=1.34, RRSA enabled, reachable API endpoint and a Ready **fixed on-demand**
   system worker. Label the system worker to match `system_node_selector`.
   Virtual/serverless workers and a pre-existing Karpenter controller are not
   supported by this fresh-install smoke.
2. Pin an active regular native ACK containerd pool with a supported kubelet
   resource model, no custom userdata/auto-mode and **autoscaling disabled**.
   ACK attach can increase native desired capacity even with autoscaling off
   (#101). System-pool reuse needs live revalidation with coordinated removal;
   a separate zero-capacity template pool alone does not prove exclusive
   capacity ownership. The provider does not create or reconfigure pools, but
   termination now removes the exact owned native member and decrements its
   capacity through ACK before deleting ECS. Select 1..5
   confirmed amd64 instance types with identical vCPU counts, explicit vSwitch
   and security-group IDs, worker RAM role, system disk category/size and an
   exact public image ID accepted by the target ACK's regional/version metadata.
   The controller's NodeClass Ready check enforces ACK image compatibility.
3. Build/publish a controller from the tested candidate and pin its full Git SHA
   tag plus immutable digest. Use matching checked-out Chart/CRDs; publishing an
   image is not performed by this smoke. Pin the small Pod image to a digest too.
4. Configure the separate RRSA role/SA (`karpenter/karpenter`) and runner role's
   cloud permissions and ACK Kubernetes RBAC. Start from the provider's current
   README controller permission list; limit worker PassRole appropriately.
   The controller additionally needs cluster-scoped `cs:DescribeClusterNodes`
   and `cs:RemoveNodePoolNodes`, plus `cs:DescribeTaskInfo` (only `Resource: "*"`
   is supported for this read). Karpenter drains first; native removal uses
   `drain_node=false` and `release_node=false`, persists the task receipt, and
   waits for success and membership absence before exact ECS release. Failed
   or ambiguous tasks retain the finalizer; never bypass them by deleting ECS.
   Before submission the controller writes an optimistic-lock removal intent
   to `karpenter.alibabacloud.com/ack-removal` on the NodeClaim. If the outcome
   is unknown (for example, a restart before saving `taskID`), automatic native
   removal stops. Inspect the exact ACK task and restore its verified `taskID`
   in that receipt; retain its cluster/pool/instance/claim identity fields. Only
   clear an intent after proving ACK never accepted it. Do not remove finalizers
   or retry a potentially completed native-capacity decrement blindly.
   The runner additionally needs ACK detail/native-pool/node/temp-kubeconfig reads;
   ECS DescribeInstances/InstanceTypes/AvailableResource/Disks/NetworkInterfaces
   and exact run-owned DeleteInstances for fallback. Describe authorization
   dry-run requires `DryRunOperation`, including before absence assertions.
   Disk/ENI snapshots require regional read visibility; their broad reads never
   authorize broad deletion. Kubernetes permissions include CRD application,
   controller Helm resources, Nodes, events and this run's custom/workload
   resources. RAM and Kubernetes RBAC are separate.
5. Fill `.github/ack-e2e-config.example.yaml` without cloud credentials and store
   it as the `ack-e2e` Environment secret `ALIBABA_CLOUD_E2E_CONFIG`. Its region,
   cluster name/ID, version and controller RRSA ARN must match actual metadata.
   Keep the already prepared runner OIDC Environment variables. Grant the runner
   only the cluster-scoped reads, RBAC and run-owned cleanup described above.

## Dispatch the bounded test

Publish the reviewed workflow to the default branch at a separately authorized
main checkpoint, then set repository `ACK_E2E_ENABLED=true` for the intended
manual validation. The environment permits only dev/main. Select a kubectl
version within one minor of the actual ACK; the runner checks the skew.

```shell
gh workflow run e2e-smoke.yaml --repo rambow-cloud/karpenter-provider-alibabacloud --ref dev -f mode=provision -f kubectl_version=v1.35.0
```

This is a future live-run command, not an instruction to run it during ordinary
implementation review. A workflow must exist on the default branch for manual
dispatch. No PR, push, schedule, repository event or workflow-completion trigger
is registered. Tools and the CLI are prepared before the initial OIDC exchange.

The runner allocates a random run ID and atomically persists `run.json` before
any owned workload/release mutation. Two exclusive NodePools and required Pod
anti-affinity force 2/1 capacity and one Pod per node. NodePool CPU limits bound
the target shape; non-atomic creation races are caught by actual cloud inventory.
Extra instances are recorded and the test fails; cleanup can still read/release
them. Spot inventory must be available; fallback to on-demand never passes.
Both test NodePools explicitly set `consolidateAfter: Never` as required by the
Karpenter v1 disruption schema, in addition to their zero voluntary-disruption
budgets.

It checks Launched/Registered/Initialized/Ready, Node Ready, Pod Running/Ready/IP,
unique ProviderIDs/instance IDs and ECS Running/charge type/SpotStrategy/image/
type/zone/network plus provider cluster/run/NodeClaim UID tags. Existing workers
cannot count as new capacity. Actual versions, identities and placement appear
in the sanitized report. Snapshot/permission/partial-body errors fail closed.

## Cleanup and interrupted runs

Cleanup always follows provisioning in the same workflow, even after partial
setup. It exchanges a **new** GitHub OIDC/STS session first and only runs cleanup
if that exchange succeeds. It has an independent ten-minute deadline. It removes
demand, deletes exact owned NodeClaims and waits for Kubernetes and cloud
termination. It does not remove finalizers or globally delete pools/classes.
Only proven recorded/run-owned, non-native ECS can be released by fallback;
native membership/tags refuse a direct ECS fallback. Using any fallback still
fails normal termination acceptance. No additional runner CS write is needed.

Before mutation, capture native desired size, total count, exact member IDs and
all ECS IDs in the ACK VPC, including instances without Karpenter tags. After
tagged resources disappear, require those baselines to remain restored for five
minutes within the independent cleanup deadline. Native replacements or a
changed desired size fail acceptance and remain evidence; do not delete
unattributed replacements. Old manifests without this baseline cannot claim
complete zero residue. The earlier 2026-10-16 deletion result was insufficient
because delayed native replacements escaped its run-tag queries.

Both the original manifest and partial-resource inventory are uploaded as
`ack-smoke-RUN_ID`. After a hard cancellation, disable new creation, select the
same configuration and manually dispatch cleanup with that **GitHub run ID**:

```shell
gh workflow run e2e-smoke.yaml --repo rambow-cloud/karpenter-provider-alibabacloud --ref dev -f mode=cleanup -f source_run_id=ORIGINAL_GITHUB_RUN_ID -f kubectl_version=v1.35.0
```

Cleanup remains available with `ACK_E2E_ENABLED=false`, refreshes credentials and
uses the authenticated artifact from this repository. Config/cluster/region hash
mismatches or replaced Kubernetes UIDs are refused. Successful cleanup can be
repeated. Keep the original report until the run is confirmed clean; after
artifact expiration, use a locally retained `run.json` with the stage command
and a fresh STS session rather than guessing ownership.

The stage CLI accepts `provision` and `cleanup` with `--config`, `--ackctl`,
`--private` and `--report`. Provision alone is not a complete test: a caller must
always invoke cleanup with fresh credentials. The GitHub workflow supplies that
orchestration. Local stages require a complete temporary STS session; the local
OAuth profile used to prepare identity is not implicitly used as runner identity.

## Evidence and limits

Provisioning/setup has an overall 25-minute deadline; cleanup has ten minutes,
within the workflow's 45-minute cap including tools. Interruptions preserve the
manifest. A hard runner kill can prevent cleanup, hence cleanup-only and residue
checks remain required. Clear the creation opt-in after planned validation.

Only sanitized ownership, identity, cloud fields, event reasons, placement,
failures and cleanup results are uploaded. Private configuration, kubeconfig,
Pod specs, JWT/STS values and raw userdata/controller logs are excluded. Use the
stage/error code and event reasons to diagnose failure; raw sensitive logs need
a separately controlled collection on the selected test cluster.

The disk/ENI baseline is taken before mutation. Recorded new attached resources
are checked by exact IDs after release. A final regional difference also detects
unobserved new resources after a partial failure. Unattributed differences fail
the residue check and are **never deleted** automatically. Other concurrent
regional changes or Terway retaining new pooled ENIs can therefore cause a
conservative failure; isolate the test environment and inspect the report. This
is preferable to claiming zero residue without ownership evidence. No claim is
made about storage workloads, calibrated ephemeral-storage scheduling, Spot
interruption handling or all future Kubernetes versions.
