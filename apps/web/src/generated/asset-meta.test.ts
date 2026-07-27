import { describe, expect, it } from 'vitest'
import { assetSchemas } from './asset-meta'

describe('generated asset metadata', () => {
  it('contains seven uniquely named asset types', () => {
    expect(assetSchemas).toHaveLength(7)
    expect(new Set(assetSchemas.map((schema) => schema.type)).size).toBe(7)
  })

  it('contains unique business fields and no system-write fields', () => {
    const reserved = new Set([
      'id', 'owner_id', 'department_id', 'version',
      'created_at', 'updated_at', 'deleted_at',
    ])
    for (const schema of assetSchemas) {
      const names = schema.fields.map((field) => field.name)
      expect(new Set(names).size).toBe(names.length)
      expect(names.some((name) => reserved.has(name))).toBe(false)
    }
  })

  it('keeps responsibility department as the first end-to-end migration type', () => {
    const schema = assetSchemas.find((candidate) => candidate.type === 'responsible-department')
    expect(schema?.fields.some((field) => field.name === 'department_name' && field.required)).toBe(true)
  })
})
