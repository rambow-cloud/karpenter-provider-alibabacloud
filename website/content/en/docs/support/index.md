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
