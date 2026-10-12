---
title: 快速开始
description: 在 Kubernetes 1.34+ 的阿里云 ACK 集群全新安装 Karpenter，准备匹配镜像、RAM 凭证、Helm 配置、ECSNodeClass 和 NodePool，并检查节点供给状态。
---

# 快速开始

本文面向在已有 ACK 集群中**全新安装本 provider**。当前 Chart 为 **0.3.0-dev**，尚需真实 ACK 生命周期验收；先在隔离测试环境试用。[支持版本与镜像](../support/index.md)列出版本和操作系统边界。

## 1. 准备集群与权限

- ACK Kubernetes 版本至少 1.34，`kubectl` 可访问集群，准备 Helm 3。
- 确认 ACK 集群 ID、区域、VPC、交换机和安全组；节点网络须能访问 API Server。
- controller 需要 ECS、VPC、RAM、CS 的相关权限，公共镜像查询包括 `cs:DescribeClusterDetail`、`cs:DescribeKubernetesVersionMetadata`、`cs:ListClusterAddonInstances` 和 `ecs:DescribeImages`；报价需要 `ecs:DescribePrice`，dev 原生节点池解析还需 `cs:DescribeClusterNodePools` 和 `cs:DescribeClusterNodePoolDetail`。
- 为 ECS 节点准备 RAM 角色，与 controller 访问云 API 的身份分别配置。
- controller 固定在已有真实节点上运行，不使用它自身供给的节点作为首次启动依赖。

建议使用 RRSA，按仓库 [RRSA 配置与 RAM 权限](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/README.md#rrsa-使用指导)准备集群、角色信任和 controller 环境变量。若测试环境使用 AccessKey，先创建 `karpenter` namespace，并在其中准备 Kubernetes Secret，键为 `access_key_id` 和 `access_key_secret`；不将凭据写入 Git。以下 values 示例使用这个已有 Secret，本机 CLI profile 不会自动传入 Pod。

## 2. 准备匹配的镜像与安装配置

```sh
git clone https://github.com/rambow-cloud/karpenter-provider-alibabacloud.git
cd karpenter-provider-alibabacloud
```

选择待测提交，从该提交构建并推送 controller 镜像到你的镜像仓库。构建要求、feature gates 和 CRD 说明见[源码安装指南](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/UPGRADING.md)。镜像、Chart 与 CRD 使用同一提交，不直接采用历史发布镜像。

建立 `your-values.yaml`，替换所有占位符；区域和集群 ID 使用目标 ACK 的实际值。RRSA 用户应按上述配置指南改用角色环境变量，不同时设置 AccessKey Secret。

```yaml
settings:
  clusterID: YOUR_ACK_CLUSTER_ID
  region: cn-hangzhou
controller:
  image:
    repository: YOUR_REGISTRY/YOUR_PROVIDER
    tag: YOUR_COMMIT_TAG
  credentialsSecretName: karpenter-credentials
  nodeSelector:
    kubernetes.io/hostname: YOUR_EXISTING_NODE_NAME
```

controller 的 RAM 策略必须具备实际操作所需权限；上面的镜像查询权限不是完整授权策略。

## 3. 全新安装 CRD 与 controller

```sh
kubectl apply --server-side --field-manager=karpenter-install -f charts/karpenter/crds/
helm install karpenter ./charts/karpenter \
  --namespace karpenter --create-namespace -f your-values.yaml
kubectl rollout status deployment/karpenter -n karpenter
```

使用私有镜像仓库时，配置 Chart 的 `imagePullSecrets`。controller 启动成功后，再创建 NodeClass 和 NodePool。

## 4. 定义节点配置与容量边界

将下面内容保存为 `node-config.yaml`。替换集群 ID、交换机、安全组与节点 RAM 角色。镜像名称模式匹配目标 ACK 清单内的阿里云 Linux 3 amd64 镜像；如果目标环境没有兼容候选，NodeClass 会解析失败，需要根据[镜像支持策略](../support/index.md)调整 selector。


选择已有 active 的普通 ACK 原生节点池。下面使用显式 `ackNodePoolID`；若省略，必须有唯一默认池。不要添加 `spec.kubelet`，请在原生池配置 kubelet；需要 ephemeral-storage 时先完成[资源校准](../support/index.md#nodefs-calibration)。这些资源功能目前在 **dev**，请构建同一 dev 提交的镜像、Chart 和 CRD。

```yaml
apiVersion: karpenter.alibabacloud.com/v1alpha1
kind: ECSNodeClass
metadata:
  name: default
spec:
  clusterID: YOUR_ACK_CLUSTER_ID
  ackNodePoolID: np-YOUR_EXISTING_ACK_POOL
  vSwitchSelectorTerms:
    - id: vsw-YOUR_VSWITCH_ID
  securityGroupSelectorTerms:
    - id: sg-YOUR_SECURITY_GROUP_ID
  imageSelectorTerms:
    - name: aliyun_3_x64*
      imageOwnerAlias: system
  role: YOUR_ECS_NODE_RAM_ROLE
---
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: default
spec:
  limits:
    cpu: "10"
  template:
    spec:
      nodeClassRef:
        group: karpenter.alibabacloud.com
        kind: ECSNodeClass
        name: default
      requirements:
        - key: kubernetes.io/arch
          operator: In
          values: [amd64]
        - key: karpenter.sh/capacity-type
          operator: In
          values: [on-demand]
```

```sh
kubectl apply -f node-config.yaml
kubectl get ecsnodeclass default -o yaml
kubectl get nodepool default
```

NodeClass 必须 `Ready=True`，且 `observedGeneration` 与当前 `metadata.generation` 一致。`limits.cpu` 控制此 NodePool 的 CPU 容量范围，不是云费用硬预算。

## 5. 验证供给与清理

在测试环境提交一个因容量不足而待调度的工作负载，再观察：

```sh
kubectl get pods -A
kubectl get nodeclaims
kubectl get nodes
kubectl logs -n karpenter deployment/karpenter --tail=100
```

确认 NodeClaim、节点注册、Pod Ready 和工作负载删除后的节点回收，同时检查 ECS 云资源清理。创建工作负载可能产生付费 ECS 资源；仅 controller 安装成功不代表生命周期已验收。完整、可重复的基础 smoke 由 [Issue #23](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/23)跟踪，本次网站更新没有执行云端测试。

继续阅读[核心功能](../features/index.md)、[状态与路线图](../status/index.md)，或通过[参与贡献](../participate/index.md)提交脱敏问题。
