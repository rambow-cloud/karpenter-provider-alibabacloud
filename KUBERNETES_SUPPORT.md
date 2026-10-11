# Kubernetes version policy

This provider's `0.3.0-dev` candidate with Karpenter core `v1.14.1` requires **Kubernetes 1.34 or newer**. Kubernetes **1.33 and earlier are unsupported**. Upgrade the cluster before applying this candidate's CRDs or installing/upgrading the controller. Passing the minimum check does not establish runtime compatibility for every later version.

最低受支持 Kubernetes 版本为 **1.34**，**1.33 及更早版本不受支持**。请先升级集群，再应用本候选版本的 CRD 或安装、升级控制器。最低版本要求、离线检查与真实 ACK 验收分别记录；更高版本通过版本检查不代表已验证兼容。

## Enforcement

- Helm declares `kubeVersion: ">=1.34.0-0"`, admitting distribution suffixes such as `1.34.0-aliyun.1`.
- Controller startup checks the API server's actual `GitVersion` before initializing cloud clients/providers. Old, malformed, missing or undiscoverable versions fail clearly.
- ACK `render-config` validates the effective version after manual overrides. `setup`, node-pool creation and capacity-reservation commands reject old or missing configured targets before cloud creation. Specify `cluster.kubernetes_version` or the E2E workflow's `k8s_version`; an unspecified cloud default is not a supported test target.
- Setup also checks the running ACK cluster's actual `CurrentVersion`. Successful setup records the observed version in `manifest.yaml` (`kubernetesVersion`) and `ack.env` (`TEST_KUBERNETES_VERSION`); the E2E workflow uploads an `ack-version-*` artifact containing only the observed version. Dry-run does not claim an observed version.
- Config loading and cleanup remain available for old clusters, so the new installation minimum does not prevent test resource removal.

Semantic parsing and numeric major/minor comparisons handle minor inputs (`1.34`), full versions, `v` prefixes and valid distribution/build suffixes. String ordering is not used.

## ACK version selection

The compatibility trigger requires `k8s_versions`: a JSON array of versions the operator has confirmed available in the selected region for the configured ACK cluster type. There is no fixed/default cloud version matrix. A credential-free preflight rejects empty, malformed, duplicate or sub-1.34 lists before cloud jobs start. Other E2E entry points validate the final config before setup.

Example syntax: `["1.35", "1.36"]`. Confirm regional availability before using these values; this is not a record of tests run. The [ACK version policy](https://www.alibabacloud.com/help/en/ack/ack-managed-and-ack-dedicated/user-guide/support-for-kubernetes-versions/) documents release history and creation restrictions. At this change it lists 1.35 and 1.36 as released and gives 1.34 an end-of-support date of 2026-09-30. The provider minimum remains 1.34; actual cloud targets must also meet ACK availability and maintenance policy.

`1.34.10-aliyun.1` is a format/example from the [ACK 1.34 release notes](https://help.aliyun.com/zh/ack/ack-managed-and-ack-dedicated/user-guide/kubernetes-1-34-release-notes), not a guarantee that this patch can still be created in a particular region.

## Offline checks and release boundary

| Check | Version boundary |
| --- | --- |
| `make compatibility` / `go run ./hack/compatibility` | Helm rejects 1.33, renders 1.34/1.35/1.36 and ACK suffixes; checks pinned CRDs/RBAC/gates |
| Version/operator/ACK helper unit tests | Numeric comparisons, suffixes, invalid/missing versions, effective overrides, early rejection, actual server/ACK checks and version evidence |
| Linux CI envtest | Full suite on 1.35.0; additional controller contracts on minimum 1.34.0 |
| KWOK/kind configuration | kind v0.30.0 and digest-pinned Kubernetes 1.34.0 from the [kind release](https://github.com/kubernetes-sigs/kind/releases/tag/v0.30.0); checks the actual server version, including reused clusters |

Helm and envtest do not establish ACK/ECS runtime compatibility. Pinning KWOK does not by itself establish that the complete regression suite passed; actual results belong in CI/run evidence.

Real ACK E2E remains deferred under [release acceptance #5](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/5). Before release, record the actual ACK/Kubernetes version (>=1.34), region, controller digest/chart/git SHA, enabled gates, lifecycle and upgrade/rollback results, limitations and cleanup evidence. [Issue #7](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/7) performs no cloud test or cluster upgrade.
