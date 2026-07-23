# 分支与发布策略

## 分支职责

- `master`：稳定、已验收、可部署的版本，只接受 Pull Request。
- `develop`：当前改造阶段的集成分支，只接受通过质量门禁的 Pull Request。
- `feature/*`：从 `develop` 创建的短期功能分支，完成后合回 `develop`。
- `hotfix/*`：从 `master` 创建的旧系统阻断性修复分支，合入 `master` 后必须同步回 `develop`。

Codex 创建的工作分支使用 `codex/` 前缀。阶段基线、自动化或文档改造也通过 Pull Request 合并。

## 阶段一基线

阶段一成果先通过独立基线 Pull Request 合入 `master`，随后创建 `phase1-baseline` 标签。只有基线合并、GitHub 保护规则生效、部署密钥和权限矩阵完成确认后，才从最新 `master` 创建 `develop`。

## 阶段二开发

阶段二功能按以下粒度拆分：

- `feature/iam-foundation`
- `feature/session-rotation`
- `feature/asset-schema-compiler`
- `feature/responsible-department`
- `feature/asset-versioning`

功能分支应保持短期，禁止把整个月份的工作堆积在单一分支。Schema、数据库迁移、OpenAPI 和生成产物必须在同一 Pull Request 中审查。

## 合并与发布

1. 功能 Pull Request 以 `develop` 为目标分支。
2. 普通 Pull Request 只要求快速 `quality` 检查和旧系统冻结检查通过。
3. 阶段退出条件全部通过后，创建 `develop -> master` 发布 Pull Request。
4. 合入 `master` 后自动执行竞态、Compose、OpenAPI lint 和完整安全检查；通过后创建阶段标签。
5. 禁止对 `master`、`develop` 强制推送或直接提交。

完整检查也在每周一和手动触发时执行。小团队日常开发不为每个功能分支重复运行耗时的全量检查。

## 旧系统冻结

`main.go`、`internal/` 和 `web/` 已进入功能冻结。只有阻断上线或迁移的安全、数据正确性和兼容性修复可以修改，Pull Request 必须使用 `legacy-hotfix-approved` 标签并说明风险、回归证据和移除时间。
