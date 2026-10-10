# Why we maintain this fork

## The motivation

We want **AWS Karpenter-like node provisioning on Alibaba Cloud**: start with Pod resource and scheduling requirements, choose suitable instance types and zones, provision nodes that can run the workload, and manage their retirement as part of a controlled lifecycle.

The official [Alibaba Cloud provider](https://github.com/AliyunContainerService/karpenter-provider-alibabacloud) established a starting point. Its ongoing maintenance, upstream version updates, and fixes have not met the maintainer's practical needs. **That gap is the motivation for continuing this community fork.**

We retain upstream attribution and licensing, and continue through public code review, issues, tests, and pull requests. This community project is managed separately from the official Alibaba Cloud project and the AWS provider.

## The experience we are working toward

1. **Workloads express requirements.** Pod resource requests, architecture, and scheduling constraints inform Karpenter's capacity decisions.
2. **NodePool defines the boundaries.** Users declare capacity types, instance requirements, zones, and lifecycle policies.
3. **ECSNodeClass describes cloud resources.** Users select images, vSwitches, security groups, and disks; the provider connects that configuration to ECS APIs.
4. **Nodes join the cluster.** An ECS instance completes bootstrap, registration, and initialization before hosting workloads. Discovery and deletion respect cluster ownership.

This is the intended workflow. Each stage needs implementation, regression coverage, and real cloud evidence. See the [project status](../status/index.md) for the current boundaries.

## Why look to the AWS provider

The [AWS Karpenter provider](https://github.com/aws/karpenter-provider-aws) informs the user experience and engineering practices we want: compatibility management, creation and failure recovery, resource accounting, scheduling feedback, lifecycle safety, and E2E validation.

Alibaba Cloud has its own ECS, ACK, networking, image, and permission APIs. Sharing Karpenter core does not establish feature parity. We work toward alignment through concrete issues and acceptance criteria, organized in [public milestones](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestones).

## Origins and license

The provider continues AliyunContainerService's open-source implementation under the [Apache License 2.0](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/LICENSE). This site is built with [Zensical](https://zensical.org/).
