<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { request } from '@/api/client'
import { useSessionStore } from '@/stores/session'

interface Job {
  id: string
  asset_type: string
  phase: 'preflight' | 'execution'
  state: string
  progress: number
  result?: { total?: number; error_count?: number; error_download_url?: string }
  last_error?: string
}
const props = defineProps<{ assetType: string }>()
const emit = defineEmits<{ completed: [] }>()
const jobs = ref<Job[]>([])
const file = ref<File | null>(null)
const requestKey = ref('')
const busy = ref(false)
const error = ref('')
let timer: ReturnType<typeof setTimeout> | undefined
let stopped = false
const labels: Record<string, string> = {
  pending: '等待执行', running: '执行中', awaiting_confirmation: '等待确认',
  succeeded: '已完成', failed: '等待重试', cancelled: '已取消', dead_letter: '失败',
}
function selectFile(event: Event) {
  file.value = (event.target as HTMLInputElement).files?.[0] ?? null
  requestKey.value = crypto.randomUUID()
}
async function load() {
  try {
    const response = await request<{ items: Job[] | null }>('/api/v1/import-jobs')
    const next = response.items ?? []
    if (next.some((job) => job.state === 'succeeded' && jobs.value.find((old) => old.id === job.id)?.state !== 'succeeded')) emit('completed')
    jobs.value = next
    error.value = ''
  } catch (cause) { error.value = cause instanceof Error ? cause.message : '任务加载失败' }
  if (!stopped) timer = setTimeout(load, 2000)
}
async function create() {
  if (!file.value || busy.value) return
  busy.value = true
  try {
    const body = JSON.stringify({ asset_type: props.assetType, idempotency_key: requestKey.value, csv: await file.value.text() })
    if (new TextEncoder().encode(body).length > 104857600) throw new Error('编码后的请求超过 100 MiB')
    await request('/api/v1/import-jobs', { method: 'POST', body })
    file.value = null
    ElMessage.success('文件已提交，正在预检；通过后请确认导入')
    clearTimeout(timer)
    await load()
  } catch (cause) { ElMessage.error(cause instanceof Error ? cause.message : '提交失败，可使用同一文件重试') }
  finally { busy.value = false }
}
async function act(job: Job, action: 'confirm' | 'cancel') {
  busy.value = true
  try {
    const path = action === 'confirm' ? `/api/v1/import-jobs/${job.id}/confirm` : `/api/v1/tasks/${job.id}/cancel`
    await request(path, { method: 'POST' })
    clearTimeout(timer)
    await load()
  } catch (cause) { ElMessage.error(cause instanceof Error ? cause.message : '操作失败') }
  finally { busy.value = false }
}
async function downloadErrors(job: Job) {
  try {
    const response = await fetch(`/api/v1/import-jobs/${job.id}/errors`, {
      credentials: 'include', headers: { Authorization: `Bearer ${useSessionStore().accessToken}` },
    })
    if (!response.ok) throw new Error('下载失败，请刷新会话后重试')
    const url = URL.createObjectURL(await response.blob())
    const link = document.createElement('a'); link.href = url; link.download = `import-errors-${job.id}.jsonl`; link.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  } catch (cause) { ElMessage.error(cause instanceof Error ? cause.message : '下载失败') }
}
watch(() => props.assetType, () => { file.value = null; requestKey.value = '' })
void load()
onBeforeUnmount(() => { stopped = true; clearTimeout(timer) })
</script>

<template>
  <el-collapse>
    <el-collapse-item title="CSV 导入与任务进度" name="imports">
      <p>选择当前资产类型的 CSV。预检通过后，点击确认才会写入资产。</p>
      <input type="file" accept=".csv,text/csv" aria-label="选择导入 CSV" :disabled="busy" @change="selectFile" />
      <el-button :disabled="!file || busy" :loading="busy" @click="create">提交预检</el-button>
      <el-alert v-if="error" :title="error" type="error" :closable="false" />
      <el-table :data="jobs.filter((job) => job.asset_type === assetType)">
        <el-table-column prop="id" label="任务" min-width="240" />
        <el-table-column label="阶段" width="100"><template #default="{ row }">{{ row.phase === 'preflight' ? '预检' : '导入' }}</template></el-table-column>
        <el-table-column label="状态" width="120"><template #default="{ row }">{{ labels[row.state] ?? row.state }}</template></el-table-column>
        <el-table-column label="结果" min-width="180"><template #default="{ row }">{{ row.last_error || `总行数 ${row.result?.total ?? '—'}，错误 ${row.result?.error_count ?? '—'}` }}</template></el-table-column>
        <el-table-column label="操作" min-width="220"><template #default="{ row }">
          <el-button v-if="row.state === 'awaiting_confirmation' && row.result?.error_count === 0" link type="primary" :disabled="busy" @click="act(row, 'confirm')">确认导入</el-button>
          <el-button v-if="row.result?.error_download_url" link @click="downloadErrors(row)">错误报告</el-button>
          <el-button v-if="['pending', 'failed', 'awaiting_confirmation'].includes(row.state) || (row.phase === 'preflight' && row.state === 'running')" link type="danger" :disabled="busy" @click="act(row, 'cancel')">取消</el-button>
        </template></el-table-column>
      </el-table>
    </el-collapse-item>
  </el-collapse>
</template>
