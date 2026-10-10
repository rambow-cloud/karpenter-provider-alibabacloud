---
title: 为什么继续维护
description: 以 AWS Karpenter 的体验为方向，延续 Alibaba Cloud provider 的维护。
weight: 10
---

## 项目的动力

我们希望在阿里云获得类似 **AWS Karpenter** 的节点弹性效果：从 Pod 的资源与调度需求出发，选择合适的实例规格和可用区，创建能够承载负载的节点，并把节点回收纳入可控的生命周期。

官方 [Alibaba Cloud provider](https://github.com/AliyunContainerService/karpenter-provider-alibabacloud) 提供了这个方向的起点。但它的持续维护、上游版本跟进与问题修复，尚未满足维护者的实际使用需求。**这就是本项目继续维护这个社区分支的动力。**

我们保留上游的来源与许可，通过公开代码审查、issue、测试和 PR 补足这些工作。本项目由社区维护，和阿里云官方项目及 AWS provider 分别管理。

## 想实现怎样的体验

1. **工作负载提出需求。** Pod 的资源请求、架构和调度约束进入 Karpenter 的容量决策。
2. **NodePool 定义边界。** 用户声明容量类型、规格、可用区和节点生命周期策略。
3. **ECSNodeClass 定义云资源。** 用户选择镜像、交换机、安全组与磁盘，provider 将这些配置连接到 ECS API。
4. **节点进入集群。** 新 ECS 完成引导、注册和初始化后承载工作负载；实例发现与删除遵守集群所有权边界。

这是我们要推进的工作流。每一环都需要实现、回归与真实云端证据；当前完成程度见[项目状态](../status/)。

## 为什么参考 AWS provider

[AWS Karpenter provider](https://github.com/aws/karpenter-provider-aws) 是我们参考的使用体验和工程实践：兼容性管理、实例创建与失败恢复、资源计算、调度反馈、节点生命周期和 E2E 验证。

阿里云有自己的 ECS、ACK、网络、镜像和权限接口，不能把 AWS 的实现直接照搬，也不能因为共用 Karpenter core 就推定具有相同能力。我们以实际问题和验收条件推进对齐，路线图通过[公开 milestone](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestones)管理。

## 来源与许可

Provider 延续自 AliyunContainerService 的开源实现，项目代码遵循 [Apache License 2.0](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/LICENSE)。网站使用 [Pigsty OINK](https://github.com/pgsty/oink) 与 [Hugo](https://gohugo.io/) 构建。
