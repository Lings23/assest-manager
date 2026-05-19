# 资产导入功能使用说明

## 功能概述

已成功实现资产数据导入功能，支持以下5种资产类型的CSV文件导入：

1. 信息系统清单 (system-info)
2. 信息化软硬件清单 (hardware)
3. 数据资产清单 (data)
4. 供应链清单 (supply-chain)
5. 风险漏洞清单 (vulnerability)

## 使用步骤

### 1. 下载模板

在资产管理页面，点击"导入"按钮，然后点击"下载模板"按钮获取对应资产类型的CSV模板文件。

模板特点：
- 包含正确的表头格式
- 包含一行示例数据
- 带BOM头的UTF-8编码，确保Excel正确显示中文
- 必填字段用*号标记

### 2. 填写数据

按照模板格式填写您的数据：
- 保持表头不变
- 每行代表一条记录
- 必填字段不能为空
- 日期格式建议使用 YYYY-MM-DD
- 数字字段不要包含单位

### 3. 上传导入

1. 点击"导入"按钮
2. 选择填写好的CSV文件
3. 点击"开始导入"
4. 等待导入完成，查看导入结果

### 4. 查看结果

导入完成后会显示：
- 成功导入的记录数
- 失败的记录数
- 每条失败记录的详细错误信息

## API接口说明

### 下载模板
```
GET /api/assets/:type/template
Headers: Authorization: Bearer {token}
Response: CSV文件下载
```

### 导入数据
```
POST /api/assets/:type/import
Headers: Authorization: Bearer {token}
Body: multipart/form-data with file field
Response: {
  "code": 200,
  "message": "导入完成：成功X条，失败Y条",
  "success_count": X,
  "error_count": Y,
  "errors": ["第X行：错误描述", ...]
}
```

## 注意事项

1. **文件格式**：仅支持CSV格式文件
2. **编码要求**：建议使用UTF-8编码（带BOM头）
3. **表头验证**：必须与模板表头完全一致
4. **必填字段**：标记为*的字段不能为空
5. **数据验证**：系统会逐行验证并跳过错误行
6. **事务处理**：成功的记录会被保存，失败的记录会被跳过
7. **权限控制**：需要登录后才能使用导入功能

## 错误处理

导入过程中可能出现的错误：
- 文件格式错误：请确保是CSV格式
- 表头不匹配：请下载最新模板
- 必填字段为空：检查标记*的字段
- 数据类型错误：确保数字字段只包含数字
- 数据库错误：联系系统管理员

## 技术实现

### 后端实现 (Go)
- 路由：`internal/routes/routes.go`
- Handler：`internal/handlers/import.go`
- 功能：
  - CSV文件解析
  - 表头验证
  - 必填字段校验
  - 批量数据插入
  - 错误信息收集

### 前端实现 (JavaScript)
- 文件：`web/index.html`
- 功能：
  - 导入对话框
  - 模板下载
  - 文件上传
  - 进度显示
  - 结果展示

## 示例数据

各资产类型的示例数据请参考系统中的模板文件，或查看 `internal/handlers/import.go` 中的 `getExampleData` 函数。
