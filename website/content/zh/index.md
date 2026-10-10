---
hide:
  - toc
---

# Alibaba Cloud Karpenter

<div class="project-intro" markdown>

![Rambow Cloud 羊头与云朵，配合 Karpenter 的 K](assets/rambow-karpenter.png){ width="160" height="160" }

<p class="tagline">让阿里云节点，跟随工作负载而伸缩。</p>

社区维护的 Alibaba Cloud Karpenter provider，以 AWS Karpenter 的节点弹性体验为方向。

[了解项目](docs/motivation/index.md){ .md-button .md-button--primary }
[查看源码](https://github.com/rambow-cloud/karpenter-provider-alibabacloud){ .md-button }

</div>

## 为什么做这个项目

我们希望在阿里云实现类似 **AWS Karpenter** 的效果：根据 Pod 的资源与调度需求，选择合适的 ECS 实例，创建节点，并持续管理节点的生命周期。

官方 Alibaba Cloud provider 提供了基础，但持续维护、Karpenter 版本跟进和问题修复尚未满足我们的实际需求。**这是 Rambow Cloud 继续维护这个社区分支的动力。** 我们保留上游来源，通过公开的 issue、代码审查与验证记录推进改进。

## 从工作负载到 ECS

| 配置 | 职责 |
| --- | --- |
| **Pod** | 提出资源请求、架构与调度约束 |
| **NodePool** | 定义节点容量、规格、可用区与生命周期边界 |
| **ECSNodeClass** | 选择镜像、交换机、安全组与磁盘 |
| **Provider** | 连接 ECS API，推进实例创建、节点注册与回收 |

这是一条持续完善的工作流。AWS provider 是我们参考的体验与工程方向，能力对齐通过具体问题与验收条件推进。[阅读项目动机 →](docs/motivation/index.md)

## 当前进展

**Karpenter core v1.14.1 · Chart 0.3.0-dev · 社区开发候选版本**

版本迁移和相关离线、KWOK 检查已推进。真实 ACK/ECS 运行及升级、回滚验收仍待完成；Kubernetes 1.34+ 支持策略、依赖安全、镜像查询与生命周期修复分别由现有 issue 跟踪。

[状态与路线图](docs/status/index.md){ .md-button }
[上手与参与](docs/participate/index.md){ .md-button }

<p class="project-status">社区分支延续自 AliyunContainerService，采用 Apache-2.0 许可。实时进度见 GitHub Issues 与 Milestones。</p>
