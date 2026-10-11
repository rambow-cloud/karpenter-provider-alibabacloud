---
title: 支持版本与镜像
description: 阿里云 Karpenter 的 Kubernetes 1.34+ 版本要求、ACK 公共镜像兼容清单、Linux 与 containerd 支持、ContainerOS 数据盘和自定义镜像限制。
---

# 支持版本与镜像

部署前先确认目标 ACK 版本、区域和镜像属于当前实现范围，再按[快速开始](../quickstart/index.md)安装。以下区分代码检查与真实云端验收。

## Kubernetes 与 Karpenter 版本

| 项目 | 当前基线 | 验证范围 |
| --- | --- | --- |
| Kubernetes | 1.34 或更新版本；1.33 及更早版本不支持 | 最低版本由 Helm、controller 启动和 ACK 配置检查 |
| Karpenter core | v1.14.1 | 固定 core、CRD 和测试 harness 版本 |
| Chart | 0.3.0-dev | 开发候选，使用匹配源码构建的 controller 镜像 |
| 离线控制器测试 | Kubernetes 1.34 / 1.35 envtest | API 与控制器合约；不能替代 ACK/ECS 实际节点验证 |

目标 ACK 版本还必须在所选区域、集群类型可用且符合阿里云维护策略。通过最低版本检查不代表所有更高版本均已验收。当前交付按全新 provider 安装处理，真实 ACK 生命周期验收由 [Issue #5](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/5) 跟踪。

## 公共镜像按 ACK 兼容清单选择

默认 `imageCompatibilityPolicy: ACK`，也可省略该字段。controller 使用目标集群的实际版本、类型、Profile、区域和 containerd 查询 ACK 元数据，再以具体 ID 查询 ECS，核对 Available 状态、Linux OS 与架构。

- family、name 和 tags 在 ACK 已批准的镜像中匹配，ECS 最新家族镜像不一定已进入清单。
- 单个精确 ID 固定该镜像；不在清单或不可用时明确失败，不自动替换。
- 多个 selector 为无序 OR，单个 term 的条件为 AND。同架构候选按创建时间选择最新版本，时间缺失或无效时排在最后。
- 调度与创建都要求镜像/实例架构匹配；未知架构不能授权启动。
- 元数据查询失败会阻止解析，创建前重新验证候选；新版本出现不会单独触发仍在允许集合中的旧镜像节点 Drift。

## 操作系统与磁盘限制

| 镜像类型 | 当前行为 |
| --- | --- |
| ACK 公共 Linux 镜像 | 按目标 ACK/containerd 清单及 ECS 状态校验 |
| ContainerOS | 恰好一块显式有效的持久数据盘；不支持 ephemeral 数据盘或 instanceStorePolicy |
| ContainerOS + LaunchTemplate | 当前拒绝，模板数据盘传递仍由 #51 跟踪 |
| 自定义/共享镜像 | 显式 Custom 策略，仅支持精确 ID；基础 OS、runtime 和引导兼容性由使用者验证 |
| Windows / 未知 OS | 当前拒绝，不支持对应 bootstrap 与资源预留 |

GPU 驱动、Terway、网络插件及本地盘等前置条件仍需单独配置与验证，见 [Issue #52](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/52)。镜像解析成功不代表节点已成功注册。

## 自定义镜像配置

```yaml
spec:
  imageCompatibilityPolicy: Custom
  imageSelectorTerms:
    - id: m-your-verified-custom-image
```

Custom 只接受 ECS 返回的 self/others 镜像，并核对 Linux、架构和 Available 状态；不能绕过公共镜像的 ACK 校验，也不支持 name/family/tags 批量发现自定义镜像。

权限、ContainerOS 示例及详细行为见 [ACK_IMAGE_SUPPORT.md](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/ACK_IMAGE_SUPPORT.md)；版本证据见 [KUBERNETES_SUPPORT.md](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/KUBERNETES_SUPPORT.md)。继续阅读[核心功能](../features/index.md)或[状态与路线图](../status/index.md)。
