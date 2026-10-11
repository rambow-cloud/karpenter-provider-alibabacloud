---
title: Kubernetes node provisioning on Alibaba Cloud ECS
description: Rambow Cloud's community-maintained Alibaba Cloud Karpenter provider connects Pod demand to ECS capacity. Explore features, Kubernetes 1.34+ requirements and pending ACK acceptance.
hide:
  - toc
  - navigation
---

<div class="project-hero" markdown>

<div class="project-brand">
  <img src="assets/rambow-karpenter.png" width="48" height="48" alt="Rambow Cloud and Karpenter K project mark">
  <span>RAMBOW CLOUD <span class="brand-divider">/</span> KARPENTER</span>
</div>

# Alibaba Cloud nodes.<br>Driven by your workloads.

<p class="hero-summary">Working toward the AWS Karpenter experience,<br class="wide-break">with a maintained ECS provider for Kubernetes on Alibaba Cloud.</p>

[Explore features](docs/features/index.md){ .md-button .md-button--primary }
[Quick start](docs/quickstart/index.md){ .md-button }

<div class="project-baseline"><span>Karpenter v1.14.1</span><span>Kubernetes 1.34+</span><span>Apache-2.0</span></div>

</div>

## Explore the project

<div class="project-categories">
  <a href="docs/features/"><strong>Core features</strong><span>Pod-driven provisioning, configuration and lifecycle</span></a>
  <a href="docs/quickstart/"><strong>Quick start</strong><span>Prepare credentials and install from scratch</span></a>
  <a href="docs/support/"><strong>Versions and images</strong><span>Kubernetes requirements and ACK image boundaries</span></a>
  <a href="docs/status/"><strong>Status and roadmap</strong><span>Completed validation and work in progress</span></a>
</div>

## Workloads first. Cloud capacity on demand.

Alibaba Cloud Karpenter is Rambow Cloud's community provider. It connects Pod resource requests and scheduling constraints to Alibaba Cloud ECS, so node configuration starts with workload needs.

<div class="feature-grid" markdown>

<div class="feature-card" markdown>

<span class="feature-label">01 / PROVISIONING</span>

### Start with Pod demand

Use Karpenter core to process pending Pods' resource requests and scheduling constraints, then select suitable ECS node capacity.

</div>

<div class="feature-card" markdown>

<span class="feature-label">02 / SCHEDULING</span>

### Declare capacity boundaries

Define instance requirements, zones, architecture and capacity types through NodePool, with separate on-demand and Spot creation paths.

</div>

<div class="feature-card" markdown>

<span class="feature-label">03 / CLOUD CONFIGURATION</span>

### Configure Alibaba Cloud resources

Manage images, vSwitches, security groups and disks through ECSNodeClass, keeping cloud configuration declarative and traceable.

</div>

<div class="feature-card" markdown>

<span class="feature-label">04 / LIFECYCLE</span>

### Connect the node lifecycle

Integrate instance creation, bootstrap, discovery and deletion. Cluster ownership checks bound resource operations.

</div>

<div class="feature-card" markdown>

<span class="feature-label">05 / COMPATIBILITY</span>

### Make version requirements explicit

Follow Karpenter v1.14.1 with Kubernetes 1.34 as the minimum, checked by Helm, controller startup and ACK configuration.

</div>

<div class="feature-card" markdown>

<span class="feature-label">06 / MAINTENANCE</span>

### Maintain with evidence

Alibaba Cloud SDK V2.0, CGO-disabled builds and Go vulnerability checks, with delivery and verification recorded in issues, PRs and CI.

</div>

</div>

These describe current code capabilities and engineering direction. **Chart 0.3.0-dev remains a development candidate. Fresh-install ACK/ECS lifecycle acceptance are pending; Spot interruption handling is planned.** [Review features and validation boundaries →](docs/features/index.md)

## Why we keep maintaining it

We want **AWS Karpenter-like behavior on Alibaba Cloud**. The official provider established a foundation, but ongoing maintenance, upstream updates and fixes have not met our practical needs. That gap motivates this community fork.

We preserve AliyunContainerService attribution and the Apache-2.0 license, and improve the project through public review and validation. [Read the motivation →](docs/motivation/index.md)

<div class="project-next" markdown>

### Understand the boundaries. Choose your next step.

[Status and roadmap](docs/status/index.md) · [Try or contribute](docs/participate/index.md) · [Source on GitHub](https://github.com/rambow-cloud/karpenter-provider-alibabacloud)

</div>
