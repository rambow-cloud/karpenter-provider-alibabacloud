---
title: Status and roadmap
description: Review Karpenter v1.14.1, Kubernetes 1.34+, SDK, security and lifecycle
  progress. Distinguish offline CI and KWOK checks from pending real ACK acceptance.
---

# Status and roadmap

## Current development baseline

Karpenter core and the AWS test harness are pinned to **v1.14.1**, with chart **0.3.0-dev**. The migration is merged. This is a development candidate; deployment requires an image built from matching source.

Offline CI covers the Kubernetes minimum. KWOK results must be read with each run's actual Kubernetes baseline; pinning a new baseline alone does not establish a complete regression pass. **Fresh-install ACK runtime acceptance remain pending.** Simulated nodes do not establish actual ECS or ACK behavior. See [Kubernetes support](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/KUBERNETES_SUPPORT.md) and [UPGRADING.md](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/UPGRADING.md).

## What is being worked on

| Area | Current boundary | Tracking |
| --- | --- | --- |
| Karpenter v1.14.1 | Migration merged; real ACK version acceptance pending | [Version milestone](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestone/1) |
| Kubernetes 1.34+ | Minimum enforced by Helm, controller startup and ACK setup; offline boundary checks; real ACK acceptance pending | [Issue #7](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/7) |
| Provider lifecycle and scheduling | Ownership, recovery, template disks, image prerequisites and pricing have implementation/offline evidence; resource accounting #16 remains | [Provider milestone](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestone/2) |
| Dependency security | Go CVE rescans pass; legacy SDK/PDF chain removed; unused OpenPGP advisory remains; container OS scan pending | [SDK migration and audit](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/SDK_MIGRATION.md) |
| Alibaba Cloud SDK V2.0 | Remaining legacy calls migrated; unused ALB/NLB cleanup removed; selected modules now 292 (314 before migration); real cloud acceptance pending | [SDK migration and follow-up](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/SDK_MIGRATION.md) |
| Minimal ACK/ECS E2E | A one-NodePool, one-Pod, target-one-ECS acceptance entry is being planned | [E2E milestone](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestone/4) |
| Spot interruption | A separately planned feature; Spot creation does not establish interruption handling | [Spot milestone](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestone/5) |

Content reviewed **2026-10-12**. [Issues](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues), [Milestones](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestones), and the [Project](https://github.com/orgs/rambow-cloud/projects/4) carry live progress.

## dev integration workflow

Individual issue PRs target `dev` and merge after review and required CI. After the current development batch is accepted, rebase dev onto the latest main and open an integration PR. A dev merge is neither a main release nor ACK acceptance; online Pages update only after later main integration.

## What completion means

Each change starts with a problem and acceptance criteria, then a `feat/<issue-number>-<description>` branch and a pull request. Required checks and merge results are recorded. Cloud behavior also needs its ACK/ECS evidence and limitations.

Publishing this site, releasing a controller/chart, and accepting cloud capabilities are separate deliverables. The site launch does not establish production acceptance.
