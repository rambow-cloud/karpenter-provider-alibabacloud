---
title: 核心功能与常见问题
description: 了解 阿里云 Karpenter 的 Pod 驱动供给、NodePool、ECSNodeClass、按需与 Spot 创建、节点生命周期及验证边界，回答最低版本、官方关系和生产可用性问题。
---

# 核心功能与常见问题

**阿里云 Karpenter 是 Rambow Cloud 持续维护的社区 provider，将 Karpenter 的容量决策连接到阿里云 ECS。** 目标是在阿里云提供类似 AWS Karpenter 的节点弹性体验，当前不是 AWS provider 的全部能力对等实现。

## 核心功能

| 能力 | 当前代码做什么 | 实现与验证入口 |
| --- | --- | --- |
| Pod 驱动的节点供给 | 将 Pod 资源请求和调度约束交给 Karpenter core，provider 查询 ECS 规格与 offering 并创建实例 | [CloudProvider](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/pkg/cloudprovider/cloudprovider.go) |
| 声明式容量配置 | NodePool 约束规格、架构、可用区与容量类型；支持按需和 Spot 创建路径 | [实例类型 provider](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/pkg/providers/instancetype/instancetype.go)；Spot 中断见下文 |
| 阿里云资源配置 | ECSNodeClass 选择镜像、交换机、安全组和磁盘；引导配置将实例接入目标集群 | [API 定义](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/tree/main/pkg/apis)；[Provider](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/tree/main/pkg/providers) |
| 生命周期与所有权 | 实例创建、发现、删除与 Drift 接入；所有权限制避免误操作其他集群资源 | [所有权修复 #2](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/2)；[待修复问题](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestone/2) |
| 版本边界 | core 固定 v1.14.1；Helm、controller 和 ACK 配置要求 Kubernetes >=1.34 | [支持策略](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/KUBERNETES_SUPPORT.md)；[已完成 #7](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/7) |
| 可维护性与验证 | 阿里云 SDK V2.0、CGO 关闭构建、单元/控制器测试、Go 漏洞扫描和 KWOK 验证入口 | [SDK 审计](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/SDK_MIGRATION.md)；[CI 记录](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/actions) |

## 实现与验收的边界

当前基线为 **Karpenter v1.14.1 / Chart 0.3.0-dev / Kubernetes 1.34+**。最低版本检查与离线测试已交付；全新部署的真实 ACK/ECS 生命周期验收仍由 [Issue #5](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/5) 跟踪。镜像解析、失败恢复、资源计算等已知问题继续在 provider milestone 中推进。

Spot 实例创建路径已经存在，**Spot 中断处理仍是计划中的功能**，见 [Issue #20](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/20)。不能将实例创建支持视为完整的中断通知、排空与替换支持。

每个 CI/KWOK 结果都应结合对应提交、集群版本和实际检查范围阅读。网站的功能展示不是生产验收结论；更新记录以公开 Issue 与 PR 为依据。[查看完整状态与路线图 →](../status/index.md)

## 常见问题

### 这是阿里云官方插件吗？

这是 Rambow Cloud 维护的社区分支，源自 [AliyunContainerService 的开源 provider](https://github.com/AliyunContainerService/karpenter-provider-alibabacloud)。持续维护、版本跟进和问题修复未满足维护者需求，促成了这个项目；它与官方项目分别管理。[了解项目动机](../motivation/index.md)。

### 与 AWS Karpenter 有什么关系？

本项目复用 Karpenter core，参考 AWS provider 的使用体验与工程实践，云资源接口则由阿里云 ECS、ACK 等服务提供。参考目标不代表 AWS 功能全部具备；请按上面的实现与验收入口判断具体能力。

### 最低支持哪个 Kubernetes 版本？

最低为 **1.34**，1.33 及更早版本不受支持。Helm、controller 启动和 ACK 配置都执行版本检查，并识别 ACK 发行版后缀。目标 ACK 版本还需在所选区域、集群类型可用且符合阿里云维护策略；更高版本通过最低检查不代表已完成运行验证。

### 现在可以直接用于生产吗？

当前 chart 是 **0.3.0-dev 开发候选版本**，全新部署的真实 ACK 验收证据待完成。先阅读 [上手指南](../participate/index.md)、[UPGRADING.md](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/UPGRADING.md) 与 [Issue #5](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/5)，使用匹配源码的自建 controller 镜像在隔离环境中验证。

### 如何参与维护？

通过 Issue 明确问题与验收条件，进入 `feat/<issue-number>-<description>` 分支，再提交关联 Issue 的 PR。贡献与试用入口见 [上手与参与](../participate/index.md)。项目采用 [Apache-2.0](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/LICENSE) 许可。

## ACK 镜像与操作系统边界

公共镜像按目标 ACK 区域、集群实际版本和 containerd 兼容清单选择，再通过 ECS 核对镜像状态、Linux OS 和架构。ECS 最新镜像不一定已进入 ACK 清单，精确 pin 不会被替换。元数据错误会阻止解析和启动。

当前 provider 只支持 Linux，拒绝 Windows 和未知 OS。ContainerOS 需要恰好一块显式持久数据盘，暂不支持通过 LaunchTemplate 使用 ContainerOS。自定义/共享镜像需要显式 `imageCompatibilityPolicy: Custom` 和精确 ID，基础 OS、runtime、GPU 驱动及启动兼容性由用户验证。

部署时配置 RAM 只读权限 `cs:DescribeKubernetesVersionMetadata` 并安装匹配的 CRD。[完整支持边界与配置示例](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/ACK_IMAGE_SUPPORT.md)。只读 API 测试不代表真实 ACK 节点生命周期已验收。
