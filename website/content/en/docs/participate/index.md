---
title: Get started and contribute
description: Prepare an isolated Alibaba Cloud Karpenter trial with a matching controller
  image. Find Kubernetes requirements, deployment documents and the issue, branch
  and PR workflow.
---

# Get started and contribute

## Before trying the provider

Read the [current status](../status/index.md), [quick start](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/QUICK_START.md), and [upgrade notes](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/UPGRADING.md). Historical quick-start version examples need the upgrade notes; an old image must not be paired with the current development chart.

Identify the ACK cluster, region, VPC/vSwitches, security groups, available images, and RAM permissions, and use an isolated test environment. The current candidate requires a controller image built from matching source. Follow the upgrade notes for build requirements and feature-gate migration.

Kubernetes 1.34+ is required; 1.33 and earlier are unsupported. Upgrade the cluster before installing this candidate. Version enforcement is tracked in [Issue #7](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/7). Relevant milestones track real cloud acceptance. Check the target environment against the documented boundaries first.

## Choose a contribution

- [Report a bug](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/new?template=bug_report.yml): include a minimal reproduction, versions, expected behavior, and sanitized logs.
- [Propose a feature](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/new?template=feature_request.yml): describe the use case and observable acceptance criteria.
- [Explore milestones](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestones): distinguish compatibility, general fixes, security, and testing.
- [Open the project board](https://github.com/orgs/rambow-cloud/projects/4): find Todo items and work in progress.

## Delivery workflow

1. Create or claim an issue with scope, milestone, and acceptance criteria.
2. Create a `feat/<issue-number>-<short-description>` branch and move the task to In Progress.
3. Open a pull request using the repository template and link `Closes #<issue-number>`.
4. Complete relevant validation and required CI. Record results after merge before marking the task Done.

See [CONTRIBUTING.md](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/CONTRIBUTING.md). Keep published changes and validation evidence traceable. Do not include credentials, kubeconfig contents, or account identifiers in code or issues.
