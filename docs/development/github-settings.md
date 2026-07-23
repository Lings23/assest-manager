# GitHub 仓库设置清单

以下项目需要仓库管理员在 GitHub 网页完成，不能仅通过代码文件生效。

## `master` 保护规则

- 要求通过 Pull Request 合并。
- 小团队只有一名维护者时不强制独立审批；增加协作者后再启用一名审批人。
- 要求快速 `quality` 检查通过。
- 禁止 force push 和分支删除。

## `develop` 保护规则

- 要求通过 Pull Request 合并。
- 要求快速 `quality` 检查通过。
- 禁止 force push 和分支删除。

`master` 保持默认分支；阶段二功能 Pull Request 显式选择 `develop` 为目标分支。

完整竞态、Compose 和安全检查在 `master` 合并后、每周定时及手动触发时执行。阶段标签只在该检查通过后创建。

Dependabot 常规版本更新按月跨生态合并为一个 PR，避免小团队同时处理大量独立升级。安全更新不等待月度批次，仍由 GitHub 安全功能单独处理。

## Secrets 与 Environments

在 `Settings -> Secrets and variables -> Actions` 配置实际 Secret。建议创建 `development`、`staging`、`production` Environments，并对生产环境启用人工审批。变量清单和安全要求见 `docs/security/secrets.md`。

## 安全功能

在仓库能力允许时启用：

- Dependabot alerts
- Dependabot security updates
- Secret scanning
- Push protection
- Actions 最小写权限

## 业务确认

在创建 `develop` 前，由业务负责人确认 `权限矩阵.md`，尤其是填报员的数据范围。确认结果应记录确认人、日期和结论，避免 IAM 实现阶段反复修改数据模型。
