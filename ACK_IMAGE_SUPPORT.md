# ACK 镜像选择与操作系统支持边界

本 provider 的 ACK 路径目前支持 **Linux + containerd**。ACK 官方支持的操作系统范围大于本 provider 已实现和已验收的范围。镜像解析成功不等同于实际节点启动、注册、Pod Ready 或删除验收通过；真实 ACK E2E 仍由 #5/#23 跟踪。

## 默认公共镜像策略

`spec.imageCompatibilityPolicy` 未设置或为 `ACK` 时，controller 从目标集群获取 `CurrentVersion`、`ClusterType`、`Profile`，使用 provider 区域和 `containerd` 查询 `DescribeKubernetesVersionMetadata`。集群区域必须与 provider 区域一致。

以 ACK 返回的精确公共镜像 ID 查询 ECS，再检查镜像状态、Linux OSType 和架构是否与 ACK 元数据一致。family、name（支持 `*`、`?`）和 tags 在这些已批准的镜像上匹配，因此不会把 ECS 最新 family 镜像自动当作 ACK 支持镜像。多个 selector 是 OR，单个 selector 的条件是 AND。精确 ID 不被替换：不在当前清单或不可用时，NodeClass 不会 Ready，启动请求会被拒绝。

ACK 元数据失败、为空或缺少目标版本时，解析失败，不回退到 ECS 最新镜像。每次解析读取目标集群当前版本，成功的镜像清单最多缓存 5 分钟且只保留一个快照；集群版本/类型/场景变化会更新清单，失败不会缓存。创建前按原始 selector 重新解析，再与 status 的 ID 取交集：过滤失效的自动候选，保留仍然有效的镜像；尚未进入 status 的新镜像等待 NodeClass 重协调。用户显式 pin 和 LaunchTemplate 镜像仍严格校验，无法满足时拒绝启动。元数据缓存期间的云端状态变动仍可能在实际 ECS 创建时被拒绝。

family、name、tags 或 owner 自动发现多个候选时，每个候选分别校验启动条件。例如宽 `imageOwnerAlias: system` 同时匹配普通 Linux 和 ContainerOS，缺少 ContainerOS 数据盘只会过滤该候选，不阻断普通 Linux；全部候选都不适用才失败。显式 ID 不参与这种自动替代，包含一个无效显式 pin 的 selector 集合仍失败。

LaunchTemplate 的镜像也需要查询和校验，不能仅凭模板中的字符串 ID 标记 ImageResolved。

镜像结果保留 ECS `creationTime`，新建实例在已选择且架构兼容的候选中优先使用创建时间最新的版本；时间缺失/无效排在最后，同时间按 ID 升序稳定选择。多个 selector 是无序 OR，不表示用户指定的优先级。单个精确 ID 固定该镜像；多个 pin 或 pin 与动态 selector 混用时，按上述策略从其并集中选择，不引入集合外的镜像。已有节点的镜像仍在解析结果中时，不会仅因出现新版本触发 ImageDrift。

调度和创建都会排除没有已知兼容镜像架构的实例规格，不再把未知架构猜成 amd64 来授权创建，也不会将第一张镜像作为不匹配时的兜底。架构匹配不等同于 GPU、网络插件或其他硬件约束已通过，#52 仍独立跟踪这些前置条件。

## 自定义与共享镜像

自定义镜像不在 ACK 公共兼容清单中。经自行验证后，可以显式设置：

```yaml
spec:
  imageCompatibilityPolicy: Custom
  imageSelectorTerms:
    - id: m-your-verified-custom-image
```

此策略只允许精确 pin 的 `self` 或 `others` 镜像，并检查 ECS 返回的 Linux OSType、架构和 Available 状态。拒绝公共镜像及云市场镜像，不能用 Custom 绕过公共镜像清单；不支持 name/family/tag 的自定义镜像批量发现。

**Custom 不代表已通过 ACK 兼容认证。** 用户需要验证镜像基础 OS、ACK/Kubernetes 版本、runtime、架构、磁盘布局和 GPU 驱动，并确认实际节点启动与注册。未知 OS 会被拒绝；自定义镜像的发行版、派生来源和特殊启动要求不能仅凭 ECS OSType 确定。不要直接从仍在集群运行的节点制作镜像，禁用 Swap。详见[官方自定义镜像要求](https://help.aliyun.com/zh/ack/ack-managed-and-ack-dedicated/user-guide/create-custom-images)。

## ContainerOS

ACK 元数据中的 `ContainerOS`、`ContainerOS4` 和 `ContainerOSGPU` 采用受限的单数据盘配置。需要 **恰好一块有效的持久云数据盘**，不支持 ephemeral 数据盘或 `instanceStorePolicy`。缺盘、多盘或无效大小会使该候选不可用；显式 pin 或没有其他有效候选时解析和创建失败。controller 不自动增加收费云盘。

```yaml
spec:
  imageSelectorTerms:
    - id: lifsea_3_x64_5G_alibase_20260716.qcow2 # 示例；须在目标 ACK 清单中
  dataDisks:
    - category: cloud_essd
      size: 40
```

**目前拒绝 ContainerOS + LaunchTemplate**：现有启动路径没有将模板的数据盘配置传入 RunInstances，无法证明盘配置会生效。改用 selector 和上述显式数据盘。自定义派生镜像的特殊磁盘/初始化需求必须由用户验证。

ContainerOS 3.4 起只读系统盘需要数据盘，且不适用于带本地盘的实例规格。选择 GPU 变体还需要匹配 GPU 机型和驱动。镜像清单校验并不能替代这些硬件/运行验收。参见[更换操作系统](https://help.aliyun.com/zh/ack/ack-managed-and-ack-dedicated/user-guide/replace-the-operating-system)、[ContainerOS 说明](https://help.aliyun.com/zh/ack/ack-managed-and-ack-dedicated/user-guide/containeros-overview/)。

## Windows 与其他约束

本 provider 不支持 Windows bootstrap、Windows 调度与资源预留，明确拒绝 Windows 和未知 OSType。ACK 官方的 Windows 节点池能力不能视为本 provider 的支持承诺。

ACK 元数据不是全部部署约束的检查器。阿里云 Linux 4 的 Terway 版本、Ubuntu 的 CPFS/镜像加速/安全加固限制、ContainerOS 的本地盘限制及 GPU 驱动适配仍需按官方说明配置和验收。具体镜像以目标区域、ACK 类型、版本和 runtime 返回的清单为准，不把文档示例 ID 固定为通用最新版。参见[ACK 操作系统与限制](https://help.aliyun.com/zh/ack/ack-managed-and-ack-dedicated/user-guide/overview-of-os-images/)。

## 全新部署与权限

ACK bootstrap 在每次读取脚本缓存前查询当前集群上下文，按 cluster、region、type、profile、version、runtime 分区，成功脚本缓存最多 5 分钟。集群升级和 Profile 变化立即刷新；runtime 元数据必须提供精确 Kubernetes 版本的 containerd。查询失败不会缓存未经验证的 fallback 脚本，也不会用旧版本缓存掩盖失败。

生产 ECS/VPC/RAM/CS SDK 的连接和读超时各为 30 秒（配置字段单位毫秒，值为 `30000`），默认 HTTP 总超时为 60 秒。adapter 把调用者 context 绑定到实际 HTTP 请求，兼容清单缓存等待也可取消。SDK 自定义 HTTP client 保持原配置；SDK 内部凭证刷新没有 context 参数，取消无法中断这部分调用，也不能撤销云端已接收的请求。云端创建结果不确定时仍需要资源对账，真实节点验收由 #5/#23 跟踪。

全新安装时使用匹配的 CRD、Chart 与 controller；CRD 包含 spec 配置与 status 的 `osType`/`imageType`/`creationTime` 字段。RAM 策略需要只读 `cs:DescribeKubernetesVersionMetadata`，保留 `cs:DescribeClusterDetail`、`ecs:DescribeImages`。缺少权限会阻止公共镜像解析和启动。

公共镜像 pin 必须属于目标 ACK 清单，不会自动替换为其他镜像。自定义/共享镜像需要显式启用 Custom。创建前仍会按当前 selector 和启动条件重新验证镜像，防止解析后的云端状态变化导致错误启动。当前交付范围为全新部署，不要求旧 provider 的迁移或回滚验收。

## 取证

2026-10-11 本机 OAuth profile 的 cn-hangzhou 只读 API 查询显示，ACK 1.34.10-aliyun.1 的 Linux 3 容器优化镜像为 `20260720`，ECS family 则返回 `20260828`；Ubuntu 也存在类似差异。该证据证明需要 ACK 兼容清单，不能证明创建和加入节点已成功。Issue #47/#48/#49 记录修复；ShowExpired 的订阅/过期镜像调查仍留在 #29。
