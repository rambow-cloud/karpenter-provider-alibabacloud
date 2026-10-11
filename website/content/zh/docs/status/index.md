# 当前状态与路线图

## 当前开发基线

Karpenter core 和 AWS 测试 harness 固定为 **v1.14.1**，chart 为 **0.3.0-dev**。版本迁移已经合并；这是开发候选版本，部署必须使用与当前代码匹配的自建镜像。

离线 CI 和 KWOK 回归已完成相应检查。**真实 ACK 运行、版本迁移和升级/回滚验收仍待完成**，不能用模拟节点的结果替代 ECS 与 ACK 的实际表现。详细说明见 [UPGRADING.md](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/UPGRADING.md)。

## 工作如何推进

| 方向 | 当前边界 | 跟踪入口 |
| --- | --- | --- |
| Karpenter v1.14.1 | 迁移已合并；真实 ACK 版本验收待完成 | [版本 milestone](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestone/1) |
| Kubernetes 1.34+ | 支持策略目标；版本边界检查尚在推进 | [Issue #7](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/7) |
| Provider 生命周期与调度 | 集群所有权修复已合并；资源计算、失败恢复、镜像等问题继续修复 | [Provider milestone](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestone/2) |
| 依赖安全 | Go CVE 复扫通过；旧 SDK/PDF 链已移除，剩余未导入的 OpenPGP 公告；容器 OS 扫描待完成 | [SDK 迁移与审计](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/SDK_MIGRATION.md) |
| 阿里云 SDK V2.0 | 剩余旧调用已迁移，已移除无用 ALB/NLB 清理；当前选中模块数 292（迁移前 314）；真实云验收待完成 | [SDK 迁移与后续精简](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/SDK_MIGRATION.md) |
| 最小 ACK/ECS E2E | 一个 NodePool、一个 Pod、目标一台 ECS 的验收入口正在规划 | [E2E milestone](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestone/4) |
| Spot 中断处理 | 独立规划的功能；不能将 Spot 创建等同于完整中断处理 | [Spot milestone](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestone/5) |

这是网站首次发布时的状态说明。实时进度以 [Issues](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues)、[Milestones](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestones) 和 [Project](https://github.com/orgs/rambow-cloud/projects/4) 为准。

## 如何判断一项能力已经完成

一项工作先写清问题与验收条件，再进入 `feat/<issue-number>-<description>` 分支和 PR。通过必需检查并合并后记录结果；涉及云端的行为还要注明 ACK/ECS 验证证据和限制。

网站发布、controller/chart 发布与真实云端能力验收是各自独立的交付，当前网站上线不代表生产验收已经完成。
