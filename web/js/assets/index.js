/**
 * 资产类型注册表
 * 统一管理所有资产类型的配置
 */

// 导入所有类型配置
import systemInfo from './types/system-info.js';
import hardware from './types/hardware.js';
import data from './types/data.js';
import supplyChain from './types/supply-chain.js';
import vulnerability from './types/vulnerability.js';
import softwareStat from './types/software-stat.js';
import responsibleDept from './types/responsible-dept.js';

// 资产类型注册表
const ASSET_REGISTRY = {
    'system-info': systemInfo,
    'hardware': hardware,
    'data': data,
    'supply-chain': supplyChain,
    'vulnerability': vulnerability,
    'software-stat': softwareStat,
    'responsible-dept': responsibleDept
};

/**
 * 获取类型配置
 * @param {string} type - 类型标识符
 * @returns {Object|null} - 类型配置对象
 */
export function getAssetConfig(type) {
    return ASSET_REGISTRY[type] || null;
}

/**
 * 动态注册新类型
 * @param {Object} config - 类型配置对象
 */
export function registerAssetType(config) {
    if (!config || !config.type) {
        throw new Error('Type config must have a type identifier');
    }
    ASSET_REGISTRY[config.type] = config;
}

/**
 * 获取所有类型列表
 * @returns {string[]} - 所有已注册的类型标识符
 */
export function getAllAssetTypes() {
    return Object.keys(ASSET_REGISTRY);
}

/**
 * 检查类型是否存在
 * @param {string} type - 类型标识符
 * @returns {boolean}
 */
export function hasAssetType(type) {
    return ASSET_REGISTRY.hasOwnProperty(type);
}

// 导出注册表供高级用法
export { ASSET_REGISTRY };