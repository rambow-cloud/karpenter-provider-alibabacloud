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

## 原生 ACK 节点池与资源配置 {#ack-node-resources}

本节描述 **dev 分支** 的 [资源计算修复 #16](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/16) 和 [网络发现修复 #76](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/76)。部署时选择同一 dev 提交的 controller、Chart 和 CRD；合入 dev 不代表 main 已发布。

`spec.ackNodePoolID` 选择一个已有、active 的普通 ACK ESS/containerd 节点池。省略时必须有且只有一个默认池。controller 需要 `cs:DescribeClusterNodePools` 和 `cs:DescribeClusterNodePoolDetail`，把实际池 ID 和 kubelet 设置写入 `status.ackNodePool`，引导脚本固定该 ID。创建前再次读取池；配置改变后等待 NodeClass status 刷新，不修改原生池。

**不要填写 `spec.kubelet`。** ACK attach script 不应用该字段，因此 CRD 和运行时都会拒绝，包括 maxPods、podsPerCore 和预留设置。请在 ACK 原生节点池配置受支持的 kubelet 参数。API 未提供 podsPerCore，不能据此声明节点容量。含 native userdata、auto mode、CPU/内存 manager 或 soft eviction overrides 的池暂不支持。

| 资源 | dev 的计算边界 |
| --- | --- |
| Pod | 原生 maxPods 与网络上限取较小值；Flannel 要求实际有效 CIDR mask，最大 256；Terway 要求已知 ECS ENI/IP 数量，保守地不扩大 trunk 容量 |
| CPU / 内存 | ACK 1.28+ 预留规则与原生 kube/system reservation；默认内存预留按网络与原生上限的较大值计算，避免预留不足 |
| VM / eviction | VM 内存损耗只扣减一次 Capacity；eviction 单独扣减，ACK 默认 memory.available 为 300Mi；自定义 hard eviction 必须显式包含该信号 |
| ephemeral-storage | 只有匹配镜像与资源配置的实测 nodefs 才参与新节点调度；不把系统盘与独立数据盘大小相加 |
| 注册与恢复 | 首次 ECS 创建保存不可变资源快照；Create/Get/List 和恢复共用它。节点注册后核对 NodeClaim UID 与 ProviderID，再使用 kubelet 的真实报告 |

启动时必须发现唯一 active 的已知 Terway/Flannel addon；API 错误、缺失/冲突元数据、未知 IP stack 或错误配置阻止初始化。Terway 未知 ENI 元数据不会获得候选；exclusive ENI 和非默认 pod-density-mode 暂不支持。保守上限可能低于节点实际容量，真实网络行为仍待 ACK 验收。

## 可选：校准临时存储 {#nodefs-calibration}

不设置 `nodeFilesystem` 时，普通 CPU/内存工作负载可继续使用；请求 ephemeral-storage 的 Pod 不会获得未测量的新节点候选。若需要该能力，先用匹配镜像、磁盘布局、原生节点池和自定义 userdata 的独立校准节点读取实际资源。修改这些设置后必须重新校准。

1. 创建不含 `nodeFilesystem` 的 ECSNodeClass，等待资源解析，并读取 `status.resourceConfigurationHash` 与实际镜像 ID。
2. 在由相同启动配置创建的校准节点读取 kubelet 的 nodefs Capacity/Allocatable；不是读取 ECS 云盘标称大小。
3. 把两个实测值、精确镜像 ID 和哈希写入该 NodeClass。下方值仅演示格式，必须替换。

```sh
kubectl get ecsnodeclass default -o jsonpath='{.status.resourceConfigurationHash}{"\n"}'
kubectl get node YOUR_CALIBRATION_NODE -o jsonpath='{.status.capacity.ephemeral-storage}{"\n"}{.status.allocatable.ephemeral-storage}{"\n"}'
```

```yaml
spec:
  ackNodePoolID: np-YOUR_EXISTING_ACK_POOL
  nodeFilesystem:
    configurationHash: REPLACE_WITH_ACTUAL_64_CHARACTER_HASH
    imageID: REPLACE_WITH_EXACT_MEASURED_IMAGE_ID
    capacity: 37Gi     # 替换为 kubelet 实测 Capacity
    allocatable: 32Gi  # 替换为 kubelet 实测 Allocatable
```

哈希覆盖有效磁盘配置、原生池 kubelet 设置与 userdata，镜像另外精确匹配。Capacity 必须为正，Allocatable 不大于 Capacity，且符合已知预留/eviction；容量不得超过任一启动云盘大小。多盘累加、未验证的 RAID 或独立 imagefs 布局不能通过此例推算。ContainerOS 仍需符合其单块全新持久数据盘要求。

此功能的公式、API 请求和恢复路径已有离线证据；**目标 ACK 节点的实际资源校准及一致性对比仍属于 #5/#23 的发布验收**，本次文档更新未运行云端测试。详见 [dev 资源模型与限制](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/dev/PROVIDER_BEHAVIOR.md#ack-节点资源计算) 和 [ACK 官方预留策略](https://help.aliyun.com/en/ack/ack-managed-and-ack-dedicated/user-guide/resource-reservation-policy)。
