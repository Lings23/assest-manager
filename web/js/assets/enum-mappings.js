/**
 * 枚举映射配置
 * 管理所有资产类型的枚举字段映射关系
 * 数字编码 → 中文显示文本
 */

export const ENUM_MAPPINGS = {
  // system-info (信息系统清单)
  'system-info': {
    run_status: {
      0: '正式运行',
      1: '试运行',
      2: '在建',
      3: '临时下线',
      4: '停用'
    },
    network_type: {
      0: '互联网',
      1: '专网',
      2: '互联网+专网'
    },
    mobile_app_type: {
      0: '否',
      1: 'APP',
      2: '小程序',
      3: '快应用',
      4: '其他'
    },
    maintenance_mode: {
      0: '现场运维',
      1: '远程运维',
      2: '现场+远程运维'
    },
    backup_type: {
      0: '数据灾备',
      1: '系统灾备',
      2: '数据灾备+系统灾备',
      3: '无灾备'
    },
    security_level: {
      0: '一级',
      1: '二级',
      2: '三级',
      3: '未定级'
    },
    security_assessment: {
      0: '',
      1: '符合',
      2: '基本符合',
      3: '不符合'
    },
    crypto_assessment: {
      0: '',
      1: '符合',
      2: '基本符合',
      3: '不符合'
    },
    cloud_security_review: {
      0: '',
      1: '通过',
      2: '未通过',
      3: '未参加'
    }
  },

  // hardware (信息化软硬件清单)
  'hardware': {
    use_status: {
      0: '在网',
      1: '不在网',
      2: '闲置',
      3: '报废'
    },
    device_status: {
      0: '正常',
      1: '故障',
      2: '维修中'
    }
  },

  // data (数据资产清单)
  'data': {
    security_level: {
      0: '一级',
      1: '二级',
      2: '三级'
    },
    data_classification: {
      0: '',
      1: '重要数据',
      2: '一般3级',
      3: '一般2级',
      4: '一般1级'
    },
    data_source: {
      0: '共享交换',
      1: '人工填报'
    }
  },

  // supply-chain (供应链清单)
  'supply-chain': {
    supplier_type: {
      0: '设计方',
      1: '开发方',
      2: '承建方',
      3: '网络安全产品提供方',
      4: '信息化产品提供方',
      5: '运维方',
      6: '安全服务提供方',
      7: '信息安全评测方',
      8: '其他参与方'
    }
  },

  // vulnerability (风险漏洞清单)
  'vulnerability': {
    severity: {
      0: '高',
      1: '中',
      2: '低'
    },
    discovery_method: {
      0: '渗透',
      1: '漏扫',
      2: '第三方通报'
    }
  }
};

/**
 * 根据编码获取显示文本
 * @param {string} assetType - 资产类型，如 'system-info'
 * @param {string} fieldName - 字段名，如 'run_status'
 * @param {number} code - 数字编码，如 0
 * @returns {string} 显示文本，如 '正式运行'
 */
export function getEnumText(assetType, fieldName, code) {
  const mapping = ENUM_MAPPINGS[assetType]?.[fieldName];
  if (mapping && code !== null && code !== undefined) {
    return mapping[code] ?? '';
  }
  return '';
}

/**
 * 获取枚举选项列表（用于下拉框）
 * @param {string} assetType - 资产类型
 * @param {string} fieldName - 字段名
 * @returns {Array} 选项数组 [{ code: 0, text: '正式运行' }, ...]
 */
export function getEnumOptions(assetType, fieldName) {
  const mapping = ENUM_MAPPINGS[assetType]?.[fieldName];
  if (!mapping) return [];

  return Object.entries(mapping)
    .map(([code, text]) => ({ code: parseInt(code), text }))
    .filter(item => item.text !== '') // 过滤空值选项
    .sort((a, b) => a.code - b.code);
}

/**
 * 获取所有枚举选项列表（包含空值）
 * @param {string} assetType - 资产类型
 * @param {string} fieldName - 字段名
 * @returns {Array} 选项数组 [{ code: 0, text: '' }, { code: 1, text: '符合' }, ...]
 */
export function getAllEnumOptions(assetType, fieldName) {
  const mapping = ENUM_MAPPINGS[assetType]?.[fieldName];
  if (!mapping) return [];

  return Object.entries(mapping)
    .map(([code, text]) => ({ code: parseInt(code), text }))
    .sort((a, b) => a.code - b.code);
}

/**
 * 检查字段是否是枚举字段
 * @param {string} assetType - 资产类型
 * @param {string} fieldName - 字段名
 * @returns {boolean}
 */
export function isEnumField(assetType, fieldName) {
  return ENUM_MAPPINGS[assetType]?.[fieldName] !== undefined;
}