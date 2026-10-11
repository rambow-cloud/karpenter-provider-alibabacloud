# 上手与参与

## 准备试用

先阅读[当前状态](../status/index.md)、[快速开始](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/QUICK_START.md)与[升级说明](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/UPGRADING.md)。快速开始文档中的历史版本示例需结合升级说明使用，不能把旧镜像与当前开发 chart 混用。

试用前需要明确 ACK 集群、区域、VPC/交换机、安全组、可用镜像与 RAM 权限，并使用隔离测试环境。当前候选版本必须从对应代码构建 controller 镜像；构建条件和 feature gates 迁移以升级说明为准。

本项目要求 Kubernetes 1.34+，1.33 及更早版本不受支持；请先升级集群再安装本候选版本。版本限制由 [Issue #7](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/7) 跟踪，真实云端验收由相应 milestone 跟踪。请先确认目标环境与已记录的支持边界。

## 找到下一项工作

- [报告缺陷](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/new?template=bug_report.yml)：提供最小复现、版本、预期行为与脱敏日志。
- [提出功能](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/issues/new?template=feature_request.yml)：描述使用场景与可观察的验收条件。
- [查看路线图](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/milestones)：区分版本兼容、通用修复、安全和测试工作。
- [查看任务看板](https://github.com/orgs/rambow-cloud/projects/4)：找到 Todo 与正在推进的工作。

## 贡献流程

1. 先创建或认领一个 issue，明确范围、milestone 和验收条件。
2. 创建 `feat/<issue-number>-<short-description>` 分支，开始实现时将任务移到 In Progress。
3. 提交 PR，填写仓库模板并关联 `Closes #<issue-number>`。
4. 完成相关验证和必需 CI；合并后记录结果，再把任务标为 Done。

具体规则见 [CONTRIBUTING.md](https://github.com/rambow-cloud/karpenter-provider-alibabacloud/blob/main/CONTRIBUTING.md)。发布内容与验证结果都应可追溯，账号标识、凭据和 kubeconfig 不进入代码或 issue。
