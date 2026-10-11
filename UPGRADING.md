# Karpenter v1.14.1 upgrade

This development change pins `sigs.k8s.io/karpenter` and the AWS E2E harness to v1.14.1, the official stable release verified on 2026-10-10. The provider chart is 0.3.0-dev. It is a development candidate: no matching image or chart package has been published by this change, and ACK runtime E2E remains a release acceptance item.

Upstream references: [release](https://github.com/kubernetes-sigs/karpenter/releases/tag/v1.14.1), [Go dependencies](https://github.com/kubernetes-sigs/karpenter/blob/v1.14.1/go.mod), and [upgrade guide](https://karpenter.sh/docs/upgrading/upgrade-guide/).

## Kubernetes minimum

This candidate requires **Kubernetes 1.34 or newer**; **1.33 and earlier are unsupported**. Upgrade Kubernetes before applying this candidate's CRDs or deploying its controller. Helm rejects old clusters, and controller startup checks the actual API server version. ACK setup requires an explicit supported version and checks the actual ACK version.

最低版本为 **Kubernetes 1.34**，**1.33 及更早版本不受支持**。请先升级 Kubernetes，再应用本候选版本的 CRD 或部署控制器。最低支持要求不等于所有更新版本均已完成实测；真实 ACK/ECS 和升级、回滚验收仍待完成。

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

KWOK runs the complete upstream regression suite and a 1000-pod scale check. Some upstream cases deliberately wait for long lifecycle deadlines, including a 17-minute registration timeout. Ginkgo prints progress and goroutine stacks every two minutes during long cases, stops on the first failure, and retains raw test logs plus JSON/JUnit reports. An independent `always()` workflow step captures cluster diagnostics before kind cleanup, including when cancellation bypasses the shell's exit trap. All diagnostic API requests are bounded to ten seconds.

## Migrate configuration

Core and provider gates have separate flags and environment variables:

| Scope | Helm value | CLI flag | Environment |
| --- | --- | --- | --- |
| Karpenter core | `settings.featureGates` | `--feature-gates` | `FEATURE_GATES` |
| Alibaba Cloud provider | `settings.providerFeatureGates` | `--provider-feature-gates` | `PROVIDER_FEATURE_GATES` |

Move TerwayPodDensity, InterruptionHandling, LaunchTemplateID, MetadataOptions, CapacityReservation, DeploymentSet, InstanceStoreRAID0, CreateFallback, and PricingRefresh entries into `settings.providerFeatureGates`. Core gates include NodeRepair, ReservedCapacity, SpotToSpotConsolidation, NodeOverlay, StaticCapacity, and CapacityBuffer. Remove obsolete Drift gate entries. Empty core settings use upstream defaults. CapacityBuffer, NodeOverlay and StaticCapacity remain opt-in according to upstream defaults. This provider does not advertise DRA device metadata; keep upstream `IGNORE_DRA_REQUESTS=true`.

```yaml
settings:
  clusterID: your-existing-ack-cluster-id
  featureGates: "NodeOverlay=true,NodeRepair=true"
  providerFeatureGates: "TerwayPodDensity=true,CreateFallback=true"
```

The controller's cluster ID is the instance ownership boundary. An ECSNodeClass with a specified `spec.clusterID` must match it. New instances are always tagged with the controller cluster ID. Existing managed ECS instances must carry both `karpenter.alibabacloud.com/managed-by=karpenter` and `karpenter.alibabacloud.com/cluster-id=<controller-cluster-id>`. Review ownership in the Alibaba Cloud console before rollout; do not retag another cluster's resources.

## Deploy a matching image and CRDs

Build and publish your own candidate image through your normal registry workflow. Do not reuse the old 0.2.5 controller with the new chart. Export the existing Helm values and relevant CRD/NodePool/ECSNodeClass manifests to a secure local backup, and migrate the values above. Review the upstream upgrade notes against the actual Kubernetes/ACK version before selecting a test rollout.

Helm does not automatically upgrade CRDs in its `crds/` directory during an upgrade. Apply the complete set explicitly, including `autoscaling.x-k8s.io_capacitybuffers.yaml`, before updating the controller:

```sh
kubectl apply --server-side --field-manager=karpenter-upgrade -f charts/karpenter/crds/
helm upgrade --install karpenter ./charts/karpenter \
  --namespace karpenter --create-namespace --reset-values \
  -f migrated-values.yaml \
  --set controller.image.repository=YOUR_REGISTRY/YOUR_PROVIDER \
  --set controller.image.tag=YOUR_COMMIT_TAG
kubectl rollout status deployment/karpenter -n karpenter
```

Resolve CRD field ownership conflicts deliberately if server-side apply reports them. The updated role covers CapacityBuffer status, PodTemplates, events and core workload reads. Optional core APIs still require runtime acceptance in the selected ACK version.

## Rollback

Before applying the current provider CRD, check existing image selectors against [ECSNodeClass readiness and image selection](ECSNODECLASS_VALIDATION.md). Admission now rejects empty terms/tags, conflicting IDs, invalid aliases and unsupported owner combinations. Specify `imageOwnerAlias: others` with `imageOwnerID` only for shared images; remove owner filters from `self`, `system` or `marketplace` queries. Existing NodeClasses must reconcile their current generation before new provisioning resumes. Running nodes are unaffected by this readiness gate.

升级前请检查镜像 selector：`imageOwnerID` 必须显式配合 `imageOwnerAlias: others`，空 term、空标签值和 ID 混用条件需修正。先应用对应 CRD，再部署控制器；旧 NodeClass 需解析当前 generation 后才能继续扩容。详细规则见上面的文档。

Retain the previous controller image, chart, Helm values, and exported resource/CRD schemas. Before restoring the previous controller, disable new optional features and reconcile resources that use fields the old version cannot interpret. Rolling back the controller alone does not roll back CRD schemas. Revert the provider feature-gate configuration to the old flag/value layout when restoring 0.2.5.

Do not delete CRDs to perform a rollback: deleting a CRD deletes its custom resources. Restore schemas only after checking stored objects for newly introduced fields and validating the downgrade against the upstream guide. Avoid enabling new APIs in the first rollout until the rollback path is confirmed.

## Release acceptance

Offline implementation and ACK runtime acceptance are separate. ACK E2E is deferred for this task and tracked in [release acceptance issue #5](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/5). Keep the milestone open until a dedicated ACK test environment records:

- The actual ACK/Kubernetes version (>=1.34), region and matching controller digest/chart/git SHA; retain the helper manifest and TEST_KUBERNETES_VERSION evidence.
- Provisioning and registration for the required amd64/arm64 and spot/on-demand inventory.
- Consolidation, drift, interruption, NodeRepair and enabled optional core features.
- Upgrade behavior with existing NodeClaims, matching ownership tags, and validated rollback.
- Relevant upstream contract suites, provider suites and retained test artifacts.

The existing E2E workflows require explicit dispatch and cloud credentials. Do not dispatch them as part of ordinary PR CI. `E2EUpgrade` currently runs the Drift suite; it is not evidence of a complete old-controller-to-new-controller upgrade rehearsal.

The minimal ACK/ECS helper retains SLB API-server listener/ACL configuration and tagged SLB cleanup. ALB/NLB SDKs and their dedicated resource sweep are removed in [#39](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/39); the helper also omits the explicit ALB cluster-deletion option. ALB/NLB workloads and residual-resource cleanup need separate acceptance coverage before those scenarios are added.

## ACK image compatibility gate

Update both the CRD and controller and grant `cs:DescribeKubernetesVersionMetadata`. Public images must resolve from the target ACK cluster version/region/profile/containerd image list. Existing pins outside that list fail explicitly; the controller never substitutes another pin. A newer ECS family release can differ from the ACK-approved image. Creation revalidates concrete status IDs, including legacy status missing OS fields.

The provider supports Linux only. Modern ContainerOS requires one explicitly configured persistent data disk; ContainerOS with a launch template is rejected because the current launch path does not propagate its data disks. Existing custom/shared images require `imageCompatibilityPolicy: Custom` with exact IDs; the user must verify their actual OS/runtime/bootstrap compatibility. `Custom` cannot bypass the ACK gate for public images. The new policy participates in drift hashing; implicit/default ACK policies have the same hash.

Read [ACK_IMAGE_SUPPORT.md](ACK_IMAGE_SUPPORT.md) for restrictions and examples. Rollback removes these guards and may reintroduce unsupported image selection. Read-only CS/ECS API evidence and offline tests do not replace #5/#23's real ACK provisioning/registration/termination acceptance, which remains deferred.
