# Karpenter v1.14.1 upgrade

This development change pins `sigs.k8s.io/karpenter` and the AWS E2E harness to v1.14.1, the official stable release verified on 2026-10-10. The provider chart is 0.3.0-dev. It is a development candidate: no matching image or chart package has been published by this change, and ACK runtime E2E remains a release acceptance item.

Upstream references: [release](https://github.com/kubernetes-sigs/karpenter/releases/tag/v1.14.1), [Go dependencies](https://github.com/kubernetes-sigs/karpenter/blob/v1.14.1/go.mod), and [upgrade guide](https://karpenter.sh/docs/upgrading/upgrade-guide/).

## Build and offline validation

Use Go 1.26.6 or newer and Helm 3. The container builder is pinned to Go 1.26.6 with local toolchain selection. Kubernetes client libraries use stable 0.35.x versions; controller-runtime is 0.23.1.

```sh
go mod download
make compatibility
make test
make e2e-parity
go test -p 1 -tags integration -run '^$' ./test/pkg/... ./test/suites/...
```

`make compatibility` checks exact core/harness versions, compares every upstream core CRD byte-for-byte in both vendored directories, renders Helm, and checks its permissions against the matching upstream core ClusterRole. It also verifies that core/provider feature gates render independently. `make sync-core-crds` copies CRDs from the pinned module. `make manifests` regenerates the provider CRD and synchronizes both locations. CI runs provider unit/controller tests with Linux envtest 1.35.0, vet, build, E2E compilation, Helm and dependency consistency. Envtest verifies API/controller behavior, not ACK or ECS runtime compatibility.

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

Retain the previous controller image, chart, Helm values, and exported resource/CRD schemas. Before restoring the previous controller, disable new optional features and reconcile resources that use fields the old version cannot interpret. Rolling back the controller alone does not roll back CRD schemas. Revert the provider feature-gate configuration to the old flag/value layout when restoring 0.2.5.

Do not delete CRDs to perform a rollback: deleting a CRD deletes its custom resources. Restore schemas only after checking stored objects for newly introduced fields and validating the downgrade against the upstream guide. Avoid enabling new APIs in the first rollout until the rollback path is confirmed.

## Release acceptance

Offline implementation and ACK runtime acceptance are separate. ACK E2E is deferred for this task and tracked in [release acceptance issue #5](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/5). Keep the milestone open until a dedicated ACK test environment records:

- Provisioning and registration for the required amd64/arm64 and spot/on-demand inventory.
- Consolidation, drift, interruption, NodeRepair and enabled optional core features.
- Upgrade behavior with existing NodeClaims, matching ownership tags, and validated rollback.
- Relevant upstream contract suites, provider suites and retained test artifacts.

The existing E2E workflows require explicit dispatch and cloud credentials. Do not dispatch them as part of ordinary PR CI. `E2EUpgrade` currently runs the Drift suite; it is not evidence of a complete old-controller-to-new-controller upgrade rehearsal.
