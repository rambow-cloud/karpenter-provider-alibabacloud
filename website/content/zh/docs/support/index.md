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
| ContainerOS | 恰好一块全新有效的持久数据盘；拒绝快照盘、ephemeral、instanceStorePolicy，以及本地盘或本地盘信息未知的规格 |
| ContainerOS + LaunchTemplate | 解析并固定数字版本，使用模板磁盘快照；布局满足上述要求时由 ACK bootstrap 初始化数据盘，不额外加盘 |
| 自定义/共享镜像 | 显式 Custom 策略、精确 ID 和 imagePrerequisitesVerified: true；基础 OS、runtime 和引导兼容性由使用者确认 |
| Windows / 未知 OS | 当前拒绝，不支持对应 bootstrap 与资源预留 |

阿里云 Linux 4 需要已知且处于 active 状态的 Flannel，或 Terway 版本 `[1.9.18, 1.10.0)` / `>=1.16.4`；缺失、冲突、升级中或未知网络信息均拒绝。创建前重新查询已安装 addon，不能用旧缓存掩盖版本变化。

GPU 镜像与 GPU 规格必须匹配；当前只开放已知 NVIDIA 硬件，驱动、runtime、device plugin 与资源注册由使用者验证。Ubuntu 的 CPFS、安全加固及初始化限制也需验证；节点标签 `alibabacloud.com/image-accelerate-enabled: "true"` 与 Ubuntu 不兼容。GPU、Ubuntu 和 Custom 使用 `imagePrerequisitesVerified: true` 记录使用者确认，该字段不能绕过已知的网络、架构或硬件限制。见 [已实现的前置条件 #52](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/52)；镜像解析成功不代表节点已成功注册。

## 自定义镜像配置

```yaml
spec:
  imageCompatibilityPolicy: Custom
  imagePrerequisitesVerified: true # 完成下述验证后再确认
  imageSelectorTerms:
    - id: m-your-verified-custom-image
```

Custom 只接受 ECS 返回的 self/others 镜像，并核对 Linux、架构和可启动状态；不能绕过公共镜像的 ACK 校验，也不支持 name/family/tags 批量发现自定义镜像。填写确认字段前，验证镜像来源、Swap/系统设置、容器 runtime、ACK bootstrap、驱动及所需插件；这不代表 provider 已认证镜像。

## 模板与报价边界

原生模板的版本和磁盘写入 status，RunInstances 和 bootstrap 使用同一快照；不支持 NodeClass 磁盘 override。模板中的预配置磁盘 IOPS 或 bursting 无法完整报价时，`status.launchTemplate.pricing.unsupportedReason` 给出原因，该配置不参与自动供给。

按量和 Spot 使用 ECS 小时报价，包括实际镜像和磁盘；缺失、失败、过期或正在刷新的报价令 offering 暂不可用，保留规格记录用于 drift 判断。报价使用同一账号币种 CNY 或 USD，不猜测汇率；市场镜像还要求完整的同币种许可报价。按量报价有效期 5 分钟，Spot 1 分钟，失败缓存 30 秒；刷新并发和频率有上限，冷启动期间逐步开放有效报价。此价格不是最终账单预测，不包括流量、快照等后续用量费用。详见 [Provider 报价与生命周期行为](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/PROVIDER_BEHAVIOR.md)。

权限、ContainerOS 示例及详细行为见 [ACK_IMAGE_SUPPORT.md](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/ACK_IMAGE_SUPPORT.md)；版本证据见 [KUBERNETES_SUPPORT.md](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/KUBERNETES_SUPPORT.md)。继续阅读[核心功能](../features/index.md)或[状态与路线图](../status/index.md)。
