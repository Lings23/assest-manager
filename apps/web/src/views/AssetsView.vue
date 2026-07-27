<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { request } from '@/api/client'
import { assetSchemas } from '@/generated/asset-meta'
import { useSessionStore } from '@/stores/session'

interface EnumOption { value: string | number; label: string }
interface FieldMeta {
  name: string
  label: string
  type: 'string' | 'integer' | 'number' | 'boolean' | 'date'
  required: boolean
  requiredWhen?: string
  default?: unknown
  enum?: readonly EnumOption[]
  list: boolean
  group?: string
  widget?: string
}
interface SchemaMeta {
  type: string
  title: string
  version: number
  fields: readonly FieldMeta[]
}
interface AssetRecord {
  id: string
  type: string
  owner_id: string
  department_id?: string
  version: number
  fields: Record<string, unknown>
  created_at: string
  updated_at: string
  deleted_at?: string
}

const schemas = assetSchemas as unknown as readonly SchemaMeta[]
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const loading = ref(false)
const saving = ref(false)
const showDeleted = ref(false)
const search = ref('')
const items = ref<AssetRecord[]>([])
const dialogVisible = ref(false)
const editing = ref<AssetRecord | null>(null)
const form = reactive<Record<string, unknown>>({})

const currentType = computed(() => String(route.params.type || schemas[0]?.type || ''))
const schema = computed(() => schemas.find((item) => item.type === currentType.value) ?? schemas[0])
const listFields = computed(() => schema.value?.fields.filter((field) => field.list) ?? [])
const groupedFields = computed(() => {
  const result = new Map<string, FieldMeta[]>()
  for (const field of schema.value?.fields ?? []) {
    const group = field.group || '基本信息'
    result.set(group, [...(result.get(group) ?? []), field])
  }
  return result
})

async function load() {
  if (!currentType.value) return
  loading.value = true
  try {
    const query = new URLSearchParams({
      limit: '100',
      search: search.value,
      include_deleted: String(showDeleted.value),
    })
    const response = await request<{ items: AssetRecord[] }>(
      `/api/v1/assets/${currentType.value}?${query}`,
    )
    items.value = response.items
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : '加载失败')
  } finally {
    loading.value = false
  }
}

function selectType(type: string) {
  void router.push(`/assets/${type}`)
}

function openCreate() {
  editing.value = null
  resetForm()
  dialogVisible.value = true
}

function openEdit(record: AssetRecord) {
  editing.value = record
  resetForm(record.fields)
  dialogVisible.value = true
}

function resetForm(values: Record<string, unknown> = {}) {
  for (const key of Object.keys(form)) delete form[key]
  for (const field of schema.value?.fields ?? []) {
    if (values[field.name] !== undefined) form[field.name] = values[field.name]
    else if (field.default !== undefined) form[field.name] = field.default
    else if (field.type === 'boolean') form[field.name] = false
  }
}

async function save() {
  saving.value = true
  try {
    if (editing.value) {
      await request(`/api/v1/assets/${currentType.value}/${editing.value.id}`, {
        method: 'PATCH',
        body: JSON.stringify({ version: editing.value.version, fields: form }),
      })
    } else {
      await request(`/api/v1/assets/${currentType.value}`, {
        method: 'POST',
        body: JSON.stringify({ fields: form }),
      })
    }
    dialogVisible.value = false
    ElMessage.success(editing.value ? '资产已更新' : '资产已创建')
    await load()
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : '保存失败')
  } finally {
    saving.value = false
  }
}

async function remove(record: AssetRecord) {
  await ElMessageBox.confirm('资产将被软删除，可由管理员恢复。', '确认删除', { type: 'warning' })
  await request<void>(`/api/v1/assets/${currentType.value}/${record.id}?version=${record.version}`, {
    method: 'DELETE',
  })
  ElMessage.success('资产已软删除')
  await load()
}

async function restore(record: AssetRecord) {
  await request(`/api/v1/assets/${currentType.value}/${record.id}/restore`, {
    method: 'POST',
    body: JSON.stringify({ version: record.version }),
  })
  ElMessage.success('资产已恢复')
  await load()
}

watch(currentType, load)
watch(showDeleted, load)
onMounted(load)
</script>

<template>
  <div class="asset-toolbar">
    <el-select :model-value="currentType" class="type-select" @change="selectType">
      <el-option v-for="item in schemas" :key="item.type" :label="item.title" :value="item.type" />
    </el-select>
    <el-input v-model="search" clearable placeholder="搜索 Schema 标记为可搜索的字段" @keyup.enter="load" />
    <el-button @click="load">查询</el-button>
    <el-checkbox v-if="session.isAdmin" v-model="showDeleted">包含已删除</el-checkbox>
    <el-button type="primary" @click="openCreate">新增</el-button>
  </div>

  <el-alert
    :title="`${schema?.title ?? ''} · Schema v${schema?.version ?? ''}`"
    type="info"
    :closable="false"
    class="schema-alert"
  />

  <el-table v-loading="loading" :data="items" border>
    <el-table-column v-for="field in listFields" :key="field.name" :label="field.label" min-width="140">
      <template #default="{ row }">{{ row.fields[field.name] ?? '—' }}</template>
    </el-table-column>
    <el-table-column prop="version" label="版本" width="80" />
    <el-table-column label="操作" width="220" fixed="right">
      <template #default="{ row }">
        <template v-if="!row.deleted_at">
          <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
          <el-button v-if="session.isAdmin" link type="danger" @click="remove(row)">删除</el-button>
        </template>
        <el-button v-else-if="session.isAdmin" link type="success" @click="restore(row)">恢复</el-button>
      </template>
    </el-table-column>
  </el-table>

  <el-dialog v-model="dialogVisible" :title="editing ? '编辑资产' : '新增资产'" width="760px">
    <section v-for="[group, fields] in groupedFields" :key="group" class="form-section">
      <h3>{{ group }}</h3>
      <el-form label-position="top">
        <el-form-item v-for="field in fields" :key="field.name" :label="field.label" :required="field.required">
          <el-switch v-if="field.type === 'boolean'" v-model="form[field.name]" />
          <el-select v-else-if="field.enum?.length" v-model="form[field.name]" clearable>
            <el-option
              v-for="option in field.enum"
              :key="String(option.value)"
              :label="option.label"
              :value="option.value"
            />
          </el-select>
          <el-date-picker
            v-else-if="field.type === 'date'"
            v-model="form[field.name]"
            type="date"
            value-format="YYYY-MM-DD"
          />
          <el-input-number
            v-else-if="field.type === 'integer' || field.type === 'number'"
            v-model="form[field.name]"
            :precision="field.type === 'number' ? 2 : 0"
          />
          <el-input
            v-else
            v-model="form[field.name]"
            :type="field.widget === 'textarea' ? 'textarea' : 'text'"
            :rows="field.widget === 'textarea' ? 3 : undefined"
          />
        </el-form-item>
      </el-form>
    </section>
    <template #footer>
      <el-button @click="dialogVisible = false">取消</el-button>
      <el-button type="primary" :loading="saving" @click="save">保存</el-button>
    </template>
  </el-dialog>
</template>
