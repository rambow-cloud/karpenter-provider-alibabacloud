# 节点漂移、缺货反馈与 E2E 镜像发现

## ECS 创建与恢复

创建前将一次启动的参数摘要、ClientToken 和静态配置哈希持久化到 NodeClaim annotation；不保存启动脚本、密钥或凭据。Token 绑定 NodeClaim UID、尝试编号与参数摘要，首次 RunInstances 明确请求一台按量付费实例。相同参数重试复用 token；结构化缺货或交换机 IP 不足响应确认未创建后，才允许 fallback 使用新参数和新 token。

成功响应的实例 ID 在查询详情前保存。controller 重启、创建后查询失败或响应丢失时，使用记录的 ID，或区域内 managed-by、cluster-id、NodeClaim 名称和 UID 四个标签共同查找原实例。所有权标签优先随首次创建发送，避免大量用户标签将它们挤到后续 TagResources。查询失败、不完整分页、所有权不符或多个匹配实例都停止恢复，禁止把错误当作“实例不存在”继续创建。

结果不明确且暂时查询不到实例时，仅允许相同参数和 token 重试。配置、候选顺序或动态生成的启动脚本变化时会拒绝再次创建，直到原实例可以恢复；这是避免重复付费实例的保守行为。真实 ECS 的一致性、ClientToken 服务端行为和重启恢复仍由 #5/#23 验收。参考 [ECS 官方幂等规则](https://www.alibabacloud.com/help/en/ecs/developer-reference/how-to-ensure-idempotence)。

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

查询复用生产 ECS adapter 的分页、context 取消及精确 ID 的 `ShowExpired=true` 策略，保留 #29 已验证的 Lifsea 精确 pin 查询行为。发现镜像不等于完成 ACK 启动兼容认证；生产 provider 仍执行 [ACK 镜像兼容校验](ACK_IMAGE_SUPPORT.md)。该发现路径供 #23 最小 smoke 入口使用；真实 ACK 生命周期验证仍按 #5/#23 保留为发布验收项。

## 镜像查询策略

family、名称和标签查询使用 `ShowExpired=false`，避免自动发现扩展到过期订阅镜像。用户精确指定 ImageId 或 ACK 兼容清单指定 ID 时使用 `ShowExpired=true` 保留可见性；该开关只决定发现范围，不授权启动。所有请求都附带 `Status=Available`、`Usable=true`，并再次检查响应的状态和可用性；缺少这些字段的镜像不参与选择。marketplace 镜像还要求 `IsSubscribed=true`，否则不代替用户接受服务条款；私有和共享镜像不使用此项限制。`IsSubscribed` 表示条款接受状态，不等同于使用期限未过期或没有费用。

SDK v5.1.0 的生成模型缺少 `Usable` 请求及响应字段。adapter 通过同一个 SDK 的签名 RPC transport 查询，并在本地扩展响应模型保留字段；没有引入另一套凭据或 HTTP 客户端。查询失败、缺少 TotalCount 或中途空页会返回错误，不能缓存为完整成功结果。原有分页、取消和 HTTP 超时保持有效。

2026-10-11 的真实只读查询证据来自 cn-hangzhou 的一个 OAuth profile：`lifsea_3_x64_5G_alibase_20260519.qcow2` 在省略/false 时返回 0，true 时返回 1，且额外过滤 Available/Usable 仍返回 1；`aliyun_3_x64_20G_container_optimized_alibase_20260828.vhd` 三种设置均返回 1。Lifsea family `acs:lifsea_os_3_x64` 三种设置均返回 0，因此不能用 family 替代该精确 pin。成功 Lifsea Available/Usable 查询 RequestId 为 `01A129B7-C202-58FC-B7FC-1714ACCF8FFE`；完整脱敏记录见 [issue #29](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/29)。

上述证据不代表所有 ContainerOS、区域或账号的行为。该 profile 未取得已订阅、已过期或 Usable=false 的真实镜像样本；这些过滤分支使用本地 SDK HTTP 响应测试，不能称为云端实测。真实订阅期限与运行权限仍须在目标账号核实，节点启动与加入 ACK 仍由 #5/#23 验收。官方字段定义见 [DescribeImages](https://www.alibabacloud.com/help/en/ecs/developer-reference/api-ecs-2014-05-26-describeimages)。
