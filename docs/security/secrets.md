# 密钥与敏感配置管理

## 基本规则

- 仓库只保存变量名和非敏感示例，不保存真实密码、令牌、私钥或连接串。
- 本地开发使用未提交的 `.env`；`.env.example` 只提供占位值。
- GitHub Actions 使用 Repository 或 Environment Secrets。
- 生产部署使用受控密钥平台或 Kubernetes Secret，不从源码、镜像层或普通 ConfigMap 读取。
- 日志、测试快照和错误响应不得输出密钥、完整 Token 或数据库连接密码。

## 阶段一变量

| 变量 | 用途 | 最低要求 |
|---|---|---|
| `JWT_SECRET` | 旧系统 HS256 签名 | 至少 32 个随机字符 |
| `ADMIN_PASSWORD` | 旧系统首次启动管理员密码 | 至少 12 个随机字符 |
| `POSTGRES_PASSWORD` | 新平台 PostgreSQL | 长随机密码 |
| `RABBITMQ_PASSWORD` | RabbitMQ | 长随机密码 |
| `MINIO_ROOT_PASSWORD` | MinIO | 长随机密码 |

缺少旧系统密钥时，应用只允许生成进程级临时值用于开发；正式环境必须显式配置。Compose 对基础设施密码采用必填校验。

## 阶段二变量

| 变量 | 用途 | 生产要求 |
|---|---|---|
| `IAM_PRIVATE_KEY` / `IAM_PRIVATE_KEY_FILE` | RS256 Access Token 私钥 | 优先挂载只读文件；不得提交、写入镜像或普通日志 |
| `IAM_TOKEN_ISSUER` | Token 签发者 | 固定为目标环境可识别值 |
| `IAM_TOKEN_AUDIENCE` | Token 受众 | 固定为资产治理平台 API |
| `IAM_BOOTSTRAP_ADMIN_PASSWORD` | 首次管理员初始密码 | 随机且至少 12 位；首次登录后强制修改 |
| `IAM_COOKIE_SECURE` | Refresh Cookie 仅 HTTPS | 生产必须为 `true` |

IAM 未配置私钥时只允许在本地开发中生成进程级临时 RSA 密钥，重启后 Access Token 失效。私钥只能进入 IAM 服务。Refresh Token 只在浏览器 HttpOnly Cookie 中传输，服务端数据库仅保存哈希。

## GitHub 配置

在 `Settings -> Secrets and variables -> Actions` 中配置 CI 或部署所需变量。生产 Secret 应放入受保护的 `production` Environment，并设置人工审批。Pull Request 工作流不得向来自 Fork 的代码暴露生产 Secret。

## 轮换

- 发现泄露时立即撤销或轮换，并检查 Git 历史、构建日志和发布镜像。
- IAM 私钥必须带密钥 ID，允许新旧公钥在短轮换窗口内并存。
- 基础设施账号按服务拆分后分别轮换，禁止四个领域服务长期共用数据库超级账号。
