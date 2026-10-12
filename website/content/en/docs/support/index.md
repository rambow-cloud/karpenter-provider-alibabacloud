---
title: Versions and images
description: Kubernetes 1.34+ requirements for Alibaba Cloud Karpenter, ACK-approved public images, Linux and containerd support, ContainerOS disks and custom-image restrictions.
---

# Versions and images

Check the target ACK version, region and image against the current implementation before following the [quick start](../quickstart/index.md). Code checks and real cloud acceptance are recorded separately.

## Kubernetes and Karpenter versions

| Component | Current baseline | Validation scope |
| --- | --- | --- |
| Kubernetes | 1.34 or newer; 1.33 and earlier unsupported | Helm, controller startup and ACK configuration enforce the minimum |
| Karpenter core | v1.14.1 | Core, CRDs and test harness pinned together |
| Chart | 0.3.0-dev | Development candidate; build the controller image from matching source |
| Offline controller tests | Kubernetes 1.34 / 1.35 envtest | API/controller contracts; not actual ACK/ECS node validation |

The target ACK version must also be available for the region and cluster type and meet Alibaba Cloud's maintenance policy. Passing the minimum check does not establish acceptance of every later version. Current delivery targets a fresh provider installation; [Issue #5](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/5) tracks real ACK lifecycle acceptance.

## Public images use the ACK compatibility list

The default policy is `imageCompatibilityPolicy: ACK`; the field may be omitted. The controller queries ACK metadata using the actual cluster version, type, profile, region and containerd, then checks concrete IDs in ECS for Available status, Linux OS and matching architecture.

- Family, name and tags match ACK-approved candidates. The latest ECS family image may not yet be approved.
- A single exact ID pins that image. An unavailable or unapproved pin fails explicitly rather than being replaced.
- Selector terms are unordered OR alternatives; fields within a term are AND filters. New launches prefer the newest creation time for a compatible architecture; missing/invalid timestamps rank last.
- Scheduling and creation require matching image/instance architectures. Unknown architecture cannot authorize a launch.
- Metadata errors block resolution and candidates are revalidated before creation. A new image release alone does not drift nodes whose older images remain allowed.

## Operating systems and disks

| Image type | Current behavior |
| --- | --- |
| ACK public Linux images | Checked against the target ACK/containerd list and ECS status |
| ContainerOS | Exactly one new valid persistent data disk; reject snapshot/ephemeral disks, instanceStorePolicy and instance types with local or unknown local-disk metadata |
| ContainerOS + LaunchTemplate | Resolve and pin the numeric version and disk snapshot; ACK bootstrap initializes the data disk when the layout meets those requirements, without adding disks |
| Custom/shared images | Explicit Custom policy, exact IDs and imagePrerequisitesVerified: true; users confirm OS, runtime and bootstrap compatibility |
| Windows / unknown OS | Rejected; corresponding bootstrap and resource reservations are not supported |

Alibaba Cloud Linux 4 requires a known active Flannel addon, or Terway `[1.9.18, 1.10.0)` / `>=1.16.4`. Missing, conflicting, upgrading or unknown network metadata is rejected. Installed addons are queried again before creation so cached metadata cannot hide a version change.

GPU images must match GPU hardware; only known NVIDIA hardware is exposed. Users verify drivers, runtime, device plugins and resource registration. Ubuntu CPFS, security hardening and initialization restrictions also require verification; the node label `alibabacloud.com/image-accelerate-enabled: "true"` is incompatible with Ubuntu. GPU, Ubuntu and Custom use `imagePrerequisitesVerified: true` to record user confirmation, which cannot waive known network, architecture or hardware restrictions. See [implemented prerequisites #52](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/52); resolving an image does not establish successful node registration.

## Custom image configuration

```yaml
spec:
  imageCompatibilityPolicy: Custom
  imagePrerequisitesVerified: true # Confirm only after the checks below
  imageSelectorTerms:
    - id: m-your-verified-custom-image
```

Custom permits ECS self/others images and checks Linux, architecture and launch availability. It cannot bypass ACK validation for public images or discover custom images in bulk through name/family/tags. Before confirming the field, verify provenance, Swap/system settings, container runtime, ACK bootstrap, drivers and required plugins. Confirmation is not provider certification.

## Template and pricing boundaries

Native template versions and disks are recorded in status; RunInstances and bootstrap use the same snapshot. NodeClass disk overrides are not supported. If template provisioned disk IOPS or bursting cannot be fully priced, `status.launchTemplate.pricing.unsupportedReason` explains the exclusion from automatic provisioning.

On-demand and Spot use ECS hourly quotes for the actual image and disks. Missing, failed, expired or pending quotes temporarily make an offering unavailable while retaining its drift membership. Quotes use one account currency, CNY or USD, without assumed exchange rates; marketplace images also require complete licensing quotes in the same currency. On-demand quotes last 5 minutes, Spot 1 minute and failures 30 seconds. Refresh concurrency and request rate are bounded; cold catalogs become available as valid quotes arrive. This is not an invoice forecast and excludes later usage charges such as traffic and snapshots. See [provider pricing and lifecycle behavior](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/PROVIDER_BEHAVIOR.md).

See [ACK_IMAGE_SUPPORT.md](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/ACK_IMAGE_SUPPORT.md) for permissions, ContainerOS examples and detailed behavior, and [KUBERNETES_SUPPORT.md](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/KUBERNETES_SUPPORT.md) for version evidence. Continue to [core features](../features/index.md) or [status and roadmap](../status/index.md).

## Native ACK pool and node resources

This section describes the **dev branch** changes in [resource accounting #16](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/16) and [network discovery #76](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/76). Use controller, Chart and CRDs from the same dev commit. A dev merge does not mean the change has shipped on main.

`spec.ackNodePoolID` selects an existing active regular ACK ESS/containerd pool. When omitted, exactly one default pool must exist. Grant `cs:DescribeClusterNodePools` and `cs:DescribeClusterNodePoolDetail`. Status records the actual pool ID and kubelet settings; attach scripts pin that ID. Creation reads the pool again and waits for a status refresh if settings changed. The provider does not modify the native pool.

**Omit `spec.kubelet`.** ACK attach scripts do not apply its overrides, so CRD admission and runtime reject it, including maxPods, podsPerCore and reservations. Configure supported kubelet parameters on the selected native pool. The API does not expose podsPerCore, so it cannot authorize predicted density. Native userdata, auto mode, CPU/memory manager and soft eviction overrides are unsupported.

| Resource | dev calculation boundary |
| --- | --- |
| Pods | Minimum of native maxPods and network ceiling; Flannel requires the actual valid CIDR mask and is capped at 256; Terway requires known ECS ENI/IP metadata and conservatively excludes trunk expansion |
| CPU / memory | ACK 1.28+ reservation rules and native kube/system settings; default memory reservation uses the larger network/native limit to avoid under-reserving |
| VM / eviction | VM memory loss reduces Capacity once; eviction is separate, with ACK default memory.available of 300Mi; custom hard eviction must explicitly include it |
| ephemeral-storage | Only image/configuration-matched nodefs measurements authorize new-node storage requests; independent disk sizes are never summed |
| Registration / recovery | The first ECS request stores an immutable resource snapshot shared by Create/Get/List and recovery; registered kubelet reports require matching NodeClaim UID and ProviderID |

Startup requires exactly one known active Terway/Flannel addon. API errors, missing/conflicting metadata, unknown IP stack or invalid configuration block initialization. Unknown Terway ENI metadata cannot authorize candidates; exclusive ENI and non-default pod-density-mode are unsupported. The conservative limit may be below actual node capacity; real network behavior remains an ACK acceptance item.

## Optional ephemeral-storage calibration

Without `nodeFilesystem`, ordinary CPU/memory workloads can proceed, while ephemeral-storage requests cannot use unmeasured new-node candidates. To enable them, measure an independent calibration node using the same image, disks, native pool and custom userdata. Recalibrate after changing those settings.

1. Resolve an ECSNodeClass without `nodeFilesystem`; read `status.resourceConfigurationHash` and the actual image ID.
2. Read the matching node's kubelet nodefs Capacity/Allocatable, rather than nominal ECS disk size.
3. Record those measured values, exact image ID and configuration hash in the NodeClass. Replace every example value below.

```sh
kubectl get ecsnodeclass default -o jsonpath='{.status.resourceConfigurationHash}{"\n"}'
kubectl get node YOUR_CALIBRATION_NODE -o jsonpath='{.status.capacity.ephemeral-storage}{"\n"}{.status.allocatable.ephemeral-storage}{"\n"}'
```

```yaml
spec:
  ackNodePoolID: np-YOUR_EXISTING_ACK_POOL
  nodeFilesystem:
    configurationHash: REPLACE_WITH_ACTUAL_64_CHARACTER_HASH
    imageID: REPLACE_WITH_EXACT_MEASURED_IMAGE_ID
    capacity: 37Gi     # Replace with measured kubelet Capacity
    allocatable: 32Gi  # Replace with measured kubelet Allocatable
```

The digest covers effective disks, native pool settings and userdata; image ID is matched separately. Capacity must be positive and Allocatable no greater than Capacity, consistent with known reservations/eviction. Capacity cannot exceed any individual launch cloud disk. Do not infer combined disks, unverified RAID or separate imagefs layouts from this example. ContainerOS still requires its single new persistent data disk.

Formulas, API requests and recovery have offline evidence. **Target ACK calibration and actual node-resource agreement remain release acceptance in #5/#23.** This documentation change did not run cloud tests. See [dev resource behavior and limitations](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/dev/PROVIDER_BEHAVIOR.md#ack-节点资源计算) and [ACK's reservation policy](https://help.aliyun.com/en/ack/ack-managed-and-ack-dedicated/user-guide/resource-reservation-policy).
