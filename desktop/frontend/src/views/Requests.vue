<script setup>
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { ClearRequests, GetRequestDetail, GetRequestSummaries } from '../../wailsjs/go/main/App.js'
import { ClipboardSetText, EventsOff, EventsOn } from '../../wailsjs/runtime'
import JsonViewer from '../components/JsonViewer.vue'

const props = defineProps({
  selectedRequestId: {
    type: String,
    default: null
  },
  initialRequests: {
    type: Array,
    default: () => []
  }
})

const emit = defineEmits(['notice', 'request-selected'])

const requests = ref(Array.isArray(props.initialRequests) ? [...props.initialRequests] : [])
const selectedKey = ref(null)
const selectedDetail = ref(null)
const detailLoading = ref(false)
const query = ref('')
const activeStatus = ref('all')
const pendingSelectId = ref(null)
const loading = ref(requests.value.length === 0)
const activeDetailPane = ref('request')
const requestViewerRef = ref(null)
const responseViewerRef = ref(null)
let refreshTimer = null

function requestKey(request) {
  return request?.id || request?.createdAt || request?.time || ''
}

const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  return requests.value.filter((request) => {
    const matchesQuery = !q || `${request.method} ${request.path} ${request.statusCode} ${request.model || ''}`.toLowerCase().includes(q)
    const code = Number(request.statusCode)
    const matchesStatus =
      activeStatus.value === 'all' ||
      (activeStatus.value === 'ok' && code >= 200 && code < 300) ||
      (activeStatus.value === 'err' && code >= 400) ||
      (activeStatus.value === 'warn' && code >= 300 && code < 400)
    return matchesQuery && matchesStatus
  })
})

async function refresh() {
  if (requests.value.length === 0) {
    loading.value = true
  }
  try {
    requests.value = await GetRequestSummaries()
    if (pendingSelectId.value) {
      selectRequestById(pendingSelectId.value)
      pendingSelectId.value = null
    }
  } catch (e) {
    console.debug('Wails GetRequestSummaries unavailable in browser preview')
  } finally {
    loading.value = false
  }
}

async function clear() {
  try {
    await ClearRequests()
  } catch (e) {
    console.debug('Wails ClearRequests unavailable in browser preview')
  }
  requests.value = []
  selectedKey.value = null
  selectedDetail.value = null
}

function statusClass(code) {
  if (code >= 200 && code < 300) return 'ok'
  if (code >= 400) return 'err'
  return 'warn'
}

async function loadDetail(requestId) {
  const key = (requestId || '').trim()
  if (!key) {
    selectedDetail.value = null
    return
  }
  detailLoading.value = true
  try {
    selectedDetail.value = await GetRequestDetail(key)
  } catch (e) {
    console.debug('Wails GetRequestDetail unavailable in browser preview')
    selectedDetail.value = requests.value.find((item) => requestKey(item) === key) || null
  } finally {
    detailLoading.value = false
  }
}

async function selectRow(request) {
  const key = requestKey(request)
  if (!key) return
  if (selectedKey.value === key) {
    selectedKey.value = null
    selectedDetail.value = null
    return
  }
  selectedKey.value = key
  await loadDetail(key)
}

function selectRequestById(requestId) {
  if (!requestId) return
  const match = filtered.value.find((req) => req.id === requestId || req.createdAt === requestId || req.time === requestId)
  if (match) {
    selectedKey.value = requestKey(match)
    loadDetail(requestKey(match))
    emit('request-selected', requestId)
    setTimeout(() => {
      const row = document.querySelector('.data-table tbody tr.selected')
      row?.scrollIntoView({ behavior: 'smooth', block: 'center' })
    }, 100)
  } else {
    pendingSelectId.value = requestId
    emit('request-selected', requestId)
  }
}

watch(() => props.selectedRequestId, (newId) => {
  if (newId) {
    selectRequestById(newId)
  }
}, { immediate: true })

watch(() => props.initialRequests, (nextRequests) => {
  if (!Array.isArray(nextRequests) || nextRequests.length === 0) return
  requests.value = [...nextRequests]
  loading.value = false
}, { deep: true })

watch(() => requests.value.length, (newLength, oldLength) => {
  if (newLength > 0 && oldLength === 0 && pendingSelectId.value) {
    selectRequestById(pendingSelectId.value)
    pendingSelectId.value = null
  }
})

async function writeClipboard(text) {
  const value = text || ''
  try {
    await ClipboardSetText(value)
    return true
  } catch (e) {
    await navigator.clipboard?.writeText(value)
    return true
  }
}

async function copyText(text, label) {
  try {
    await writeClipboard(text)
    emit('notice', `已复制${label}`)
  } catch (e) {
    console.debug('Copy failed:', e)
    emit('notice', `${label}复制失败`)
  }
}

function safeEventsOn(name, handler) {
  try {
    EventsOn(name, handler)
  } catch (e) {
    console.debug('Wails runtime event unavailable:', name)
  }
}

function safeEventsOff(name) {
  try {
    EventsOff(name)
  } catch (e) {
    console.debug('Wails runtime event unavailable:', name)
  }
}

function scheduleRefresh() {
  clearTimeout(refreshTimer)
  refreshTimer = setTimeout(() => {
    refresh()
  }, 180)
}

function formatDateTime(request) {
  if (request.createdAt) {
    try {
      const date = new Date(request.createdAt)
      const now = new Date()
      const isToday = date.toDateString() === now.toDateString()
      const yesterday = new Date(now)
      yesterday.setDate(yesterday.getDate() - 1)
      const isYesterday = date.toDateString() === yesterday.toDateString()

      const timeStr = date.toLocaleTimeString('zh-CN', { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' })

      if (isToday) {
        return `今天 ${timeStr}`
      } else if (isYesterday) {
        return `昨天 ${timeStr}`
      } else {
        const dateStr = date.toLocaleDateString('zh-CN', { month: '2-digit', day: '2-digit' })
        return `${dateStr} ${timeStr}`
      }
    } catch (e) {
      return request.time || '-'
    }
  }
  return request.time || '-'
}

function rowSubText(request) {
  return request?.hasReqBody ? '包含请求体' : '无请求体'
}

function setActiveDetailPane(pane) {
  activeDetailPane.value = pane
}

function activeViewer() {
  return activeDetailPane.value === 'response' ? responseViewerRef.value : requestViewerRef.value
}

function onGlobalKeydown(event) {
  if (!selectedKey.value) return
  const shortcut = (event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'f'
  if (!shortcut) return
  event.preventDefault()
  activeViewer()?.toggleFinder?.()
}

onMounted(() => {
  refresh()
  safeEventsOn('requests:updated', () => {
    scheduleRefresh()
  })
  window.addEventListener('keydown', onGlobalKeydown)
})

onUnmounted(() => {
  clearTimeout(refreshTimer)
  safeEventsOff('requests:updated')
  window.removeEventListener('keydown', onGlobalKeydown)
})
</script>

<template>
  <div class="page requests-page">
    <div class="page-title">
      <div>
        <h1>请求流</h1>
        <p>查看客户端调用 OpenAI / Anthropic 兼容接口的请求与响应。</p>
      </div>
      <div class="toolbar">
        <button class="secondary-button" type="button" @click="refresh">刷新</button>
        <button class="danger-button" type="button" @click="clear">清空</button>
      </div>
    </div>

    <section class="table-panel requests-panel">
      <div class="table-toolbar">
        <div class="toolbar-search-wrap">
          <input v-model="query" class="search-input toolbar-search-input" type="search" placeholder="搜索路径、方法或状态码" />
          <span class="muted toolbar-count">Showing {{ filtered.length }} of {{ requests.length }}</span>
        </div>
        <div class="segmented">
          <button :class="{ active: activeStatus === 'all' }" type="button" @click="activeStatus = 'all'">全部</button>
          <button :class="{ active: activeStatus === 'ok' }" type="button" @click="activeStatus = 'ok'">成功</button>
          <button :class="{ active: activeStatus === 'warn' }" type="button" @click="activeStatus = 'warn'">跳转</button>
          <button :class="{ active: activeStatus === 'err' }" type="button" @click="activeStatus = 'err'">错误</button>
        </div>
      </div>

      <div v-if="filtered.length > 0" class="table-scroll hidden-scrollbar">
        <table class="data-table">
          <thead>
            <tr>
              <th>时间</th>
              <th>方法</th>
              <th>路径</th>
              <th>模型</th>
              <th>状态</th>
              <th>耗时</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="(request, index) in filtered"
              :key="request.id || request.createdAt || `${request.time}-${index}`"
              :class="{ selected: selectedKey === requestKey(request) }"
              @click="selectRow(request)"
            >
              <td>{{ formatDateTime(request) }}</td>
              <td><span class="method-chip">{{ request.method }}</span></td>
              <td>
                <div class="cell-main">{{ request.path }}</div>
                <div class="cell-sub">{{ rowSubText(request) }}</div>
              </td>
              <td>{{ request.model || '-' }}</td>
              <td><span class="status-chip" :class="statusClass(request.statusCode)">{{ request.statusCode }}</span></td>
              <td>{{ request.duration }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-else-if="loading" class="empty-state">加载请求记录中...</div>
      <div v-else class="empty-state">暂无匹配请求。</div>

      <div v-if="selectedKey" class="detail-panel hidden-scrollbar">
        <div v-if="detailLoading" class="empty-state">加载完整请求详情中...</div>
        <template v-else>
          <div class="detail-section">
            <div class="detail-toolbar">
              <h3>请求内容</h3>
              <div class="detail-actions">
                <button type="button" class="ghost-button" @click="copyText(selectedDetail?.reqBody, '请求内容')">
                  复制
                </button>
              </div>
            </div>
            <JsonViewer
              ref="requestViewerRef"
              :body="selectedDetail?.reqBody"
              empty-text="空请求体"
              @activated="setActiveDetailPane('request')"
            />
          </div>
          <div class="detail-section">
            <div class="detail-toolbar">
              <h3>响应内容</h3>
              <div class="detail-actions">
                <button type="button" class="ghost-button" @click="copyText(selectedDetail?.respBody, '响应内容')">
                  复制
                </button>
              </div>
            </div>
            <JsonViewer
              ref="responseViewerRef"
              :body="selectedDetail?.respBody"
              empty-text="空响应体"
              @activated="setActiveDetailPane('response')"
            />
          </div>
        </template>
      </div>
    </section>
  </div>
</template>
