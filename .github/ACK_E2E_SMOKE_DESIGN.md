# ACK 三节点、Spot 与双 OIDC E2E 设计

状态：2026-10-12 实现已加入双身份 primitives 和独立三节点 smoke，真实执行/验收仍待专用 ACK。维护者已确认：Karpenter 新建三个 worker（两个按量、一个 Spot），另保留固定系统节点；controller 使用 ACK RRSA，GitHub Actions runner 也使用 OIDC。操作入口、清理和证据限制见 test/hack/e2e/smoke/README.md。

## 验收目标与资源规模

复用专用测试 ACK，Kubernetes 实际版本 >=1.34，使用匹配的 controller、Chart、CRD、Karpenter v1.14.1。系统节点不计入新增三节点：若既有一个系统 worker，成功时总计四个 worker；ACK 托管控制平面不计数。

| 资源 | 数量 | 用途 |
| --- | --- | --- |
| 既有固定按量系统节点 | 至少 1 | controller、DNS 和测试诊断，不归本 run 删除 |
| Karpenter 按量节点 | 新建 2 | 两个测试 Pod，各占一个节点 |
| Karpenter Spot 节点 | 新建 1 | 一个测试 Pod，明确只允许 Spot |
| 测试 Pod | 3 | 验证实际创建、ACK 加入、网络和调度 |

新增三个节点必须全部由本次 Karpenter NodeClaim 创建，不能预建 ECS 或 ACK 扩容后冒充测试结果。所有 E2E（含 KWOK、Parity、ACK、矩阵及 cleanup）只能手动触发以降低费用；禁止 push、PR、schedule、workflow_run、repository_dispatch 自动入口。workflow_call 仅作手动入口内部复用，执行 job 再核对 workflow_dispatch；ACK 创建另要求 ACK_E2E_ENABLED=true，cleanup-only 不依赖创建开关。#89 跟踪该长期约束。此最小 smoke 不运行版本/区域矩阵。Spot 创建验收不等于 #20 的中断预告、drain 或抢占恢复验收。

## 双 OIDC 身份

两种 issuer、两个 OIDC provider、两个 RAM role，不能复用信任策略或把 runner 临时凭证注入 controller。

### Controller：ACK RRSA

- 复用已开启 RRSA 的测试集群。事先记录其真实 issuer URL 和 OIDC provider ARN，通常 provider 名为 `ack-rrsa-<cluster-id>`，不凭 cluster ID 拼造 ARN。
- 固定 controller namespace `karpenter`、ServiceAccount `karpenter`，信任 subject 精确为 `system:serviceaccount:karpenter:karpenter`。
- RAM 信任策略：Federated principal 为该 ACK OIDC provider，Action 按 RAM 官方格式为 `sts:AssumeRole`，`StringEquals` 限定 `oidc:iss`、`oidc:aud=sts.aliyuncs.com`、`oidc:sub`。实际换取凭证调用 STS `AssumeRoleWithOIDC`。
- 使用当前 Chart 的显式 projected ServiceAccount token 路径：audience `sts.aliyuncs.com`，expirationSeconds 3600，挂载路径 `/var/run/secrets/ack.alibabacloud.com/rrsa-tokens/token`。此用例不同时启用 webhook 自动注入，避免重复 volume/env。
- controller 设置 `ALIBABA_CLOUD_ROLE_ARN`、`ALIBABA_CLOUD_OIDC_PROVIDER_ARN`、`ALIBABA_CLOUD_OIDC_TOKEN_FILE`；`credentialsSecretName` 为空，Pod 中没有有效 AK/SK/SecurityToken、凭证 Secret 引用或替代凭证挂载。明确检验生成的 Pod spec，空默认 env 不视为凭证。
- controller 固定运行于系统节点，使用专用节点 label 的 nodeSelector，避免自举和后续释放时失去 controller。
- controller role 只授予当前 provider 实际 API 所需权限，按照 README 权限清单和目标配置逐项校对 ECS、VPC、ACK、RAM、定价等调用。节点的 ECS RAM role 与 controller 的 RRSA role 分开；所需 PassRole 限定为测试 worker role。创建/标签和删除权限须按各 API 支持的资源、请求标签与资源标签条件分别审核，不能套一个不适用的统一条件。
- 保存非敏感认证模式/角色身份、NodeClass Ready、真实 provisioning 证据；仅有初始化日志不足以证明 STS 认证成功。身份检查 Job 使用同一 SA/投影方式执行一次只读身份/API 检查，不输出 JWT 或临时凭证。另用未授权 SA 验证不能扮演 controller role。

### Runner：GitHub Actions OIDC

- 新增独立 `workflow_dispatch` smoke workflow，仅在 `ACK_E2E_ENABLED=true` 时分配云测试 job；job `environment: ack-e2e`，`permissions: contents: read, id-token: write`。
- RAM OIDC provider issuer 为 `https://token.actions.githubusercontent.com`；audience 显式选择 `sts.aliyuncs.com`，RAM provider、信任策略与 action 三者一致。
- 本仓库 REST 查询确认启用了 immutable subject（2026-10-12）：`use_default=true`、`use_immutable_subject=true`，prefix 为 `repo:rambow-cloud@333554680/karpenter-provider-alibabacloud@1412605061`。目标 environment subject 为 `repo:rambow-cloud@333554680/karpenter-provider-alibabacloud@1412605061:environment:ack-e2e`。实现时重读设置并核对实际脱敏 claim，不能使用不含 ID 的旧示例。
- RAM 信任同时限定 provider principal、issuer、audience 和精确 subject；GitHub environment 仅允许维护者选择的 dev/main，workflow 不接受 PR/fork 入口。environment subject 不包含 branch，不能另写不存在的 branch subject；分支约束由 environment deployment rules 完成。
- 使用阿里云官方 `aliyun/configure-aliyun-credentials-action`，实施时选择已审核版本并固定完整 commit SHA。STS duration 采用角色允许的 3600 秒；action 输出必须同时传递临时 AK、临时 SK、SecurityToken，全部仅存在 runner 环境中。
- runner role：获取目标 ACK 临时 kubeconfig、读取 ACK/网络/镜像/实例、管理隔离测试 Kubernetes 对象及清理本 run 资源。ACK 侧授予对应 Kubernetes RBAC；RAM 云权限不替代 Kubernetes RBAC。不授予创建/删除 ACK、网络、RAM role 或修改信任策略权限。
- 现有 ACK API endpoint 和访问规则由测试环境事先提供。执行前检验可达性，runner 不自动修改 API SLB ACL 或放开公网访问。若现有网络无法供 GitHub hosted runner 访问，配置专用 VPC runner，身份仍用 GitHub OIDC。
- 清理前重新执行 OIDC 交换，更新三项 STS 环境变量、重建客户端；不能假设 action 导出的静态临时凭证会自动续期。cleanup-only 每次取得新身份。任何凭证过期或认证失败必须显式报错。

RRSA 和 GitHub OIDC provider/role/trust、环境分支规则及系统节点均属于一次性环境准备，runner 不自动创建或删除这些共享资源。

## 确保新建 2 按量 + 1 Spot

1. 为每次运行生成不可复用 `run-id`，记录 cluster ID、git SHA、镜像 digest、基线节点/实例、所有权 manifest。所有新建 Kubernetes 对象和云资源携带 run 所有权，清理前核对 instance ID、cluster ID、run ID、managed-by。
2. 创建两个 run 专属 ECSNodeClass、两个 run 专属 Karpenter NodePool。NodeClass 显式使用已确认的 vSwitch、安全组、worker role、与目标 ACK/containerd/架构兼容的镜像；使用 provider 的 ACK 元数据选择和 ECS 精确 ID 校验，不靠镜像名称猜测。
3. 按量 NodePool `karpenter.sh/capacity-type In [on-demand]`；Spot NodePool `In [spot]`，Spot NodeClass `spotStrategy: SpotAsPriceGo`。不能把两种容量合在一个允许回退的 NodePool 中。
4. 两个 Deployment，分别 replicas 2 和 1，总计三 Pod；使用已审核的轻量容器镜像 digest，每 Pod request CPU 100m、memory 128Mi，无 PVC、Service LoadBalancer 或大负载。
5. Pod nodeSelector 精确选择本 run 的 NodePool，所有测试 Pod 共有 run label；使用 required podAntiAffinity，topologyKey `kubernetes.io/hostname`，匹配本 namespace 中同 run label，保证一 Pod 一节点。不得设置 nodeName。这样即使每个 Pod 很小，按量两个 Pod 也不能挤在一个节点。
6. 每个 NodePool 使用只属于本 run 的 NoSchedule taint；测试 Pod 使用精确 toleration。系统节点和其他池不满足 selector，现有节点不能冒充扩容结果。
7. 预检分别确认按量/Spot 区域库存、配额、磁盘、架构/镜像和网络支持。按量可选一个低成本规格；Spot 可提供多个已确认、同架构且同 vCPU 的候选规格/可用区，减少缺货影响，但不扩大测试目标。规格不硬编码为在任意区域都存在。
8. 设 NodePool CPU limits：按量为两个候选节点 CPU 合计、Spot 为一个候选节点 CPU；候选 CPU 必须相同，避免 limits 无法容纳目标节点。limits 不是原子费用上限：轮询分页云实例清单，发现多于目标的本 run 活跃实例立即失败并停止 workload。额外实例同样进入清理。
9. 验收期间设置 `expireAfter: Never` 和 voluntary disruption budget `nodes: "0"`，关闭会改变节点数的测试选项；不预设 consolidation 完成作为释放条件。Spot 自然抢占/注册失败也必须报告失败，不以 replacement 或 fallback 掩盖首次验收状态。

## 执行阶段和判定

预检与身份最多 5 分钟；创建/注册/Pod Ready 最多 20 分钟；独立清理最多 10 分钟；记录诊断预留 5 分钟；job timeout 45 分钟。每 30 秒打印阶段与耗时，成功后立即清理。目标是小规模功能验证，云端时长需首次实际测量，不承诺与 KWOK 相同。

1. **Preflight**：所有输入/环境开关、版本/镜像匹配、系统节点/controller 基线、RRSA 与 runner 身份、ACK endpoint、Kubernetes RBAC、Spot/按量容量；只读云查询和认证失败即停止。
2. **Deploy controller**：在专用测试 ACK 全新部署匹配 Chart/CRD/controller，验证使用 RRSA；已有系统组件和网络保留，记录该测试 release 的所有权。不能复用不匹配的 provider，也不在共享生产集群安装。
3. **Provision**：三个 Pod 触发三 NodeClaim。等待每个 NodeClaim Launched/Registered/Initialized/Ready=True、Node Ready=True、Pod Running/Ready=True 和 PodIP 非空。
4. **Cross-check**：三个不同 hostname、三个不同 instance ID，NodeClaim 和 Node 的 ProviderID 一致且映射到真实 ECS；核实 run/cluster 标签、ACK 注册、规格、镜像、可用区、vSwitch、安全组及 ECS Running。
5. **Capacity proof**：按量两个实例均为 `InstanceChargeType=PostPaid`、`SpotStrategy=NoSpot`；Spot 一个为 `PostPaid`、`SpotStrategy=SpotAsPriceGo`。节点 label 与 ECS 属性不一致即失败；API 查询失败和缺失关键字段不能当通过。
6. **Cleanup**：先将测试 Deployments scale 为零/删除，防止重复拉起；再逐个显式删除本 run NodeClaim，由 Karpenter 正常终止三 ECS。等待本 run Node/NodeClaim 消失、ECS 精确 ID 与标签双重查询无残留，再删除 run NodePool/NodeClass/namespace 和本次测试 controller release。基线系统节点、ACK node pool、网络、RRSA、GitHub OIDC 和角色不删除。
7. **Cloud residue**：比较 manifest 和创建前快照，分页检查所有本 run 实例及新建自动删除磁盘/ENI、launch template、测试 attach/bootstrap 资源。权限检查通过后才认可空查询，认证/API 错误不可视为资源不存在；处理最终一致性并保存清理结果。

正常 termination 失败时 runner 可在核对严格所有权后兜底清理，但整体结果仍为失败。测试超时后用独立清理 context；SIGINT/SIGTERM 尽力保存 inventory/清理，硬中断保留无密钥 manifest，支持相同 run-id 的 cleanup-only。禁止全局 `delete nodepool --all` 或按 cluster-name 释放既有集群。

报告记录实际 Kubernetes、core/controller/chart/CRD 版本、三节点与三实例映射、容量类型、三 Pod、身份模式/角色、各阶段耗时和 Kubernetes/云端零残留证据。不得上传 kubeconfig、JWT、临时云凭证或原始 userdata。读取与写入权限、Spot 缺货/抢占、Ready 超时及清理残留都独立标记，不重跑至绿。

## 现有代码缺口与交付

以下列出原设计时的缺口。#88 / PR #91 已实现完整 STS 客户端、RRSA values/Pod 验证及手动 OIDC composite；#23 的 smoke-cloud/runner/独立 workflow 实现对应生命周期、续期清理及报告。原始 userdata、完整 Pod spec 和原始 controller 日志不上传，改用白名单云属性和事件 reason。区域磁盘/ENI 新增差异若无法归属会保守失败，绝不自动删除。实际集群 IAM/RBAC、RRSA 身份认证、三节点创建及零残留仍需真实运行证据，离线通过不替代这些验收。

- `pkg/operator/operator.go` 已支持 RRSA；Chart 已支持显式 token 投影，需验证全新 RRSA 部署和信任一致性。
- `test/hack/e2e/ackctl/config.go` 只有 AK/SK 配置；`ack_runtime.go` 的 CS/ECS/VPC 客户端未设置 SecurityToken，不能直接消费 GitHub OIDC action 的 STS 输出。其他 client factory（含 SLB 等）需要一并盘点，避免仅修部分路径。
- 现有 `ackctl` setup 注入 `alibabacloud-credentials` Secret 并在 Helm 设置 credentialsSecretName，必须增加 RRSA-only 部署路径。现有 setup/cleanup 以 ACK 集群为单位，三节点 smoke 必须有独立复用集群与 run-owned 清理路径。
- 现有 `.github/workflows/e2e.yaml` 强制静态 AK/SK Secret，且运行 suite 前有全局删除；新 smoke 使用独立入口，不复用这些全局操作。清理之前刷新 runner STS 会话。
- 保留并改造 ackctl 为 smoke 的 ACK/云资源辅助工具；KWOK 仅需轻量版本校验，脱离 ackctl 编译另行处理。
- #88 先交付双 OIDC、STS 客户端和无 AK 的 controller 部署；#23 再交付三节点 smoke、严格云端断言与 cleanup-only。各实现一个 issue/PR，合并 dev。#5 保留 v1.14.1 发布验收，#20 保留 Spot 中断能力。
- 2026-10-12 维护者说明当前没有 ACK，并授权使用本地 oss-sftp profile 配置身份。已复用 GitHub OIDC provider、创建项目专用 karpenter-ack-e2e-runner role 和 ack-e2e GitHub Environment，仅允许 dev/main，设置非敏感 ARN/region environment variables，ACK_E2E_ENABLED=false。角色尚无操作权限，待选定集群后按实际资源配置 RAM/RBAC。未创建 ACK/ECS，RRSA 和真实 E2E 仍待集群。详情见 ACK_OIDC_SETUP.md。

## 官方依据

- [ACK RRSA：issuer/provider、信任策略、token 投影与刷新](https://www.alibabacloud.com/help/en/ack/ack-managed-and-ack-dedicated/user-guide/use-rrsa-to-authorize-pods-to-access-different-cloud-services)
- [GitHub OIDC：environment subject、immutable subject、权限](https://docs.github.com/en/actions/reference/security/oidc)
- [阿里云官方 GitHub 凭证 action](https://github.com/aliyun/configure-aliyun-credentials-action)
- [ECS DescribeInstances：SpotStrategy 与实际实例属性](https://www.alibabacloud.com/help/zh/ecs/developer-reference/api-ecs-2014-05-26-describeinstances)
