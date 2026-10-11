---
title: 阿里云 Kubernetes 节点弹性与 ECS 自动供给
description: Rambow Cloud 社区维护的 阿里云 Karpenter provider，让 Pod 需求驱动 ECS 节点供给。了解核心功能、Kubernetes 1.34+ 支持边界、项目动机与 ACK 验收进度。
hide:
  - toc
  - navigation
---

<div class="project-hero" markdown>

<div class="project-brand">
  <img src="assets/rambow-karpenter.png" width="48" height="48" alt="Rambow Cloud 与 Karpenter K 项目标志">
  <span>RAMBOW CLOUD <span class="brand-divider">/</span> KARPENTER</span>
</div>

# 让阿里云节点，<br>跟随工作负载而伸缩。

<p class="hero-summary">以 AWS Karpenter 的节点弹性体验为方向，<br class="wide-break">为阿里云 Kubernetes 提供持续维护的 ECS 节点供给。</p>

[探索功能](docs/features/index.md){ .md-button .md-button--primary }
[快速开始](docs/quickstart/index.md){ .md-button }

<div class="project-baseline"><span>Karpenter v1.14.1</span><span>Kubernetes 1.34+</span><span>Apache-2.0</span></div>

</div>

## 探索项目

<div class="project-categories">
  <a href="docs/features/"><strong>核心功能</strong><span>Pod 驱动供给、节点配置与生命周期</span></a>
  <a href="docs/quickstart/"><strong>快速开始</strong><span>准备集群、配置权限并全新安装</span></a>
  <a href="docs/support/"><strong>支持版本与镜像</strong><span>Kubernetes 要求与 ACK 镜像选择边界</span></a>
  <a href="docs/status/"><strong>状态与路线图</strong><span>已完成的验证与正在推进的工作</span></a>
</div>

## 工作负载优先，云资源按需

阿里云 Karpenter 是 Rambow Cloud 维护的社区 provider：将 Pod 的资源请求和调度约束连接到阿里云 ECS，让节点配置围绕工作负载展开。

<div class="feature-grid" markdown>

<div class="feature-card" markdown>

<span class="feature-label">01 / PROVISIONING</span>

### 从 Pod 需求出发

通过 Karpenter core 接收待调度 Pod 的资源请求与约束，选择相应的 ECS 节点容量。

</div>

<div class="feature-card" markdown>

<span class="feature-label">02 / SCHEDULING</span>

### 声明式容量边界

使用 NodePool 描述实例规格、可用区、架构与容量类型；按需实例和 Spot 的创建路径分别处理。

</div>

<div class="feature-card" markdown>

<span class="feature-label">03 / CLOUD CONFIGURATION</span>

### 阿里云资源配置

ECSNodeClass 管理镜像、交换机、安全组与磁盘，让云端配置保持声明式与可追溯。

</div>

<div class="feature-card" markdown>

<span class="feature-label">04 / LIFECYCLE</span>

### 节点生命周期接入

连接实例创建、节点引导、发现和删除；集群所有权检查保护资源操作的边界。

</div>

<div class="feature-card" markdown>

<span class="feature-label">05 / COMPATIBILITY</span>

### 明确的版本策略

跟进 Karpenter v1.14.1，以 Kubernetes 1.34 为最低版本，Helm、启动与 ACK 配置均有检查。

</div>

<div class="feature-card" markdown>

<span class="feature-label">06 / MAINTENANCE</span>

### 持续维护，有据可查

阿里云 SDK V2.0、关闭 CGO 的构建与 Go 漏洞检查，通过 Issue、PR 和 CI 记录交付与验证。

</div>

</div>

上述是当前代码的能力与工程方向。**Chart 0.3.0-dev 仍为开发候选版本，全新部署的真实 ACK/ECS 生命周期验收待完成；Spot 中断处理仍在规划。** [查看功能与验证边界 →](docs/features/index.md)

## 为什么继续维护

我们想在阿里云实现类似 **AWS Karpenter** 的效果。官方 provider 提供了基础，但持续维护、上游版本跟进与问题修复尚未满足我们的实际需求——这是继续维护这个社区分支的动力。

我们保留 AliyunContainerService 的来源与 Apache-2.0 许可，通过公开审查和验证推进改进。[阅读项目动机 →](docs/motivation/index.md)

<div class="project-next" markdown>

### 了解边界，再选择下一步

[查看状态与路线图](docs/status/index.md) · [准备试用或贡献](docs/participate/index.md) · [GitHub 源码](https://github.com/rambow-cloud/karpenter-provider-alibabacloud)

</div>
