/**
 * 资产类型配置接口定义
 * 定义统一的类型配置结构
 */

/**
 * 资产类型配置结构
 * @typedef {Object} AssetTypeConfig
 * @property {string} type - 类型标识符（如 'system-info'）
 * @property {string} title - 页面标题
 * @property {string} apiType - API路径（可选，默认等于type）
 * @property {string} tableHeaders - 表头HTML
 * @property {string[]} tableFields - 表格显示字段列表
 * @property {string} formTemplate - 表单HTML模板
 * @property {FieldConfig[]} fields - 字段定义（用于动态生成、校验）
 * @property {Function|null} collector - 自定义数据收集器
 * @property {Object} hooks - 生命周期钩子
 * @property {string[]} searchFields - 搜索字段列表
 */

/**
 * 字段配置结构
 * @typedef {Object} FieldConfig
 * @property {string} name - 字段名（对应后端JSON）
 * @property {string} label - 显示标签
 * @property {string} type - 输入类型: 'text'|'number'|'date'|'select'|'textarea'
 * @property {boolean} required - 是否必填
 * @property {string} placeholder - 占位提示
 * @property {string[]} options - select类型的选项
 * @property {string} section - 所属表单分组
 * @property {boolean} fullWidth - 是否占满一行
 */

// 配置接口导出（用于文档参考）
export const AssetTypeConfigInterface = {};
export const FieldConfigInterface = {};