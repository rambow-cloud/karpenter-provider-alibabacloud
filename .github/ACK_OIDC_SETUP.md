# ACK E2E OIDC configuration status

**Current status (2026-10-16): the dedicated ACK and its chargeable resources
have been deleted.** Its worker/controller/runner roles, dedicated policies,
RRSA provider, local credentials and GitHub cluster configuration were removed.
`ACK_E2E_ENABLED=false`; shared platform identities and ACR artifacts remain.
The sections below describe historical preparation and experiments.

#101 found delayed native replacements: ACK attach raised desired capacity from
1 to 5 despite autoscaling being disabled. Full teardown released all five
native workers. Earlier tagged-instance cleanup did not establish stable total
capacity reduction. The corrected termination path removes ACK membership and
waits for its task before deleting ECS. E2E now checks native desired/member
counts and all VPC instances for five minutes after tagged capacity disappears.
This change still needs an explicitly authorized new live acceptance; no cloud
environment is currently provisioned and no live rerun is implied by this fix.

Initial identity configuration was prepared on 2026-10-12. On 2026-10-16 the
maintainer authorized creation of a dedicated ACK Basic environment using the
local `oss-sftp` Alibaba Cloud CLI OAuth profile. No profile credentials,
kubeconfig, ServiceAccount tokens or STS credentials were copied into the
repository or GitHub secrets.

The dedicated `karpenter-ack-e2e` cluster is running in `cn-hangzhou-k` on ACK
`1.35.7-aliyun.1`, containerd `2.3.4`, Flannel and Alibaba Cloud Linux 3 Container
Optimized. It has one Ready on-demand system worker (4 vCPU / 8 GiB, 40 GiB ESSD
PL0) in the active regular `system` node pool. The separate zero-capacity
`karpenter-template` pool was removed at the maintainer's request after verifying
it held no nodes. Smoke now reads native ACK metadata from the existing system
pool; this reuse exposed #101's native capacity ownership conflict. Disabling
autoscaling and managed upgrades did not prevent native replacements. CoreDNS used one replica for
this single-system-node test baseline; this is not a production HA topology.

## Configured runner identity

- Reused the account's existing `github-actions` RAM OIDC provider. Its issuer is
  `https://token.actions.githubusercontent.com`; its audience is `sts.aliyuncs.com`.
  Existing provider settings and other roles were not changed.
- Created `karpenter-ack-e2e-runner`, max STS session 3600 seconds. Its trust is
  limited to that provider, issuer, audience and the exact subject below. The
  dedicated `KarpenterACKE2ERunner20261016` custom policy permits that cluster's
  temporary kubeconfig (maximum 1440 minutes), cluster/pool reads and the ECS
  inventory reads needed by smoke. Fallback instance deletion requires the
  dedicated cluster tag, `karpenter.sh/managed-by=karpenter`, `testing/type=e2e`
  and a `testing/karpenter-run=ack-*` tag together. The role cannot create/delete
  clusters, networks or IAM resources, or create ECS instances.
- Granted this runner Kubernetes `admin` RBAC on the dedicated test cluster
  only, for fresh Helm/CRD installation. This is separate from its RAM policy.
- Created GitHub Environment `ack-e2e`, with branch deployment rules permitting
  only `dev` and `main`. No approval requirement was added. Its environment
  variables are `ALIBABA_CLOUD_OIDC_PROVIDER_ARN`, `ALIBABA_CLOUD_ROLE_ARN` and
  `ALIBABA_CLOUD_REGION_ID` (initial region `cn-hangzhou`). These are identifiers,
  not credentials; future #88 workflow code must read them through `vars`.
- Set repository `ACK_E2E_ENABLED=false`. This identity configuration does not
  dispatch a workflow or start E2E. No STS exchange from a GitHub runner has yet
  been tested.

The repository currently uses an immutable OIDC subject. The environment job
must match:

```text
repo:rambow-cloud@333554680/karpenter-provider-alibabacloud@1412605061:environment:ack-e2e
```

Recheck the repository OIDC settings when implementing #88. An environment
subject has no branch segment; environment deployment rules enforce branches.
Use `environment: ack-e2e`, job `permissions: {contents: read, id-token: write}`,
manual-only event/guard, and an audited full-SHA-pinned official Alibaba Cloud
credential action with audience `sts.aliyuncs.com`.

The runner's temporary AccessKeyId, AccessKeySecret and SecurityToken must all
reach its SDK clients. They must never be copied into the controller. Before
cleanup, obtain a fresh OIDC/STS session and rebuild the clients. Secret/token
values, kubeconfig and userdata must not appear in logs or uploaded reports.

## Dedicated ACK configuration and remaining acceptance

The ignored, access-restricted `.e2e/ack-private/config.yaml` contains the actual
cluster, subnet, security group, worker role and native pool identifiers. Its
credential-free contents were set as the `ALIBABA_CLOUD_E2E_CONFIG` secret of
GitHub Environment `ack-e2e`. Both configurations now reference the remaining
system pool. The temporary local kubeconfig and SSH private key
remain outside Git tracking in that restricted directory. Never upload it.

The system node matches `e2e.karpenter.alibabacloud.com/system=true`. Worker
candidates are `ecs.c8i.large`, `ecs.c7.large` and `ecs.u1-c1m2.large`, all amd64
2-vCPU candidates with on-demand/Spot stock at preparation time. Stock is checked
again by the manually started smoke, rather than treated as a reservation.

RRSA is enabled. `karpenter-controller-e2e` trusts only the actual ACK OIDC
provider, its external issuer, `sts.aliyuncs.com`, and the exact subject
`system:serviceaccount:karpenter:karpenter`. Its custom policy limits writes to
the dedicated VPC, the three candidate instance types, PostPaid instances and
the test ownership tags above; PassRole/GetRole target the actual pool worker
role only. RAM policy resource identifiers use the documented global form
`acs:ram:*:<account>:role/<RoleName>`; the role's returned principal ARN has an
empty region and cannot be reused verbatim for GetRole authorization. APIs
without resource-level read scoping retain explicit read actions
on `*`. Default ACK first-use service roles are platform prerequisites; the
maintainer separately approved the six missing official roles and policies.

Actual short-lived TokenRequests verified both successful controller-role STS
exchange and rejection of `default/default`. No temporary credentials were
persisted, and the temporary controller ServiceAccount was deleted afterward.
The local manual smoke also installed the controller on the fixed system node
and verified its admitted RRSA Pod, projected audience and credential isolation.

ACK reports `https://kubernetes.default.svc` as its cluster default audience;
the RAM OIDC provider accepts `sts.aliyuncs.com`. These are different settings.
The Chart's explicitly projected token and admitted-Pod verification must still
use `sts.aliyuncs.com`. #95 fixes this preflight distinction and aligns Python
ECS ownership checks with the provider's `karpenter.sh/*` cloud tag keys.

The existing public ACR controller image (source commit
`7121262490ebfdaa979987e72e70324759508632`, index digest
`sha256:50beeadb8f4848b8653de1c7924b2e1741c3fa73ceae106cf989f4ef0b51a037`)
and mirrored official pause 3.10.1 amd64 image were successfully pulled without
an imagePullSecret on the system worker. Controller executed only `--help`;
both temporary pull-check Pods were deleted. The config pins both digests.

`ACK_E2E_ENABLED=false` remains set. A separately authorized local manual smoke
uses the maintainer's short-lived OAuth STS session as runner identity and ACK
RRSA for the controller. It does not prove GitHub runner OIDC exchange. #23 and
#88 remain open until their complete runtime acceptance is recorded.

The first live attempts exposed a missing required NodePool
`spec.disruption.consolidateAfter`; #97 sets it to `Never` and both complete
NodePool fixtures passed the actual cluster's server dry-run. The current
controller default policy is `v3`. Its VPC/tag creation conditions alone did not
authorize ECS's separate resource-group checks, so it is not yet a complete
reusable creation policy. The maintainer approved a temporary Hangzhou default
resource-group `RunInstances` attachment for this one local smoke, with explicit
restriction checks, independent cleanup and restoration to `v3`. Positive
on-demand/Spot DryRuns and negative wrong-type, foreign-cluster and missing-tag
DryRuns passed before actual creation. Do not enable recurring creation or copy
this temporary grant as a production policy; #98 tracks permanent resource-group
support, scoped authorization and runtime validation.

The local run `ack-c99613b5d40b47ad8678e092` passed on 2026-10-16:

- ACK `1.35.7-aliyun.1`, kubectl `v1.35.0`, pinned controller/image above.
- Three new `ecs.u1-c1m2.large` workers: two PostPaid/`NoSpot`, one
  PostPaid/`SpotAsPriceGo`. All NodeClaims reached Launched, Registered,
  Initialized and Ready, and all Nodes were Ready.
- Three lightweight Pods were Running/Ready with Pod IPs, one per new worker.
  Provisioning took **119.6 seconds**, including setup and the live checks.
- Normal controller termination passed: zero test ECS, disks, ENIs, NodeClaims
  and Nodes remained, with **no fallback release**. Owned workloads, classes,
  pools and the controller release were removed; the fixed system node remains.
- The temporary resource-group policy and candidate policy version were deleted,
  and the controller default policy was verified restored to `v3`.

Sanitized local reports are retained in the ignored
`.e2e/ack-smoke-local-20261016-014059/` directory. This is real local lifecycle
and Spot-creation evidence for #23; GitHub runner OIDC and a permanent scoped
controller creation policy remain outstanding for repeatable workflow acceptance.

The fixed system worker and disk were quoted at approximately CNY 0.744/hour at
preparation time; NAT, API endpoint/load balancer, EIPs and traffic add charges.
The baseline remains running until explicitly released. Smoke cleanup does not
delete this baseline, its VPC/subnet, roles or OIDC providers.

### Checklist for recreating the dedicated environment

Once a test ACK exists, record its region, ID, actual Kubernetes version >=1.34,
API access path, baseline system worker, vSwitch/security group, worker role and
compatible images. If it is outside `cn-hangzhou`, update the region variable.

Enable ACK RRSA and obtain its actual issuer/provider ARN. Create a separate
controller role trusting only `system:serviceaccount:karpenter:karpenter`, that
ACK issuer and audience `sts.aliyuncs.com`. The controller uses the Chart's
explicit projected token and runs on the baseline system node, without AK/SK
credentials or a credentials Secret. Avoid duplicate webhook token injection.

Then configure controller RAM permissions, runner cluster-scoped RAM/RBAC and
strict run-owned cleanup permissions. The runner must not have cluster/network
or RAM role creation/deletion permissions. Shared OIDC/RAM setup is not part of
smoke cleanup. Check every action's supported resource/tag conditions; describe
operations with no resource-level scoping may require `Resource: "*"`.

`ackctl` now accepts `alibaba_cloud.authentication: sts` and atomically reads
the three temporary credential fields from the environment. All CS/ECS/VPC/SLB
clients use the same STS configuration; missing fields and mixed identities fail
without fallback. STS values are omitted when saving configuration. Refresh the
GitHub OIDC composite before cleanup and start a fresh ackctl process so that
clients use the new session.

The manual-only `.github/actions/e2e/oidc` composite validates role/provider
identity before the official SHA-pinned action exchanges OIDC for STS. #23's
workflow uses it for both setup and cleanup; this composite is not itself
a workflow and cannot start a job.

Use `.github/ack-e2e-config.example.yaml` to describe the cluster and separate
controller identity. `ackctl rrsa-values --config CONFIG --endpoint HTTPS_URL
--output VALUES` renders only controller RRSA values; `ackctl check-rrsa-pod
--config CONFIG --input POD_JSON` verifies the admitted controller identity,
environment and projected token. Existing setup also selects the RRSA-only path
when `controller_rrsa` is configured and refuses to deploy runner STS credentials
into the controller. It uses `--reset-values` to eliminate stale credentials.
The three-worker smoke must use its own fresh-deployment/strict-cleanup path,
not legacy cluster-wide setup/cleanup. Local controller RRSA evidence does not
replace the still-pending GitHub runner OIDC acceptance.

## Manual-only operation

All E2E workflows, including KWOK and Parity, require manual dispatch under #89;
reusable callers cannot bypass it. ACK resource creation also needs explicit
opt-in. Manual cleanup remains available after clearing the creation opt-in.
Keep legacy KWOK/Parity registrations disabled until main contains #89's source.
Publishing to dev alone does not update the default-branch entry points.

Inspect the configuration in [GitHub environments](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/settings/environments)
and the RAM console's role/OIDC provider pages. #88 and #23 remain open pending
complete runtime acceptance. The local manual smoke did not dispatch GitHub E2E,
KWOK or a vulnerability scan.

Sources: [ACK RRSA](https://www.alibabacloud.com/help/en/ack/ack-managed-and-ack-dedicated/user-guide/use-rrsa-to-authorize-pods-to-access-different-cloud-services),
[GitHub OIDC](https://docs.github.com/en/actions/reference/security/oidc),
[official credential action](https://github.com/aliyun/configure-aliyun-credentials-action).
