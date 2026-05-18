<script setup>
import { computed, ref, watch } from 'vue'
import { GetLogDetail } from '../../wailsjs/go/main/App.js'
import { ClipboardSetText } from '../../wailsjs/runtime'

const props = defineProps({
  logs: {
    type: Array,
    default: () => []
  }
})

const emit = defineEmits(['clear', 'notice'])

const filter = ref('all')
const search = ref('')
const selectedKey = ref(null)
const selectedDetail = ref(null)
const detailLoading = ref(false)

const filteredLogs = computed(() => {
  const q = search.value.trim().toLowerCase()
  return props.logs.filter((log) => {
    const matchesLevel = filter.value === 'all' || log.level === filter.value
    const matchesSearch = !q || `${log.time} ${log.level} ${log.message}`.toLowerCase().includes(q)
    return matchesLevel && matchesSearch
  })
})

function logKey(log) {
  return log?.createdAt || `${log?.time || ''}-${log?.message || ''}`
}

function levelClass(level) {
  return {
    info: 'level-info',
    warn: 'level-warn',
    error: 'level-error'
  }[level] || 'level-info'
}

function levelLabel(level) {
  return {
    info: '信息',
    warn: '警告',
    error: '错误'
  }[level] || level
}

function serializeLogs() {
  return filteredLogs.value.map((log) => `[${log.time}] ${levelLabel(log.level)} ${log.message}`).join('\n')
}

async function copyLogs() {
  try {
    await ClipboardSetText(serializeLogs())
    emit('notice', `已复制 ${filteredLogs.value.length} 条日志摘要`)
  } catch (e) {
    try {
      await navigator.clipboard?.writeText(serializeLogs())
      emit('notice', `已复制 ${filteredLogs.value.length} 条日志摘要`)
    } catch (fallbackError) {
      console.debug('Copy logs failed:', fallbackError)
      emit('notice', '日志复制失败')
    }
  }
}

async function copyDetail() {
  const value = selectedDetail.value?.message || ''
  try {
    await ClipboardSetText(value)
    emit('notice', '已复制完整日志')
  } catch (e) {
    try {
      await navigator.clipboard?.writeText(value)
      emit('notice', '已复制完整日志')
    } catch (fallbackError) {
      console.debug('Copy log detail failed:', fallbackError)
      emit('notice', '完整日志复制失败')
    }
  }
}

async function loadDetail(createdAt) {
  const key = (createdAt || '').trim()
  if (!key) {
    selectedDetail.value = null
    return
  }
  detailLoading.value = true
  try {
    selectedDetail.value = await GetLogDetail(key)
  } catch (e) {
    console.debug('Wails GetLogDetail unavailable in browser preview')
    selectedDetail.value = props.logs.find((log) => logKey(log) === key) || null
  } finally {
    detailLoading.value = false
  }
}

async function selectLog(log) {
  const key = logKey(log)
  if (!key) return
  if (selectedKey.value === key) {
    selectedKey.value = null
    selectedDetail.value = null
    return
  }
  selectedKey.value = key
  await loadDetail(log.createdAt || '')
}

watch(() => props.logs, (nextLogs) => {
  if (!selectedKey.value || !Array.isArray(nextLogs)) return
  if (!nextLogs.some((log) => logKey(log) === selectedKey.value)) {
    selectedKey.value = null
    selectedDetail.value = null
  }
}, { deep: true })
</script>

<template>
  <div class="page logs-page">
    <div class="page-title">
      <div>
        <h1>日志</h1>
        <p>记录代理启动、模型同步、健康检查和配置保存事件。</p>
      </div>
      <div class="toolbar">
        <button class="secondary-button" type="button" :disabled="filteredLogs.length === 0" @click="copyLogs">复制摘要</button>
        <button class="danger-button" type="button" @click="emit('clear')">清空日志</button>
      </div>
    </div>

    <section class="table-panel logs-panel">
      <div class="table-toolbar">
        <div class="segmented">
          <button :class="{ active: filter === 'all' }" type="button" @click="filter = 'all'">全部</button>
          <button :class="{ active: filter === 'info' }" type="button" @click="filter = 'info'">信息</button>
          <button :class="{ active: filter === 'warn' }" type="button" @click="filter = 'warn'">警告</button>
          <button :class="{ active: filter === 'error' }" type="button" @click="filter = 'error'">错误</button>
        </div>
        <input v-model="search" class="search-input" type="search" placeholder="搜索日志内容" />
      </div>

      <div v-if="filteredLogs.length > 0" class="log-list hidden-scrollbar">
        <button
          v-for="(log, index) in filteredLogs"
          :key="log.createdAt || index"
          class="log-row log-row-button"
          :class="{ selected: selectedKey === logKey(log) }"
          type="button"
          @click="selectLog(log)"
        >
          <span class="muted">{{ log.time }}</span>
          <strong :class="levelClass(log.level)">{{ levelLabel(log.level) }}</strong>
          <span>{{ log.message }}</span>
        </button>
      </div>
      <div v-else class="empty-state">暂无日志。</div>

      <div v-if="selectedKey" class="detail-panel hidden-scrollbar">
        <div v-if="detailLoading" class="empty-state">加载完整日志中...</div>
        <div v-else class="detail-section">
          <div class="detail-toolbar">
            <h3>完整日志</h3>
            <div class="detail-actions">
              <button type="button" class="ghost-button" @click="copyDetail">复制</button>
            </div>
          </div>
          <pre>{{ selectedDetail?.message || '空日志内容' }}</pre>
        </div>
      </div>
    </section>
  </div>
</template>
