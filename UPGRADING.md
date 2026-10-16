# Fresh installation with Karpenter v1.14.1

This development change pins `sigs.k8s.io/karpenter` and the AWS E2E harness to v1.14.1, the official stable release verified on 2026-10-10. The provider chart is 0.3.0-dev. It is a development candidate: no matching image or chart package has been published by this change, and ACK runtime E2E remains a release acceptance item.

The current delivery scope is a fresh provider installation on an existing supported ACK cluster. Old-provider migration, historical status/tag compatibility and downgrade/rollback rehearsals are outside this scope. This file keeps its existing path for documentation links.

Upstream references: [release](https://github.com/kubernetes-sigs/karpenter/releases/tag/v1.14.1), [Go dependencies](https://github.com/kubernetes-sigs/karpenter/blob/v1.14.1/go.mod), and [upgrade guide](https://karpenter.sh/docs/upgrading/upgrade-guide/).

## Kubernetes minimum

This candidate requires **Kubernetes 1.34 or newer**; **1.33 and earlier are unsupported**. Select a cluster meeting this minimum before installation. Helm rejects old clusters, and controller startup checks the actual API server version. ACK setup requires an explicit supported version and checks the actual ACK version.

最低版本为 **Kubernetes 1.34**，**1.33 及更早版本不受支持**。请选择符合最低版本要求的集群进行全新安装。最低支持要求不等于所有更新版本均已完成实测；真实 ACK/ECS 生命周期验收仍待完成。

See [KUBERNETES_SUPPORT.md](KUBERNETES_SUPPORT.md) for ACK suffix handling, regional version selection, validation and release evidence.

## Build and offline validation

Use Go 1.26.9 or a newer security-patched toolchain and Helm 3. The container builder and CI are pinned to Go 1.26.9 with local toolchain selection. Kubernetes client libraries use stable 0.35.x versions; controller-runtime is 0.23.1. Production controller builds and the default Makefile/CI checks use `CGO_ENABLED=0`; explicitly enable CGO when using tools that require it, such as `-race`.

Remaining legacy Alibaba Cloud clients have migrated to the SDK V2.0 family. Product module major versions are independent of that family version. See [SDK_MIGRATION.md](SDK_MIGRATION.md) for pinned SDK versions, dependency removal and Go batcher API changes. Build a new controller image from matching source; real ACK acceptance remains pending.

```sh
go mod download
make compatibility
make test
make e2e-parity
go test -p 1 -tags integration -run '^$' ./test/pkg/... ./test/suites/...
```

`make compatibility` checks exact core/harness versions, compares every upstream core CRD byte-for-byte in both vendored directories, renders Helm, and checks its permissions against the matching upstream core ClusterRole. It also verifies that core/provider feature gates render independently. `make sync-core-crds` copies CRDs from the pinned module. `make manifests` regenerates the provider CRD and synchronizes both locations. CI runs provider unit/controller tests with Linux envtest 1.35.0 plus minimum-version controller contracts on 1.34.0, vet, build, E2E compilation, Helm and dependency consistency. Envtest verifies API/controller behavior, not ACK or ECS runtime compatibility.

The KWOK regression harness uses `test/hack/kwok/values.yaml` to enable StaticCapacity, CapacityBuffer and NodeRepair. Core v1.14.1's KWOK chart references CapacityBuffer without a default value, so the harness supplies explicit booleans. Compatibility checks render that chart and parse its `FEATURE_GATES` with the pinned core parser before runtime setup.

KWOK defaults to a 10-pod provisioning smoke check: install the upstream KWOK controller, create a NodePool and deployment, and require all pods to become ready plus at least one NodeClaim. The rollout deadline is five minutes and the default Actions job budget is 30 minutes. This uses the upstream simulated provider; it does not validate ECS API behavior or replace ACK release acceptance. Manual dispatch can select `regression` or `performance` and override `replicas` (for example, 20); those suites retain a 240-minute job budget. Locally, use `make kwok-core-e2e KWOK_TEST_SUITE=regression KWOK_SCALE_REPLICAS=20` for full regression.

Full regression is opt-in because some upstream cases deliberately wait for long lifecycle deadlines, including a 17-minute registration timeout; reducing the final smoke pod count alone does not shorten these waits. Ginkgo prints progress and goroutine stacks every two minutes during long cases, stops on the first failure, and retains raw test logs plus JSON/JUnit reports. An independent `always()` workflow step captures cluster diagnostics before kind cleanup, including when cancellation bypasses the shell's exit trap. All diagnostic API requests are bounded to ten seconds.

## Configure a fresh installation

Core and provider gates have separate flags and environment variables:

| Scope | Helm value | CLI flag | Environment |
| --- | --- | --- | --- |
| Karpenter core | `settings.featureGates` | `--feature-gates` | `FEATURE_GATES` |
| Alibaba Cloud provider | `settings.providerFeatureGates` | `--provider-feature-gates` | `PROVIDER_FEATURE_GATES` |

Configure TerwayPodDensity, InterruptionHandling, LaunchTemplateID, MetadataOptions, CapacityReservation, DeploymentSet, InstanceStoreRAID0, CreateFallback, and PricingRefresh under `settings.providerFeatureGates`. Core gates include NodeRepair, ReservedCapacity, SpotToSpotConsolidation, NodeOverlay, StaticCapacity, and CapacityBuffer. Empty core settings use upstream defaults. CapacityBuffer, NodeOverlay and StaticCapacity remain opt-in according to upstream defaults. This provider does not advertise DRA device metadata; keep upstream `IGNORE_DRA_REQUESTS=true`.

```yaml
settings:
  clusterID: your-existing-ack-cluster-id
  featureGates: "NodeOverlay=true,NodeRepair=true"
  providerFeatureGates: "TerwayPodDensity=true,CreateFallback=true"
```

The controller's cluster ID is the instance ownership boundary. An ECSNodeClass with a specified `spec.clusterID` must match it. New instances are always tagged with the controller cluster ID. Managed ECS instances are identified by both `karpenter.alibabacloud.com/managed-by=karpenter` and `karpenter.alibabacloud.com/cluster-id=<controller-cluster-id>`. Keep this ownership boundary when discovering or deleting resources during normal operation.

## Install a matching image, chart and CRDs

Build and publish a controller image from the selected commit. Install the matching chart and complete CRD set, including `autoscaling.x-k8s.io_capacitybuffers.yaml`, on a cluster without a previous installation of this provider. Configure credentials and region in `your-values.yaml`; local CLI credentials are not automatically available to the controller Pod.

```sh
kubectl apply --server-side --field-manager=karpenter-install -f charts/karpenter/crds/
helm install karpenter ./charts/karpenter \
  --namespace karpenter --create-namespace \
  -f your-values.yaml \
  --set-string settings.clusterID=YOUR_ACK_CLUSTER_ID \
  --set controller.image.repository=YOUR_REGISTRY/YOUR_PROVIDER \
  --set controller.image.tag=YOUR_COMMIT_TAG
kubectl rollout status deployment/karpenter -n karpenter
```

Create valid ECSNodeClass and NodePool resources after installation. Admission rejects empty terms/tags, conflicting IDs, invalid aliases and unsupported owner combinations. Use `imageOwnerAlias: others` with `imageOwnerID` for shared images. A NodeClass must resolve its current generation before it can authorize scheduling or creation. See [ECSNodeClass readiness and image selection](ECSNODECLASS_VALIDATION.md).

ACK bootstrap requires current cluster detail and exact-version containerd metadata. Grant the CS read permissions below; API failure blocks script generation instead of supplying an unverified fallback. The role includes CapacityBuffer status, PodTemplates, events and core workload reads. Enabled optional core features require runtime acceptance in the selected ACK version.

## Release acceptance

Offline implementation and ACK runtime acceptance are separate. ACK E2E is deferred for this task and tracked in [release acceptance issue #5](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/5). Keep the milestone open until a dedicated ACK test environment records:

- The actual ACK/Kubernetes version (>=1.34), region and matching controller digest/chart/git SHA; retain the helper manifest and TEST_KUBERNETES_VERSION evidence.
- Provisioning and registration for the required amd64/arm64 and spot/on-demand inventory.
- Consolidation, drift, interruption, NodeRepair and enabled optional core features.
- Fresh installation with matching CRDs/RBAC/gates and ownership isolation during the new node lifecycle.
- Relevant upstream contract suites, provider suites and retained test artifacts.

The existing E2E workflows require explicit dispatch and cloud credentials. Do not dispatch them as part of ordinary PR CI. `E2EUpgrade` currently runs the Drift suite; its workflow name does not imply migration coverage. Old-provider migration and rollback are outside current acceptance.

The minimal ACK/ECS helper retains SLB API-server listener/ACL configuration and tagged SLB cleanup. ALB/NLB SDKs and their dedicated resource sweep are removed in [#39](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/39); the helper also omits the explicit ALB cluster-deletion option. ALB/NLB workloads and residual-resource cleanup need separate acceptance coverage before those scenarios are added.

## ACK image compatibility gate

The matching CRD includes optional `status.images[].creationTime`. New launches prefer the newest creation timestamp among known compatible architectures; missing/unknown architecture cannot authorize provisioning. Positive selector-cache TTL is five minutes, with no empty-result caching and a 256-entry cap. Existing image-membership drift behavior and exact single-ID pins are preserved; see [ECSNODECLASS_VALIDATION.md](ECSNODECLASS_VALIDATION.md).

Use the matching CRD/controller and grant `cs:DescribeKubernetesVersionMetadata` and `cs:ListClusterAddonInstances`. Public images must resolve from the target ACK cluster version/region/profile/containerd image list. Pins outside that list fail explicitly; the controller never substitutes another pin. A newer ECS family release can differ from the ACK-approved image. Creation revalidates concrete status IDs against the current launch requirements.

The provider supports Linux only. Modern ContainerOS requires one new persistent data disk and known hardware without local disks. Native templates resolve their numeric version and validated disk snapshot; RunInstances and bootstrap use that same configuration. No extra data disk is added, and NodeClass disk overrides are not accepted in template mode.

Custom/shared images require `imageCompatibilityPolicy: Custom`, exact IDs and `imagePrerequisitesVerified: true` after the user verifies provenance, OS/runtime/bootstrap and required drivers/plugins. GPU and Ubuntu also require explicit verification. It cannot bypass known incompatibilities or the public-image ACK gate. GPU pairing is restricted to known NVIDIA hardware. Alibaba Cloud Linux 4 requires known active Flannel or Terway [1.9.18,1.10.0)/>=1.16.4; installed addon state is read again before launch. Ubuntu rejects the image-acceleration node label and has user-verified CPFS/security/initialization restrictions.

Offering prices now use ECS hourly transaction quotes with actual image, disks, zone and capacity type; existing `ecs:DescribePrice` permission is required. Missing, pending, expired, mixed-currency or failed quotes are unavailable, not free launch candidates. Quotes refresh with bounded concurrency/rate and failure caching. Template billable context is resolved from its pinned version; unmodeled provisioned disk IOPS/bursting is diagnosed and excluded. Marketplace images require complete same-currency licensing quotes. Baseline hourly prices do not predict future usage fees or retained-resource bills. See [PROVIDER_BEHAVIOR.md](PROVIDER_BEHAVIOR.md).

These policies and resolved template versions/disks participate in the shared v4 drift hash. Fresh installations use matching generated CRDs and controller source; historical hash migration is outside this task.

Read [ACK_IMAGE_SUPPORT.md](ACK_IMAGE_SUPPORT.md) for restrictions and examples. Read-only CS/ECS API evidence and offline tests do not replace #5/#23's real ACK provisioning/registration/termination acceptance, which remains deferred.


### Native ACK resource configuration (dev)

Fresh deployments require access to `cs:DescribeClusterNodePools` and `cs:DescribeClusterNodePoolDetail`. Select an existing regular ACK node pool with `spec.ackNodePoolID`, or retain its unique default. Kubelet overrides belong in that native pool: `spec.kubelet` is rejected because ACK attach scripts do not apply it. Provider status snapshots resource settings and launch explicitly pins the pool.

Termination also requires cluster-scoped `cs:DescribeClusterNodes` and `cs:RemoveNodePoolNodes`, plus `cs:DescribeTaskInfo` (`Resource: "*"` only). ACK adoption can raise desired capacity even with autoscaling disabled; deleting ECS alone caused native replenishment (#101). The provider now records removal intent, waits for ACK task success and absent membership, then releases the exact owned ECS. Ambiguous submission outcomes stop automatic retries; see [receipt recovery](test/hack/e2e/smoke/README.md). Live scale-in revalidation remains pending and automatic ACK testing stays disabled.

Ephemeral storage is advertised only with an image/configuration-matched `spec.nodeFilesystem` measurement; independent disk sizes are never summed. Read `status.resourceConfigurationHash` and the calibration node's kubelet Capacity/Allocatable. Registered nodes use their actual resource reports after identity verification. See [resource calculation and limits](PROVIDER_BEHAVIOR.md#ack-节点资源计算). Real ACK calibration remains a release gate in #5/#23.
