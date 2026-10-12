---
title: 状态与路线图
description: 查看 Karpenter v1.14.1、Kubernetes 1.34+、SDK、安全和生命周期工作的当前状态，区分离线 CI、KWOK
  与待完成的真实 ACK 验收。
---

# 状态与路线图

## 当前开发基线

Karpenter core 和 AWS 测试 harness 固定为 **v1.14.1**，chart 为 **0.3.0-dev**。版本迁移已经合并；这是开发候选版本，部署必须使用与当前代码匹配的自建镜像。

离线 CI 覆盖 Kubernetes 最低版本边界。KWOK 结果应结合各次运行的实际 Kubernetes 基线查看，固定新基线本身不代表完整回归通过。**全新部署的真实 ACK 运行验收仍待完成**，不能用模拟节点的结果替代 ECS 与 ACK 的实际表现。详见 [Kubernetes 支持边界](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/KUBERNETES_SUPPORT.md) 和 [UPGRADING.md](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/UPGRADING.md)。

## 工作如何推进

| 方向 | 当前边界 | 跟踪入口 |
| --- | --- | --- |
| Karpenter v1.14.1 | 迁移已合并；真实 ACK 版本验收待完成 | [版本 milestone](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestone/1) |
| Kubernetes 1.34+ | Helm、控制器启动和 ACK setup 已限制最低版本，覆盖离线边界；真实 ACK 验收待完成 | [Issue #7](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/7) |
| Provider 生命周期与调度 | 所有权、失败恢复、模板磁盘、镜像前置条件与报价已有实现及离线证据；资源计算 #16 与严格网络发现 #76 已完成源码与离线检查；实际节点资源校准待 #5/#23 验收 | [Provider milestone](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestone/2) |
| 依赖安全 | Go CVE 复扫通过；旧 SDK/PDF 链已移除，剩余未导入的 OpenPGP 公告；容器 OS 扫描待完成 | [SDK 迁移与审计](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/SDK_MIGRATION.md) |
| 阿里云 SDK V2.0 | 剩余旧调用已迁移，已移除无用 ALB/NLB 清理；当前选中模块数 292（迁移前 314）；真实云验收待完成 | [SDK 迁移与后续精简](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/SDK_MIGRATION.md) |
| 最小 ACK/ECS E2E | 一个 NodePool、一个 Pod、目标一台 ECS 的验收入口正在规划 | [E2E milestone](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestone/4) |
| Spot 中断处理 | 独立规划的功能；不能将 Spot 创建等同于完整中断处理 | [Spot milestone](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestone/5) |

内容复核日期：**2026-10-12**。实时进度以 [Issues](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues)、[Milestones](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestones) 和 [Project](https://github.com/orgs/rambow-cloud/projects/4) 为准。

## dev 集成流程

单个 issue 的 PR 先以 `dev` 为目标，经 review 和必要 CI 后合入。已验收的代码批次可先作为开发检查点集成到 main：将 dev rebase 到最新 main，建立集成 PR 并通过必要检查。主干集成后执行小规模 KWOK，仍不能替代 #5/#23 的真实 ACK 验收；#20 的 Spot 中断功能单独推进。Pages 随 main 更新，controller/chart 稳定发布另行安排。

## 如何判断一项能力已经完成

一项工作先写清问题与验收条件，再进入 `feat/<issue-number>-<description>` 分支和 PR。通过必需检查并合并后记录结果；涉及云端的行为还要注明 ACK/ECS 验证证据和限制。

网站发布、controller/chart 发布与真实云端能力验收是各自独立的交付，当前网站上线不代表生产验收已经完成。
