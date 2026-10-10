---
title: Status and roadmap
description: Distinguish the merged development baseline, validation evidence, and remaining work.
weight: 20
---

## Current development baseline

Karpenter core and the AWS test harness are pinned to **v1.14.1**, with chart **0.3.0-dev**. The migration is merged. This is a development candidate; deployment requires an image built from matching source.

Offline CI and KWOK regression have completed their relevant checks. **Real ACK runtime, migration, and upgrade/rollback acceptance remain pending.** Simulated nodes do not establish actual ECS or ACK behavior. See [UPGRADING.md](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/UPGRADING.md).

## What is being worked on

| Area | Current boundary | Tracking |
| --- | --- | --- |
| Karpenter v1.14.1 | Migration merged; real ACK version acceptance pending | [Version milestone](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestone/1) |
| Kubernetes 1.34+ | Target support policy; version boundary enforcement is still planned | [Issue #7](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/7) |
| Provider lifecycle and scheduling | Ownership fixes merged; resource accounting, recovery, images, and other fixes remain | [Provider milestone](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestone/2) |
| Dependency security | Audit issues created; upgrades and final rescanning remain | [Security milestone](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestone/3) |
| Minimal ACK/ECS E2E | A one-NodePool, one-Pod, target-one-ECS acceptance entry is being planned | [E2E milestone](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestone/4) |
| Spot interruption | A separately planned feature; Spot creation does not establish interruption handling | [Spot milestone](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestone/5) |

This is the status at the website's initial launch. [Issues](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues), [Milestones](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestones), and the [Project](https://github.com/orgs/rambow-cloud/projects/4) carry live progress.

## What completion means

Each change starts with a problem and acceptance criteria, then a `feat/<issue-number>-<description>` branch and a pull request. Required checks and merge results are recorded. Cloud behavior also needs its ACK/ECS evidence and limitations.

Publishing this site, releasing a controller/chart, and accepting cloud capabilities are separate deliverables. The site launch does not establish production acceptance.
