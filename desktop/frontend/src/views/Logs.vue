<script setup>
import { computed, ref } from 'vue'
import { GetLogDetail } from '../../wailsjs/go/main/App.js'
import { ClipboardSetText } from '../../wailsjs/runtime'
import ConfirmDialog from '../components/ConfirmDialog.vue'

const props = defineProps({
  logs: {
    type: Array,
    default: () => []
  }
})

const emit = defineEmits(['clear', 'notice'])

const filter = ref('all')
const search = ref('')
const clearConfirmOpen = ref(false)
const detailOpen = ref(false)
const detailLoading = ref(false)
const selectedLog = ref(null)
const rangeAnchorIndex = ref(-1)
const rangeEndIndex = ref(-1)
const detailCache = new Map()

const filteredLogs = computed(() => {
  const q = search.value.trim().toLowerCase()
  return props.logs.filter((log) => {
    if ((log.source || '').toLowerCase() === 'boot') return false
    const matchesLevel = filter.value === 'all' || log.level === filter.value
    const matchesSearch = !q || `${formatLogDateTime(log)} ${log.time || ''} ${log.source || 'app'} ${log.level} ${log.message}`.toLowerCase().includes(q)
    return matchesLevel && matchesSearch
  })
})

const selectedRangeLogs = computed(() => {
  const start = rangeAnchorIndex.value
  const end = rangeEndIndex.value
  if (start < 0 || end < 0 || start >= filteredLogs.value.length || end >= filteredLogs.value.length) return []
  const from = Math.min(start, end)
  const to = Math.max(start, end)
  return filteredLogs.value.slice(from, to + 1)
})

function logKey(log) {
  return log?.id || log?.createdAt || `${log?.time || ''}-${log?.source || ''}-${log?.level || ''}-${log?.message || ''}`
}

function isIndexInSelectedRange(index) {
  const start = rangeAnchorIndex.value
  const end = rangeEndIndex.value
  if (start < 0 || end < 0) return false
  return index >= Math.min(start, end) && index <= Math.max(start, end)
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

function sourceLabel(source) {
  return {
    app: '应用',
  }[source || 'app'] || (source || '应用')
}

function formatLogDateTime(log) {
  if (log?.createdAt) {
    const date = new Date(log.createdAt)
    if (!Number.isNaN(date.getTime())) {
      return formatRelativeDateTime(date)
    }
  }
  return log?.time || '-'
}

function formatRelativeDateTime(date) {
  const now = new Date()
  const yesterday = new Date(now)
  yesterday.setDate(yesterday.getDate() - 1)
  const timeStr = date.toLocaleTimeString('zh-CN', {
    hour12: false,
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit'
  })
  if (date.toDateString() === now.toDateString()) {
    return `今天 ${timeStr}`
  }
  if (date.toDateString() === yesterday.toDateString()) {
    return `昨天 ${timeStr}`
  }
  const dateStr = date.toLocaleDateString('zh-CN', {
    month: '2-digit',
    day: '2-digit'
  })
  return `${dateStr} ${timeStr}`
}

function serializeLog(log) {
  const timestamp = formatLogDateTime(log)
  return `[${timestamp}] [${sourceLabel(log.source)}] ${levelLabel(log.level)} ${log.message || ''}`
}

function serializeLogs(logs) {
  return logs.map((log) => serializeLog(log)).join('\n')
}

async function writeClipboard(text) {
  try {
    await ClipboardSetText(text || '')
    return true
  } catch (e) {
    await navigator.clipboard?.writeText(text || '')
    return true
  }
}

async function loadFullLog(log) {
  const key = logKey(log)
  if (!key) return log
  if (detailCache.has(key)) return detailCache.get(key)
  if (!log.createdAt) {
    detailCache.set(key, log)
    return log
  }
  try {
    const detail = await GetLogDetail(log.id || log.createdAt)
    detailCache.set(key, detail || log)
    return detail || log
  } catch (e) {
    console.debug('Load log detail failed:', e)
    detailCache.set(key, log)
    return log
  }
}

async function loadFullLogs(logs) {
  const out = []
  for (const log of logs) {
    out.push(await loadFullLog(log))
  }
  return out
}

async function copyLogs() {
  try {
    const fullLogs = await loadFullLogs(filteredLogs.value)
    await writeClipboard(serializeLogs(fullLogs))
    emit('notice', `已复制 ${filteredLogs.value.length} 条完整日志`)
  } catch (e) {
    console.debug('Copy logs failed:', e)
    emit('notice', '日志复制失败')
  }
}

async function copySelectedRange() {
  const logs = selectedRangeLogs.value
  if (logs.length === 0) return
  try {
    const fullLogs = await loadFullLogs(logs)
    await writeClipboard(serializeLogs(fullLogs))
    emit('notice', `已复制 ${fullLogs.length} 条完整日志`)
  } catch (e) {
    console.debug('Copy selected logs failed:', e)
    emit('notice', '区间日志复制失败')
  }
}

function selectRangePoint(index) {
  if (index < 0 || index >= filteredLogs.value.length) return
  if (isIndexInSelectedRange(index)) {
    clearRangeSelection()
    return
  }
  if (rangeAnchorIndex.value < 0 || rangeEndIndex.value < 0) {
    rangeAnchorIndex.value = index
    rangeEndIndex.value = index
    return
  }
  rangeEndIndex.value = index
}

function clearRangeSelection() {
  rangeAnchorIndex.value = -1
  rangeEndIndex.value = -1
}

function isRangeSelected(index) {
  return isIndexInSelectedRange(index)
}

async function openLogDetail(log) {
  detailOpen.value = true
  selectedLog.value = log
  detailLoading.value = true
  try {
    selectedLog.value = await loadFullLog(log)
  } finally {
    detailLoading.value = false
  }
}

function closeLogDetail() {
  detailOpen.value = false
}

async function copySelectedDetail() {
  if (!selectedLog.value) return
  try {
    await writeClipboard(serializeLog(selectedLog.value))
    emit('notice', '已复制完整日志')
  } catch (e) {
    console.debug('Copy log detail failed:', e)
    emit('notice', '完整日志复制失败')
  }
}

function confirmClearLogs() {
  if (props.logs.length === 0) return
  clearConfirmOpen.value = true
}

function cancelClearLogs() {
  clearConfirmOpen.value = false
}

function proceedClearLogs() {
  clearConfirmOpen.value = false
  clearRangeSelection()
  detailOpen.value = false
  detailCache.clear()
  emit('clear')
}
</script>

<template>
  <div class="page logs-page">
    <div class="page-title">
      <div>
        <h1>日志</h1>
        <p>记录代理启动、模型同步、健康检查和配置保存事件。</p>
      </div>
      <div class="toolbar">
        <button class="secondary-button" type="button" :disabled="filteredLogs.length === 0" @click="copyLogs">复制完整筛选结果</button>
        <button class="secondary-button" type="button" :disabled="selectedRangeLogs.length === 0" @click="copySelectedRange">复制区间</button>
        <button class="danger-button" type="button" :disabled="props.logs.length === 0" @click="confirmClearLogs">清空日志</button>
      </div>
    </div>

    <section class="table-panel logs-panel">
      <div class="table-toolbar">
        <div class="logs-toolbar-left">
          <div class="segmented">
            <button :class="{ active: filter === 'all' }" type="button" @click="filter = 'all'">全部</button>
            <button :class="{ active: filter === 'info' }" type="button" @click="filter = 'info'">信息</button>
            <button :class="{ active: filter === 'warn' }" type="button" @click="filter = 'warn'">警告</button>
            <button :class="{ active: filter === 'error' }" type="button" @click="filter = 'error'">错误</button>
          </div>
        </div>
        <div class="logs-toolbar-right">
          <button class="ghost-button" type="button" :disabled="selectedRangeLogs.length === 0" @click="clearRangeSelection">清除选择</button>
          <input v-model="search" class="search-input" type="search" placeholder="搜索来源、级别或日志内容" />
        </div>
      </div>

      <div v-if="filteredLogs.length > 0" class="log-list hidden-scrollbar">
        <div class="log-row log-header">
          <span></span>
          <span>Time</span>
          <span>Source</span>
          <span>Level</span>
          <span>Message</span>
          <span></span>
        </div>
        <div
          v-for="(log, index) in filteredLogs"
          :key="logKey(log) || index"
          class="log-row"
          :class="{ selected: isRangeSelected(index) }"
        >
          <button
            class="log-range-dot"
            type="button"
            :title="isRangeSelected(index) ? '点击取消当前区间' : '选择为区间端点'"
            :class="{ active: isRangeSelected(index) }"
            @click.stop="selectRangePoint(index)"
          ></button>
          <span class="muted">{{ formatLogDateTime(log) }}</span>
          <span class="log-source-chip">{{ sourceLabel(log.source) }}</span>
          <strong :class="levelClass(log.level)">{{ levelLabel(log.level) }}</strong>
          <span class="log-message-cell">{{ log.message }}</span>
          <button class="ghost-button log-detail-button" type="button" @click="openLogDetail(log)">详情</button>
        </div>
      </div>
      <div v-else class="empty-state">暂无日志。</div>
    </section>

    <div v-if="detailOpen" class="modal-backdrop" @click.self="closeLogDetail">
      <section class="modal-card log-detail-modal">
        <header class="modal-header">
          <div>
            <h2>完整日志详情</h2>
            <p>{{ formatLogDateTime(selectedLog) }} · {{ sourceLabel(selectedLog?.source) }} · {{ levelLabel(selectedLog?.level) }}</p>
          </div>
          <button class="icon-button" type="button" title="关闭" @click="closeLogDetail">
            <i class="bi bi-x-lg"></i>
          </button>
        </header>
        <div class="modal-body log-detail-body">
          <div v-if="detailLoading" class="empty-state">加载完整日志中...</div>
          <pre v-else class="log-detail-content">{{ serializeLog(selectedLog || {}) }}</pre>
        </div>
        <footer class="modal-footer">
          <button class="secondary-button" type="button" @click="closeLogDetail">关闭</button>
          <button class="primary-button" type="button" :disabled="!selectedLog" @click="copySelectedDetail">复制完整日志</button>
        </footer>
      </section>
    </div>

    <ConfirmDialog
      :open="clearConfirmOpen"
      title="确认清空日志"
      message="当前日志列表会被立即清空，且无法恢复。"
      confirm-label="确认清空"
      @cancel="cancelClearLogs"
      @confirm="proceedClearLogs"
    />
  </div>
</template>
