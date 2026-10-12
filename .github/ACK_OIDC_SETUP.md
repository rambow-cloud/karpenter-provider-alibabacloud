# ACK E2E OIDC configuration status

Configuration was prepared on 2026-10-12 using the maintainer-authorized local
`oss-sftp` Alibaba Cloud CLI OAuth profile. No credentials were copied from that
profile into the repository or GitHub secrets. The maintainer confirmed that
there is currently no ACK cluster; none was created during configuration.

## Configured runner identity

- Reused the account's existing `github-actions` RAM OIDC provider. Its issuer is
  `https://token.actions.githubusercontent.com`; its audience is `sts.aliyuncs.com`.
  Existing provider settings and other roles were not changed.
- Created `karpenter-ack-e2e-runner`, max STS session 3600 seconds. Its trust is
  limited to that provider, issuer, audience and the exact subject below. No
  operation policies are attached until a dedicated cluster is selected; the
  role currently cannot create or delete ACK/ECS resources.
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

## Pending dedicated ACK configuration

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

Current `ackctl` clients omit SecurityToken and existing setup still injects an
AK Secret. #88 must complete that code before the runner identity can execute
#23. Configuration alone does not satisfy #88's authentication acceptance.

## Manual-only operation

All E2E workflows, including KWOK and Parity, require manual dispatch under #89;
reusable callers cannot bypass it. ACK resource creation also needs explicit
opt-in. Manual cleanup remains available after clearing the creation opt-in.
Keep legacy KWOK/Parity registrations disabled until main contains #89's source.
Publishing to dev alone does not update the default-branch entry points.

Inspect the configuration in [GitHub environments](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/settings/environments)
and the RAM console's role/OIDC provider pages. #88 and #23 remain open pending
implementation and selected-environment runtime evidence; configuration did not
run Go builds/tests, vulnerability scans, KWOK or ACK E2E.

Sources: [ACK RRSA](https://www.alibabacloud.com/help/en/ack/ack-managed-and-ack-dedicated/user-guide/use-rrsa-to-authorize-pods-to-access-different-cloud-services),
[GitHub OIDC](https://docs.github.com/en/actions/reference/security/oidc),
[official credential action](https://github.com/aliyun/configure-aliyun-credentials-action).
