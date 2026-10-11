---
title: Features and frequently asked questions
description: Explore Pod-driven ECS provisioning, NodePool, ECSNodeClass, on-demand and Spot creation, lifecycle integration and validation boundaries. Find answers on versions, ownership and production readiness.
---

# Features and frequently asked questions

**Alibaba Cloud Karpenter is Rambow Cloud's maintained community provider, connecting Karpenter capacity decisions to Alibaba Cloud ECS.** The goal is an AWS Karpenter-like provisioning experience on Alibaba Cloud; it does not currently implement every AWS provider capability.

## Core features

| Capability | What the current code does | Implementation and evidence |
| --- | --- | --- |
| Pod-driven node provisioning | Karpenter core processes Pod resource requests and scheduling constraints; the provider queries ECS instance types and offerings and creates instances | [CloudProvider](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/pkg/cloudprovider/cloudprovider.go) |
| Declarative capacity configuration | NodePool constrains instance requirements, architecture, zones and capacity types, with on-demand and Spot creation paths | [Instance type provider](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/pkg/providers/instancetype/instancetype.go); see the Spot boundary below |
| Alibaba Cloud resource configuration | ECSNodeClass selects images, vSwitches, security groups and disks; bootstrap configuration connects instances to the target cluster | [API definitions](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/tree/main/pkg/apis); [providers](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/tree/main/pkg/providers) |
| Lifecycle and ownership | Instance creation, discovery, deletion and Drift integration; ownership checks bound operations to the intended cluster | [Ownership fix #2](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/2); [remaining fixes](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestone/2) |
| Version boundaries | Core pinned to v1.14.1; Helm, controller and ACK configuration require Kubernetes >=1.34 | [Version policy](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/KUBERNETES_SUPPORT.md); [completed #7](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/7) |
| Maintenance and verification | Alibaba Cloud SDK V2.0, CGO-disabled builds, unit/controller tests, Go vulnerability scanning and KWOK entry points | [SDK audit](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/SDK_MIGRATION.md); [CI evidence](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/actions) |

## Implementation and acceptance boundaries

The baseline is **Karpenter v1.14.1 / Chart 0.3.0-dev / Kubernetes 1.34+**. The minimum-version checks and offline tests are delivered. Real ACK/ECS lifecycle, migration and upgrade/rollback acceptance remain tracked by [Issue #5](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/5). Known image resolution, failure recovery and resource accounting issues remain in the provider milestone.

The Spot creation path exists. **Spot interruption handling is still planned**, under [Issue #20](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/20). Creating Spot instances does not establish interruption notification, draining or replacement support.

Read each CI/KWOK result with its commit, cluster version and actual check scope. Feature descriptions do not establish production acceptance; public issues and PRs carry delivery evidence. [Review the full status and roadmap →](../status/index.md)

## Frequently asked questions

### Is this an official Alibaba Cloud plugin?

This is Rambow Cloud's community fork of [AliyunContainerService's open-source provider](https://github.com/AliyunContainerService/karpenter-provider-alibabacloud). Maintenance, upstream updates and fixes have not met the maintainers' practical needs, motivating this project. It is managed separately from the official project. [Read the motivation](../motivation/index.md).

### How does it relate to AWS Karpenter?

The project reuses Karpenter core and draws on the AWS provider's experience and engineering practices, while Alibaba Cloud ECS, ACK and related services provide the cloud APIs. That goal does not imply full AWS feature parity; use the implementation and evidence links above to assess individual capabilities.

### What is the minimum Kubernetes version?

**1.34** is the minimum; 1.33 and earlier are unsupported. Helm, controller startup and ACK configuration check it, including ACK distribution suffixes. The selected ACK version must also be available for the region and cluster type and meet Alibaba Cloud's maintenance policy. Passing the minimum check on a later version does not establish runtime compatibility.

### Can I deploy it directly to production?

The current chart is a **0.3.0-dev development candidate**. Real ACK acceptance and upgrade/rollback evidence remain pending. Read [getting started](../participate/index.md), [UPGRADING.md](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/UPGRADING.md) and [Issue #5](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/5), then validate an image built from matching source in an isolated environment.

### How can I contribute?

Define the problem and acceptance criteria in an issue, work on a `feat/<issue-number>-<description>` branch, then submit a linked PR. See [getting started and contributing](../participate/index.md). The provider uses the [Apache-2.0 license](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/LICENSE).

## ACK image and operating system boundary

Public images come from the target ACK region, actual cluster version and containerd compatibility list, with ECS checks for availability, Linux OS and architecture. The latest ECS family image may differ from the ACK-approved version. Exact pins are never substituted; metadata failures block resolution and creation.

This provider supports Linux only and rejects Windows or unknown OS values. ContainerOS requires exactly one explicitly configured persistent data disk; ContainerOS with a launch template is currently rejected. Custom/shared images require `imageCompatibilityPolicy: Custom` and exact IDs. Users must verify provenance, runtime, GPU drivers and bootstrap compatibility.

Before upgrading, add the read-only RAM action `cs:DescribeKubernetesVersionMetadata` and update the CRD. See [the complete image policy and configuration examples](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/ACK_IMAGE_SUPPORT.md). Read-only API tests do not establish real ACK lifecycle acceptance.
