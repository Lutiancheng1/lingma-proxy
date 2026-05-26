<script setup>
import { computed, ref } from 'vue'
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
const sourceFilter = ref('all')
const search = ref('')
const clearConfirmOpen = ref(false)

const visibleLogs = computed(() => {
  return props.logs.filter((log) => (log.source || 'app') !== 'boot')
})

const filteredLogs = computed(() => {
  const q = search.value.trim().toLowerCase()
  return visibleLogs.value.filter((log) => {
    const matchesLevel = filter.value === 'all' || log.level === filter.value
    const matchesSource = sourceFilter.value === 'all' || (sourceFilter.value === 'feishu-bridge' && log.source === 'feishu-bridge')
    const displayTime = formatLogDateTime(log)
    const matchesSearch = !q || `${displayTime} ${log.time || ''} ${log.source || 'app'} ${log.level} ${log.message} ${log.sessionId || ''} ${log.chatId || ''}`.toLowerCase().includes(q)
    return matchesLevel && matchesSource && matchesSearch
  })
})

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
    'feishu-bridge': 'Feishu Bridge',
  }[source || 'app'] || (source || '应用')
}

function shortID(value) {
  const text = String(value || '').trim()
  if (!text) return ''
  if (text.length <= 14) return text
  return `${text.slice(0, 6)}...${text.slice(-4)}`
}

function hasLogMeta(log) {
  return Boolean(String(log?.sessionId || '').trim() || String(log?.chatId || '').trim())
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

function serializeLogs() {
  return filteredLogs.value
    .map((log) => {
      const session = shortID(log.sessionId)
      const chat = shortID(log.chatId)
      const meta = [
        session ? `session=${session}` : '',
        chat ? `chat=${chat}` : '',
      ].filter(Boolean)
      const metaText = meta.length > 0 ? ` [${meta.join('] [')}]` : ''
      return `[${formatLogDateTime(log)}] [${sourceLabel(log.source)}] ${levelLabel(log.level)}${metaText} ${log.message}`
    })
    .join('\n')
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

function confirmClearLogs() {
  if (props.logs.length === 0) return
  clearConfirmOpen.value = true
}

function cancelClearLogs() {
  clearConfirmOpen.value = false
}

function proceedClearLogs() {
  clearConfirmOpen.value = false
  emit('clear')
}
</script>

<template>
  <div class="page logs-page">
    <div class="page-title">
      <div>
        <h1>日志</h1>
        <p>记录代理启动、模型同步、健康检查、配置保存，以及 Feishu Bridge 运行事件。</p>
      </div>
      <div class="toolbar">
        <button class="secondary-button" type="button" :disabled="filteredLogs.length === 0" @click="copyLogs">复制摘要</button>
        <button class="danger-button" type="button" :disabled="props.logs.length === 0" @click="confirmClearLogs">清空日志</button>
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
        <div class="segmented">
          <button :class="{ active: sourceFilter === 'all' }" type="button" @click="sourceFilter = 'all'">全部来源</button>
          <button :class="{ active: sourceFilter === 'feishu-bridge' }" type="button" @click="sourceFilter = 'feishu-bridge'">Feishu Bridge</button>
        </div>
        <input v-model="search" class="search-input" type="search" placeholder="搜索来源、级别、会话 ID、Chat ID 或日志内容" />
      </div>

      <div v-if="filteredLogs.length > 0" class="log-list hidden-scrollbar">
        <div class="log-row log-header">
          <span>Time</span>
          <span>Source</span>
          <span>Level</span>
          <span class="log-meta-cell">Session ID</span>
          <span class="log-meta-cell">Chat ID</span>
          <span>Message</span>
        </div>
        <div
          v-for="(log, index) in filteredLogs"
          :key="log.createdAt || index"
          class="log-row"
        >
          <span class="muted">{{ formatLogDateTime(log) }}</span>
          <span class="log-source-chip">{{ sourceLabel(log.source) }}</span>
          <strong :class="levelClass(log.level)">{{ levelLabel(log.level) }}</strong>
          <span class="log-meta-cell">{{ shortID(log.sessionId) || '' }}</span>
          <span class="log-meta-cell">{{ shortID(log.chatId) || '' }}</span>
          <span>{{ log.message }}</span>
        </div>
      </div>
      <div v-else class="empty-state">暂无日志。</div>
    </section>

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
