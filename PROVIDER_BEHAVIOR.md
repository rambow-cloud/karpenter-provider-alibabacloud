# 节点漂移、缺货反馈与 E2E 镜像发现

## 节点漂移

ECSNodeClass controller 与 CloudProvider 共享 `ECSNodeClass.Hash()`，使用版本 `v2`。全新部署使用该版本；当前范围不包含旧哈希迁移。

镜像、交换机与安全组 selector 的原文、顺序和重复项不参与静态哈希。修改 selector 后，当前节点的镜像和交换机仍在解析集合中、安全组集合仍相同时，不会仅因 selector 改写报告漂移。节点使用的资源不再被允许时，分别报告 `ImageDrift`、`VSwitchDrift` 或 `SecurityGroupDrift`。出现更新的镜像不会自动淘汰仍在允许集合中的旧镜像。当前 generation 尚未 Ready 时暂停漂移判断，避免使用未完成的解析结果。

启动脚本、集群身份及 API endpoint、RAM role、kubelet、标签、metadata、Spot 配置、LaunchTemplate ID/version 与磁盘配置等静态字段变化仍会触发 `NodeClassDrift`。磁盘使用创建路径的默认值与校验；数据盘顺序和 clusterDNS 顺序保留。容量预留 selector 暂无动态 membership drift 检查，仍按排序去重的 OR 集合参与静态哈希。可变 LaunchTemplate 的实际数据盘传播由 #51 单独跟踪。

参考 [AWS v1.14.1 静态 hash 与 selector 忽略策略](https://github.com/aws/karpenter-provider-aws/blob/v1.14.1/pkg/apis/v1/ec2nodeclass.go)。

## 缺货反馈

实际 RunInstances 返回结构化 ECS 错误码 `OperationDenied.NoStock`、`Zone.NotOnSale` 或 `InvalidResourceType.NotSupported` 时，将“实例规格＋可用区＋容量类型”标记为暂不可用。缓存由 operator 创建并注入 CloudProvider，TTL 使用现有 `--unavailable-offering-cache-ttl` 配置，默认 5 分钟。调度读取该状态；下一次创建也跳过缓存中的组合，TTL 到期后允许再次尝试。

缓存只改变 offering 的 `Available`，不删除 offering 或实例规格，避免已有节点因临时缺货被错误标记为漂移。不同规格、可用区、Spot/按量容量相互隔离。

`InvalidVSwitchId.IpNotEnough` 仅让当前创建尝试切换交换机，不会禁用整个可用区。权限、参数、限流、查询失败及超时不会写入规格缺货缓存；普通错误文本中提及缺货码也不作为缓存证据。缓存是内存状态，controller 重启后重新学习库存失败。

参考 [AWS v1.14.1 创建失败反馈](https://github.com/aws/karpenter-provider-aws/blob/v1.14.1/pkg/providers/instance/instance.go)。

## ACK E2E worker 镜像发现

ackctl 按发现到的 worker **精确 ImageId** 查询，公有 `.vhd`/`.qcow2`、自定义 `m-*` 和共享镜像一律使用 ID。通过 worker 的实例规格查询实际架构，确认镜像架构一致，并要求 Linux 和 Available。缺少镜像、未知架构、架构不匹配或 API 错误时返回诊断，禁止静默切换名称或默认 x64 family。

查询复用生产 ECS adapter 的分页、context 取消及 `ShowExpired=true` 策略，保留 #29 已验证的 Lifsea 精确 pin 查询行为。发现镜像不等于完成 ACK 启动兼容认证；生产 provider 仍执行 [ACK 镜像兼容校验](ACK_IMAGE_SUPPORT.md)。该发现路径供 #23 最小 smoke 入口使用；真实 ACK 生命周期验证仍按 #5/#23 保留为发布验收项。
