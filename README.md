# 资产信息管理系统

## 项目说明

这是一个基于Go语言开发的资产信息管理系统,用于管理5类资产清单:
1. 信息系统清单
2. 信息化软硬件清单
3. 数据资产清单
4. 供应链清单
5. 风险漏洞清单

## 环境要求

- Go 1.22或更高版本

## 安装Go

### Windows系统

1. 访问 https://golang.org/dl/ 下载Windows安装包
2. 运行安装程序,按提示完成安装
3. 打开新的命令提示符窗口,验证安装:
   ```bash
   go version
   ```

或者使用winget安装:
```bash
winget install GoLang.Go
```

### Linux系统

```bash
# Ubuntu/Debian
sudo apt update
sudo apt install golang-go

# CentOS/RHEL
sudo yum install golang

# 或使用snap
sudo snap install go --classic
```

## 项目设置

### 1. 安装依赖

在项目根目录执行:
```bash
go mod download
```

### 2. 编译运行

```bash
# 直接运行
go run main.go

# 或编译后运行
go build -o asset-manager.exe
./asset-manager.exe
```

### 3. 访问系统

浏览器打开: http://localhost:8080

默认管理员账号:
- 用户名: admin
- 密码: admin123

## 项目结构

```
asset-manager/
├── main.go                  # 主程序入口
├── go.mod                   # Go模块定义
├── go.sum                   # 依赖校验文件
├── internal/
│   ├── config/
│   │   └── config.go       # 配置管理
│   ├── database/
│   │   └── database.go     # 数据库初始化
│   ├── models/
│   │   └── models.go       # 数据模型
│   ├── handlers/
│   │   ├── auth.go         # 认证处理
│   │   ├── assets.go       # 资产管理处理
│   │   ├── users.go        # 用户管理处理
│   │   └── stats.go        # 统计处理
│   ├── middleware/
│   │   └── auth.go         # 认证中间件
│   ├── routes/
│   │   └── routes.go       # 路由配置
│   └── utils/
│       ├── jwt.go          # JWT工具
│       └── export.go       # 导出工具
├── web/
│   └── index.html          # 前端页面(嵌入)
└── data/
    ├── assets.db           # SQLite数据库(自动生成)
    ├── backup/             # 备份目录
    └── export/             # 导出文件目录
```

## 功能特性

### 用户管理
- 管理员和填报员两种角色
- JWT Token认证
- 密码bcrypt加密

### 资产管理
- 5类资产信息的增删改查
- 分页、搜索、筛选
- 软删除机制

### 数据统计
- 多维度统计图表
- 实时数据看板

### 数据导出
- 支持Excel(.xlsx)导出
- 支持CSV格式导出

### 系统管理
- 操作日志记录
- 数据备份恢复
- 用户权限管理

## 技术栈

- **后端**: Go 1.22+, Gin框架
- **数据库**: SQLite (modernc.org/sqlite)
- **认证**: JWT (golang-jwt/jwt)
- **加密**: bcrypt (golang.org/x/crypto)
- **导出**: Excel (tealeg/xlsx)
- **前端**: HTML5 + CSS3 + JavaScript + Chart.js

## 开发说明

### 添加新接口

1. 在 `internal/handlers/` 创建处理函数
2. 在 `internal/routes/routes.go` 注册路由
3. 在前端页面添加对应的JavaScript调用

### 数据库迁移

修改 `internal/database/database.go` 中的表结构SQL语句

### 前端修改

编辑嵌入的HTML文件,重新编译即可

## 注意事项

1. 首次运行会自动创建数据库和管理员账号
2. 数据库文件存储在 `data/assets.db`
3. 建议定期备份数据库文件
4. 生产环境请修改JWT密钥

## 常见问题

### Q: 编译时提示找不到模块
A: 执行 `go mod tidy` 自动下载依赖

### Q: 端口被占用
A: 设置环境变量 `PORT=8081` 或其他端口

### Q: 如何重置管理员密码
A: 删除 `data/assets.db` 文件,重启系统会自动创建默认账号

## 许可证

本项目仅供内部使用

## 联系方式

如有问题请联系系统管理员
