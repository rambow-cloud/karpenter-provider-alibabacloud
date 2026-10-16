# 节点漂移、缺货反馈与 E2E 镜像发现

## ECS 创建与恢复

创建前将一次启动的参数摘要、ClientToken 和静态配置哈希持久化到 NodeClaim annotation；不保存启动脚本、密钥或凭据。Token 绑定 NodeClaim UID、尝试编号与参数摘要，首次 RunInstances 明确请求一台按量付费实例。相同参数重试复用 token；结构化缺货或交换机 IP 不足响应确认未创建后，才允许 fallback 使用新参数和新 token。

成功响应的实例 ID 在查询详情前保存。controller 重启、创建后查询失败或响应丢失时，使用记录的 ID，或区域内 managed-by、cluster-id、NodeClaim 名称和 UID 四个标签共同查找原实例。所有权标签优先随首次创建发送，避免大量用户标签将它们挤到后续 TagResources。查询失败、不完整分页、所有权不符或多个匹配实例都停止恢复，禁止把错误当作“实例不存在”继续创建。

结果不明确且暂时查询不到实例时，仅允许相同参数和 token 重试。配置、候选顺序或动态生成的启动脚本变化时会拒绝再次创建，直到原实例可以恢复；这是避免重复付费实例的保守行为。真实 ECS 的一致性、ClientToken 服务端行为和重启恢复仍由 #5/#23 验收。参考 [ECS 官方幂等规则](https://www.alibabacloud.com/help/en/ecs/developer-reference/how-to-ensure-idempotence)。

## 节点漂移

ECSNodeClass controller 与 CloudProvider 共享 `ECSNodeClass.Hash()`，使用版本 `v4`。全新部署使用该版本；当前范围不包含旧哈希迁移。

镜像、交换机与安全组 selector 的原文、顺序和重复项不参与静态哈希。修改 selector 后，当前节点的镜像和交换机仍在解析集合中、安全组集合仍相同时，不会仅因 selector 改写报告漂移。节点使用的资源不再被允许时，分别报告 `ImageDrift`、`VSwitchDrift` 或 `SecurityGroupDrift`。出现更新的镜像不会自动淘汰仍在允许集合中的旧镜像。当前 generation 尚未 Ready 时暂停漂移判断，避免使用未完成的解析结果。

动态漂移绕过通用实例缓存，按精确实例 ID 和区域重新读取 ECS，并校验响应数量、身份及必需字段。不完整响应返回错误，不能当作不存在；未观察到安全组字段或交换机信息时延后判断，不报告资源漂移。不完整网络快照不写入 Get 缓存；明确返回的空安全组集合与字段缺失保持区别，缺失或额外的实际安全组仍会触发漂移。静态配置哈希变化无需等待网络信息。#100 的本地回归覆盖旧缓存到新云响应和缺失字段恢复，但此前真实 ACK 的瞬时误漂移根因及修复效果仍须云端复验。

debug 日志 `Evaluating fresh ECS drift observation` 记录带时间戳的 claim 名称/UID、实例 ID/区域/状态、实际与 NodeClass 已解析安全组 ID、字段是否已观察到及交换机 ID，供后续有界云端复验关联；不记录完整云响应、userdata 或凭据。

启动脚本、集群身份及 API endpoint、RAM role、kubelet、标签、metadata、Spot 配置、LaunchTemplate ID/具体 version 与磁盘配置等静态字段变化仍会触发 `NodeClassDrift`。磁盘使用创建路径的默认值与校验；数据盘顺序和 clusterDNS 顺序保留。容量预留 selector 暂无动态 membership drift 检查，仍按排序去重的 OR 集合参与静态哈希。

## LaunchTemplate 磁盘与版本

模板模式与 selector 模式保持互斥，不支持 NodeClass 磁盘 override。controller 请求模板详细信息，验证返回的模板 ID、唯一数字版本和磁盘配置，并将启动所需的磁盘快照写入 `status.launchTemplate`；不写入模板 userdata、密码或凭据。显式版本必须与返回值一致；未指定版本时解析默认版本，后续 RunInstances 固定该数字版本，避免默认版本在解析与创建之间切换。默认版本或磁盘变化经 status 重新解析后参与哈希和漂移判断。

系统盘未定义的字段继续使用 provider 的既有默认值（40 GiB cloud_essd、ESSD PL0）；定义的数据盘按原顺序传入，保留 category、size、device、performanceLevel、encrypted、snapshotId 和 deleteWithInstance。RunInstances 同时指定原模板和数字版本，由 ECS 保留模板其他参数，provider 覆盖其已解析镜像/网络/磁盘、生成的 ACK bootstrap、NodePool capacity type 和所有权标签；NodeClass 明确指定的 role/metadata 配置优先。模板固定实例规格时，与 NodePool 约束取交集，不能改用其他规格。

ContainerOS 的启动校验与 bootstrap 使用同一规范化磁盘快照；只支持一块全新的持久云数据盘，通过 ACK `format_disk=true` 初始化并挂载容器和 kubelet 目录。缺盘、多盘、ephemeral/实例本地盘策略、快照盘或无效配置在创建前拒绝；普通 Linux 不自动格式化数据盘。脚本缓存额外按 format_disk 分区，避免误用其他磁盘模式的脚本。没有自动加盘或更改磁盘数量；保留磁盘的 deleteWithInstance=false 配置仍按用户意图保留。

此实现依据 [ECS 模板版本 API](https://www.alibabacloud.com/help/en/ecs/developer-reference/api-ecs-2014-05-26-describelaunchtemplateversions) 和 [ACK attach script API](https://www.alibabacloud.com/help/en/ack/ack-managed-and-ack-dedicated/developer-reference/api-cs-2015-12-15-describeclusterattachscripts)。实际挂载、ECS 模板参数继承和 ACK 注册仍由 #5/#23 验收；OS/硬件前置条件由 #52 跟踪。

参考 [AWS v1.14.1 静态 hash 与 selector 忽略策略](https://github.com/aws/karpenter-provider-aws/blob/v1.14.1/pkg/apis/v1/ec2nodeclass.go)。

## Offering 报价与成本排序

调度和实际创建共用 ECS `DescribePrice` 的一台实例、一小时成交价。查询明确指定地域、zone、实例规格、实际选择的镜像、VPC、规范化系统盘和数据盘，以及 `NoSpot` 或实际 Spot strategy；Spot 不再使用固定折扣估算。原生模板的公网计费方式、带宽、I/O 优化和 Spot 保留时长来自固定数字版本的 status 快照。市场镜像单独返回的许可成交价纳入合计，并要求币种一致。

只接受完整、有限且非负的 CNY 或 USD 报价。一个 provider 进程使用首个有效报价的币种，其他币种不参与比较，不猜测汇率。缺少价格/币种、API 失败、缓存过期或刷新中，都令该 offering `Available=false`；保留其规格、zone 和容量类型，避免报价暂不可用被当作规格消失。此时 core 接口中的零值只是不可用报价的占位值；API 明确返回的零价优惠与缺失元数据分开处理。报价恢复后才可参与新节点调度。

报价按完整查询配置隔离，按量有效期 5 分钟，Spot 1 分钟；失败缓存 30 秒，不复用过期报价。首次访问异步刷新，最多 4 个在途请求、每秒 5 个请求（初始 burst 4），每个请求含限流等待最多 30 秒；相同查询合并刷新，缓存最多 4096 项。冷启动或大目录刷新期间只开放已取得有效报价的组合，逐步扩大可调度集合，不阻塞一次调度去同步查询全部 SKU。

创建仅使用当前有库存、未被缺货缓存禁用、镜像兼容且报价有效的组合。两个容量类型都允许时遵循 Karpenter 的 Spot 优先语义，再在该容量类型内按小时报价升序选规格和 zone；同价时优先 IP 余量较多的交换机。缺货/IP 不足才回退到后续候选，权限和参数错误立即返回。报价变化可能改变候选顺序；如果前一次创建结果不明确，既有持久化启动记录仍禁止以不同参数再次创建。

报价是节点配置的小时基准成本，不是最终账单预测；流量、快照、KMS、CPU burst 等用量费用，以及删除后保留磁盘的后续费用均不据此估算。模板中无法表示的预配置 IOPS 或磁盘 bursting 会写入 `status.launchTemplate.pricing.unsupportedReason`，禁止漏价后自动选用；API 报告的磁盘总 IOPS 不作为额外预配置 IOPS。市场镜像保留 ECS 来源，缺少独立许可报价时不可调度。报价 API 权限为已有的 `ecs:DescribePrice`。离线测试执行 core 的严格替换价格筛选，并验证实际 RunInstances 选择；真实账号报价、账单与非空节点 consolidation 保留为 #5/#23 验收。

参考 [阿里云 DescribePrice](https://help.aliyun.com/zh/ecs/developer-reference/api-ecs-2014-05-26-describeprice)、[AWS offering 报价可用性](https://github.com/aws/karpenter-provider-aws/blob/v1.14.1/pkg/providers/instancetype/offering/base_resolver.go) 和 [Karpenter 替换价格筛选](https://github.com/kubernetes-sigs/karpenter/blob/v1.14.1/pkg/controllers/provisioning/scheduling/nodeclaim.go)。

## ACK 网络发现与 Pod 上限

启动时使用调用者 context 查询已安装 addon，要求恰好一个已知且 active 的 Terway/Flannel 网络，再读取对应配置和集群详情。API 错误、缺失/冲突/升级中的网络、错误的配置身份、未知 IP stack 或无效 Terway JSON 都使初始化失败，不再静默采用默认网络配置。已有 `cs:ListClusterAddonInstances` 权限也用于此发现路径。

Flannel 使用 ACK 实际返回的 IPv4 node CIDR mask 计算每节点地址上限，并受官方 256 Pod 配额约束；查询失败或 mask 缺失/无效时不会假设 `/24`。Terway 允许已知配置中省略 ENITrunking override（使用 addon 默认 false），但拒绝缺失配置、null/string 布尔值和格式错误。网络上限不等于完整 kubelet 可分配资源；预留与文件系统模型由 #16 跟踪，真实网络行为仍由 #5/#23 验收。

参考 [ACK 配额](https://help.aliyun.com/en/ack/product-overview/limits) 和 [Terway/Flannel 上限区别](https://help.aliyun.com/en/ack/ack-managed-and-ack-dedicated/user-guide/comparison-between-terway-and-flannel)。

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

## ACK 节点资源计算

通过 `spec.ackNodePoolID` 选择现有 ACK 原生节点池；省略时要求唯一默认池。仅接受 active 的普通 ESS/containerd 节点池，并读取 `cs:DescribeClusterNodePools` / `cs:DescribeClusterNodePoolDetail`。将池 ID 和资源相关 kubelet 设置写入 `status.ackNodePool`；attach script 固定该池 ID，脚本缓存按池和配置哈希隔离。创建前再次读取，配置改变时等待 status 刷新，不修改原生节点池。缺失、权限错误、身份不符、native userdata、auto mode、CPU/内存 manager 或 soft eviction overrides 均明确拒绝。

ACK 接管节点可能增加原生期望数量，即使关闭自动伸缩也会补节点（#101）。终止先核验精确的实例归属，持久化移除意图，通过 `cs:RemoveNodePoolNodes` 移除成员并调整期望数量，等待任务成功和成员消失后再释放 ECS。新增权限包括集群范围的 `cs:DescribeClusterNodes` / `cs:RemoveNodePoolNodes` 及 `cs:DescribeTaskInfo`（仅支持 `Resource: "*"`）。未知请求结果不自动重试扣减，参见 [恢复说明](test/hack/e2e/smoke/README.md)。离线回归不代表真实 ACK 验收；E2E 保持手动关闭，云端复验需另行授权。

ACK attach script 不能应用 `spec.kubelet`；admission 和启动路径明确拒绝该字段，包括 maxPods、podsPerCore 和预留配置。请在所选原生节点池配置 kubelet。原生 API 不提供 podsPerCore，不能假设已生效。Pod 候选上限取原生 maxPods 与已发现网络上限的较小值：Flannel 使用实际 mask/256 配额；Terway 使用 ECS 的已知 ENI/IP 数量，保守地不扩大 trunk 容量。不支持 exclusive ENI 或非默认 pod-density-mode；未知 ENI 元数据不会获得候选。Terway maxPods 的 host-network 语义可能使此保守上限低于实际节点容量，真实值以注册节点为准。

VM 内存损耗通过百分比和固定量的较大值扣减一次 Capacity。ACK 1.28+ CPU 分段规则、内存 `min(11×Pod上限+255 MiB, 可用内存×25%)` 分别计算 kube/system reservation，按 50/50 分配；采用网络和原生上限的较大值计算默认内存预留，避免保守 Pod 上限导致预留不足。显式原生 kubeReserved/systemReserved 按字段覆盖；eviction 单独扣减，ACK 默认 memory.available 为 300Mi。自定义 evictionHard 必须显式包含 memory.available，支持数量/百分比，拒绝未知信号和无效值；不假设 ACK 未公开的默认合并设置。调度和创建共用此模型，不再固定均分总 overhead。

ECS 磁盘大小不能证明 kubelet nodefs 的挂载、分区和可分配容量，未测量时不声明 ephemeral-storage，临时存储请求不会匹配。可在相同镜像和启动配置的校准节点读取 kubelet Capacity/Allocatable 后，填写 `spec.nodeFilesystem` 的 `imageID`、`capacity`、`allocatable` 和 `configurationHash`（来自 `status.resourceConfigurationHash`）。配置哈希覆盖有效磁盘、原生池和自定义 userdata；配置或镜像改变后测量失效。容量不得超过任一启动云盘大小，不累加独立磁盘；allocatable 必须符合已知预留/eviction。未验证的 RAID/独立 imagefs 布局不通过此模型推算。

创建的 Capacity/Allocatable 以紧凑快照保存在首次 RunInstances 的保留 ECS tag `karpenter.alibabacloud.com/resource-model`，纳入持久化幂等参数摘要。Create/Get/List 和失败恢复读取同一快照，不随 NodeClass 后续变化重新估算。节点注册后，核对 NodeClaim UID 和 Node ProviderID，再采用 kubelet 报告；查询错误、身份冲突或非法资源值明确返回错误。无快照的孤立实例保留未知资源，不伪造容量。静态哈希版本为 v4，包含原生池配置；当前按全新部署处理。

离线回归覆盖公式、网络/原生上限、配置拒绝、测量绑定、SDK 参数、脚本缓存、首次 tag 与恢复一致性。目标 ACK 实际校准和节点资源对比仍保留为 #5/#23 验收。

参考 [ACK 资源预留策略](https://help.aliyun.com/en/ack/ack-managed-and-ack-dedicated/user-guide/resource-reservation-policy)、[ACK 原生 kubelet 配置](https://help.aliyun.com/en/ack/ack-managed-and-ack-dedicated/user-guide/customize-the-kubelet-configurations-of-a-node-pool) 和 [AWS v1.14.1 资源模型](https://github.com/aws/karpenter-provider-aws/blob/v1.14.1/pkg/providers/instancetype/types.go)。
