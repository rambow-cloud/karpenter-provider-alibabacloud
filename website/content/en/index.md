---
hide:
  - toc
---

# Alibaba Cloud Karpenter

<div class="project-intro" markdown>

![Rambow Cloud ram and cloud with a K for Karpenter](assets/rambow-karpenter.png){ width="160" height="160" }

<p class="tagline">Alibaba Cloud nodes that follow your workloads.</p>

A community-maintained Alibaba Cloud Karpenter provider, working toward the AWS Karpenter node provisioning experience.

[About the project](docs/motivation/index.md){ .md-button .md-button--primary }
[View source](https://github.com/rambow-cloud/karpenter-provider-alibabacloud){ .md-button }

</div>

## Why this project exists

We want **AWS Karpenter-like behavior on Alibaba Cloud**: use Pod resource and scheduling requirements to choose suitable ECS instances, provision nodes, and manage their lifecycle.

The official Alibaba Cloud provider established a foundation, but its ongoing maintenance, Karpenter updates, and fixes have not met our practical needs. **That gap motivates Rambow Cloud to maintain this community fork.** We preserve upstream attribution and make improvements through public issues, code review, and validation evidence.

## From workloads to ECS

| Configuration | Responsibility |
| --- | --- |
| **Pod** | Resource requests, architecture, and scheduling constraints |
| **NodePool** | Capacity, instance requirements, zones, and lifecycle boundaries |
| **ECSNodeClass** | Images, vSwitches, security groups, and disks |
| **Provider** | ECS API integration, instance creation, node registration, and retirement |

This workflow is being improved. The AWS provider informs our intended experience and engineering practices; alignment is tracked through concrete issues and acceptance criteria. [Read the motivation →](docs/motivation/index.md)

## Current progress

**Karpenter core v1.14.1 · Chart 0.3.0-dev · Community development candidate**

The version migration and relevant offline and KWOK checks have progressed. Real ACK/ECS runtime and upgrade/rollback acceptance remain pending. Kubernetes 1.34+ policy, dependency security, image resolution, and lifecycle fixes are tracked separately.

[Status and roadmap](docs/status/index.md){ .md-button }
[Get started and contribute](docs/participate/index.md){ .md-button }

<p class="project-status">A community fork of AliyunContainerService's provider, under Apache-2.0. GitHub Issues and Milestones track live progress.</p>
