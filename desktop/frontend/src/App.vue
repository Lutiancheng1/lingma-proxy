<script setup>
import { onMounted, onUnmounted, reactive, ref } from 'vue'
import Dashboard from './views/Dashboard.vue'
import Logs from './views/Logs.vue'
import Models from './views/Models.vue'
import Requests from './views/Requests.vue'
import Settings from './views/Settings.vue'
import ConfirmDialog from './components/ConfirmDialog.vue'
import { ClipboardSetText } from '../wailsjs/runtime'
import { ChooseFeedbackExportPath, ClearLogs, ExportFeedbackBundle, ForceQuitApp, GetAppVersion, GetLogSummaries, GetRequestSummaries, GetStatus, HideWindow, MinimizeWindow, OpenPathInFileManager } from '../wailsjs/go/main/App.js'
import lingmaIcon from './assets/images/lingma-icon.png'
import qoderCNIcon from './assets/images/qodercn-mark.png'
import { safeEventsOff, safeEventsOn, safeInvoke } from './utils/wailsSafe'

const currentTab = ref('dashboard')
const selectedRequestId = ref(null)
const requestSnapshot = ref([])
const logs = ref([])
const status = ref({ running: false, addr: '', models: 0 })
const appVersion = ref('dev')
const toast = ref('')
const themeMode = ref(localStorage.getItem('lingma-theme-mode') || 'system')
const appliedTheme = ref('light')
const forceQuitting = ref(false)
const forceQuitConfirmOpen = ref(false)
const brandFlipped = ref(false)
const brandAnimating = ref(false)
const brandFlipDirection = ref('')
const feedbackOpen = ref(false)
const feedbackStep = ref('form')
const feedbackBusy = ref(false)
const feedbackResult = ref(null)
const feedbackRanges = [
  { value: '30m', label: '最近 30 分钟' },
  { value: '2h', label: '最近 2 小时' },
  { value: '24h', label: '最近 24 小时' },
  { value: '7d', label: '最近 7 天' },
  { value: 'all', label: '全部' },
  { value: 'custom', label: '自定义时间段' },
]
const feedbackForm = reactive({
  rangePreset: '30m',
  startAt: '',
  endAt: '',
  includeAppLogs: true,
  includeRequests: true,
  includeConfigSummary: true,
  includeEnvironment: true,
  includeDetectionInfo: true,
  issueDescription: '',
  savePath: '',
})
let systemThemeQuery = null
let toastTimer = null
let requestsRefreshTimer = null

const navigation = [
  { key: 'dashboard', label: '仪表盘', icon: 'bi-house-door' },
  { key: 'requests', label: '请求流', icon: 'bi-file-earmark-text' },
  { key: 'models', label: '模型', icon: 'bi-box' },
  { key: 'settings', label: '设置', icon: 'bi-gear' },
  { key: 'logs', label: '日志', icon: 'bi-terminal' },
]

function addLog(level, message, source = 'app') {
  const time = new Date().toLocaleTimeString('zh-CN', { hour12: false })
  logs.value.unshift({ time, level, source, message })
  if (logs.value.length > 500) {
    logs.value = logs.value.slice(0, 500)
  }
}

function showToast(message) {
  toast.value = message
  clearTimeout(toastTimer)
  toastTimer = setTimeout(() => {
    toast.value = ''
  }, 2200)
}

async function clearLocalLogs() {
  await safeInvoke(() => ClearLogs(), undefined, 'ClearLogs unavailable in app shell')
  logs.value = []
}

function setStatus(nextStatus) {
  status.value = nextStatus
}

function handleNotice(message) {
  showToast(message)
  addLog('info', message)
}

function handleOpenRequestDetail(requestId) {
  selectedRequestId.value = requestId
  currentTab.value = 'requests'
}

function handleRequestSelectionAck() {
  selectedRequestId.value = null
}

function resolveTheme() {
  if (themeMode.value === 'system') {
    return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
  }
  return themeMode.value
}

function applyTheme() {
  appliedTheme.value = resolveTheme()
  document.documentElement.dataset.theme = appliedTheme.value
  localStorage.setItem('lingma-theme-mode', themeMode.value)
}

function toggleTheme() {
  const modes = ['system', 'light', 'dark']
  const index = modes.indexOf(themeMode.value)
  themeMode.value = modes[(index + 1) % modes.length]
  applyTheme()
}

function themeTitle() {
  if (themeMode.value === 'system') return `跟随系统（当前${appliedTheme.value === 'dark' ? '夜间' : '日间'}）`
  return themeMode.value === 'dark' ? '夜间模式' : '日间模式'
}

function themeIcon() {
  if (themeMode.value === 'system') return 'bi-circle-half'
  return themeMode.value === 'dark' ? 'bi-moon-stars' : 'bi-sun'
}

async function refreshStatus() {
  try {
    status.value = await GetStatus()
  } catch (e) {
    addLog('error', '状态刷新失败：' + (e.message || String(e)))
  }
}

async function primeRequests() {
  const items = await safeInvoke(() => GetRequestSummaries(), [], 'GetRequestSummaries unavailable in app shell')
  requestSnapshot.value = Array.isArray(items) ? items : []
}

function scheduleRequestSnapshotRefresh() {
  clearTimeout(requestsRefreshTimer)
  requestsRefreshTimer = setTimeout(() => {
    if (currentTab.value === 'requests' || requestSnapshot.value.length === 0) {
      primeRequests()
    }
  }, 180)
}

async function copyEndpoint() {
  if (!status.value.addr) return
  const value = `http://${status.value.addr}`
  await navigator.clipboard?.writeText(value)
  handleNotice('已复制接口地址：' + value)
}

async function forceQuitApp() {
  if (forceQuitting.value) return
  forceQuitting.value = true
  forceQuitConfirmOpen.value = false
  showToast('正在停止代理并退出应用...')
  try {
    await ForceQuitApp()
  } catch (e) {
    forceQuitting.value = false
    addLog('error', '退出应用失败：' + (e.message || String(e)))
  }
}

function confirmForceQuit() {
  if (forceQuitting.value) return
  forceQuitConfirmOpen.value = true
}

function cancelForceQuit() {
  forceQuitConfirmOpen.value = false
}

function handleBrandClick() {
  if (brandAnimating.value) return
  brandFlipDirection.value = brandFlipped.value ? 'to-qoder' : 'to-lingma'
  brandAnimating.value = true
  brandFlipped.value = !brandFlipped.value
  currentTab.value = 'dashboard'
  window.setTimeout(() => {
    brandAnimating.value = false
    brandFlipDirection.value = ''
  }, 560)
}

function openFeedbackDialog() {
  feedbackOpen.value = true
  feedbackStep.value = 'form'
  feedbackResult.value = null
}

function closeFeedbackDialog() {
  if (feedbackBusy.value) return
  feedbackOpen.value = false
}

async function chooseFeedbackPath() {
  try {
    const path = await ChooseFeedbackExportPath()
    if (path) {
      feedbackForm.savePath = path
    }
  } catch (e) {
    addLog('error', '选择反馈包保存位置失败：' + (e.message || String(e)))
  }
}

async function exportFeedbackBundle() {
  feedbackBusy.value = true
  try {
    const result = await ExportFeedbackBundle({ ...feedbackForm })
    if (!result?.zipPath) {
      feedbackBusy.value = false
      return
    }
    feedbackResult.value = result
    feedbackForm.savePath = result.zipPath
    feedbackStep.value = 'result'
    handleNotice('反馈日志包已生成')
  } catch (e) {
    addLog('error', '导出反馈日志包失败：' + (e.message || String(e)))
    showToast('导出反馈日志包失败')
  } finally {
    feedbackBusy.value = false
  }
}

async function openFeedbackFolder() {
  if (!feedbackResult.value?.zipPath) return
  try {
    await OpenPathInFileManager(feedbackResult.value.zipPath)
  } catch (e) {
    addLog('error', '打开反馈包目录失败：' + (e.message || String(e)))
  }
}

async function copyValue(value, label) {
  try {
    await ClipboardSetText(value || '')
    showToast(`已复制${label}`)
  } catch (e) {
    try {
      await navigator.clipboard?.writeText(value || '')
      showToast(`已复制${label}`)
    } catch (fallbackError) {
      addLog('error', `${label}复制失败：` + (fallbackError.message || String(fallbackError)))
    }
  }
}

function handleAppShortcut(event) {
  const key = event.key.toLowerCase()
  if ((event.metaKey || event.ctrlKey) && key === 'w') {
    event.preventDefault()
    HideWindow()
  }
  if ((event.metaKey || event.ctrlKey) && key === 'm') {
    event.preventDefault()
    MinimizeWindow()
  }
  // Fallback copy for WebView where native Edit menu is unavailable
  if ((event.metaKey || event.ctrlKey) && key === 'c') {
    const selection = window.getSelection()?.toString()
    if (selection) {
      event.preventDefault()
      navigator.clipboard?.writeText(selection).catch(() => {})
    }
  }
  // Fallback select-all for log/request content areas
  if ((event.metaKey || event.ctrlKey) && key === 'a') {
    const active = document.activeElement
    const isEditable = active && (active.tagName === 'INPUT' || active.tagName === 'TEXTAREA' || active.isContentEditable)
    if (!isEditable) {
      const panel = document.querySelector('.log-list, .request-list, .detail-panel')
      if (panel) {
        event.preventDefault()
        const range = document.createRange()
        range.selectNodeContents(panel)
        const sel = window.getSelection()
        sel?.removeAllRanges()
        sel?.addRange(range)
      }
    }
  }
}

onMounted(() => {
  document.getElementById('boot-splash')?.remove()
  window.addEventListener('keydown', handleAppShortcut, true)
  systemThemeQuery = window.matchMedia?.('(prefers-color-scheme: dark)')
  systemThemeQuery?.addEventListener?.('change', applyTheme)
  applyTheme()
  refreshStatus()
  safeInvoke(() => GetAppVersion(), 'dev', 'GetAppVersion unavailable in app shell').then((value) => {
    if (value) appVersion.value = value
  })
  primeRequests()
  GetLogSummaries().then((items) => {
    logs.value = Array.isArray(items) ? items : []
  }).catch(() => {})
  safeEventsOn('models:updated', (data) => {
    status.value.models = Array.isArray(data) ? data.length : status.value.models
    addLog('info', `模型列表已更新：${status.value.models} 个模型`)
  })
  safeEventsOn('log', (data) => {
    if (data.time && data.message !== undefined) {
      logs.value.unshift(data)
      if (logs.value.length > 500) logs.value = logs.value.slice(0, 500)
    } else {
      addLog(data.level || 'info', data.message || '', data.source || 'app')
    }
  })
  safeEventsOn('logs:updated', (data) => {
    logs.value = Array.isArray(data) ? data : []
  })
  safeEventsOn('quit:confirm', (message) => {
    showToast(message || '再按一次退出快捷键将停止代理并退出应用')
  })
  safeEventsOn('app:confirm-force-quit', () => {
    confirmForceQuit()
  })
  safeEventsOn('status:updated', (nextStatus) => {
    status.value = nextStatus
  })
  safeEventsOn('requests:updated', () => {
    scheduleRequestSnapshotRefresh()
  })
})

onUnmounted(() => {
  window.removeEventListener('keydown', handleAppShortcut, true)
  clearTimeout(toastTimer)
  clearTimeout(requestsRefreshTimer)
  systemThemeQuery?.removeEventListener?.('change', applyTheme)
  safeEventsOff('models:updated')
  safeEventsOff('log')
  safeEventsOff('logs:updated')
  safeEventsOff('quit:confirm')
  safeEventsOff('app:confirm-force-quit')
  safeEventsOff('status:updated')
  safeEventsOff('requests:updated')
})
</script>

<template>
  <div class="app-shell">
    <aside class="sidebar">
      <button class="brand" type="button" @click="handleBrandClick">
        <span class="brand-mark brand-mark-duo" :class="[{ flipped: brandFlipped, animating: brandAnimating }, brandFlipDirection]" title="Lingma / QoderCN">
          <span class="brand-card brand-icon-lingma">
            <img :src="lingmaIcon" alt="" />
          </span>
          <span class="brand-card brand-icon-qoder">
            <img :src="qoderCNIcon" alt="" />
          </span>
        </span>
        <span>
          <strong>Lingma /<br />QoderCN</strong>
          <small>Proxy</small>
        </span>
      </button>

      <nav class="nav-list" aria-label="主导航">
        <button
          v-for="item in navigation"
          :key="item.key"
          class="nav-item"
          :class="{ active: currentTab === item.key }"
          type="button"
          @click="currentTab = item.key"
        >
          <span class="nav-icon">
            <i class="bi" :class="item.icon" aria-hidden="true"></i>
          </span>
          <span>{{ item.label }}</span>
        </button>
      </nav>

      <div class="sidebar-status">
        <span class="status-dot" :class="{ running: status.running }"></span>
        <div>
          <strong>{{ status.running ? 'Proxy Running' : 'Proxy Stopped' }}</strong>
          <small>v{{ appVersion }}</small>
        </div>
      </div>
    </aside>

    <section class="workspace">
      <header class="topbar">
        <span class="topbar-spacer" aria-hidden="true"></span>
        <div class="topbar-actions">
          <button class="icon-button" type="button" title="刷新状态" @click="refreshStatus">
            <i class="bi bi-arrow-clockwise" aria-hidden="true"></i>
          </button>
          <button class="icon-button" type="button" title="复制接口地址" @click="copyEndpoint">
            <i class="bi bi-copy" aria-hidden="true"></i>
          </button>
          <button class="icon-button" type="button" title="设置" @click="currentTab = 'settings'">
            <i class="bi bi-gear" aria-hidden="true"></i>
          </button>
          <button class="icon-button feedback-icon-button" type="button" title="反馈问题" @click="openFeedbackDialog">
            <i class="bi bi-bug" aria-hidden="true"></i>
          </button>
          <button class="icon-button" type="button" :title="themeTitle()" @click="toggleTheme">
            <i class="bi" :class="themeIcon()" aria-hidden="true"></i>
          </button>
          <button class="icon-button danger-icon-button" type="button" title="停止代理并退出应用" :disabled="forceQuitting" @click="confirmForceQuit">
            <i class="bi bi-power" aria-hidden="true"></i>
          </button>
        </div>
      </header>

      <main class="view-stage">
        <KeepAlive>
          <Dashboard
            v-if="currentTab === 'dashboard'"
            :shell-status="status"
            @log="addLog"
            @status="setStatus"
            @notice="handleNotice"
            @open-settings="currentTab = 'settings'"
            @open-requests="currentTab = 'requests'"
            @open-models="currentTab = 'models'"
            @open-request-detail="handleOpenRequestDetail"
          />
          <Requests
            v-else-if="currentTab === 'requests'"
            :selected-request-id="selectedRequestId"
            :initial-requests="requestSnapshot"
            @notice="handleNotice"
            @request-selected="handleRequestSelectionAck"
          />
        </KeepAlive>
        <Models v-if="currentTab === 'models'" @log="addLog" @status="setStatus" @notice="handleNotice" />
        <Settings v-else-if="currentTab === 'settings'" @log="addLog" @status-refresh="refreshStatus" />
        <Logs v-else-if="currentTab === 'logs'" :logs="logs" @clear="clearLocalLogs" @notice="handleNotice" />
      </main>
    </section>
    <div v-if="toast" class="toast">{{ toast }}</div>
    <div v-if="feedbackOpen" class="modal-backdrop" @click.self="closeFeedbackDialog">
      <section class="modal-card feedback-modal">
        <div class="modal-header">
          <div>
            <h2>反馈问题</h2>
            <p>导出脱敏后的反馈日志包，便于通过 GitHub Issue 或其他沟通渠道反馈问题。</p>
          </div>
          <button class="icon-button" type="button" title="关闭" :disabled="feedbackBusy" @click="closeFeedbackDialog">
            <i class="bi bi-x-lg" aria-hidden="true"></i>
          </button>
        </div>

        <div v-if="feedbackStep === 'form'" class="modal-body feedback-form">
          <div class="field">
            <label>收集范围</label>
            <div class="chip-options">
              <button
                v-for="range in feedbackRanges"
                :key="range.value"
                class="chip-option"
                :class="{ active: feedbackForm.rangePreset === range.value }"
                type="button"
                @click="feedbackForm.rangePreset = range.value"
              >
                {{ range.label }}
              </button>
            </div>
          </div>

          <div v-if="feedbackForm.rangePreset === 'custom'" class="form-grid">
            <div class="field">
              <label>开始时间</label>
              <input v-model="feedbackForm.startAt" type="datetime-local" />
            </div>
            <div class="field">
              <label>结束时间</label>
              <input v-model="feedbackForm.endAt" type="datetime-local" />
            </div>
          </div>

          <div class="field">
            <label>包含内容</label>
            <div class="checkbox-grid">
              <label class="checkbox-item">
                <input v-model="feedbackForm.includeAppLogs" type="checkbox" />
                <span>应用日志</span>
              </label>
              <label class="checkbox-item">
                <input v-model="feedbackForm.includeRequests" type="checkbox" />
                <span>请求日志</span>
              </label>
              <label class="checkbox-item">
                <input v-model="feedbackForm.includeConfigSummary" type="checkbox" />
                <span>当前配置摘要</span>
              </label>
              <label class="checkbox-item">
                <input v-model="feedbackForm.includeEnvironment" type="checkbox" />
                <span>运行环境信息</span>
              </label>
              <label class="checkbox-item">
                <input v-model="feedbackForm.includeDetectionInfo" type="checkbox" />
                <span>探测结果信息</span>
              </label>
            </div>
          </div>

          <div class="hint-box">
            <strong>隐私说明</strong>
            <span>默认脱敏 token、cookie、机器标识、图片 base64 和大请求体敏感字段；不打包明文登录态缓存，也不默认导出无限长完整请求体。</span>
          </div>

          <div class="field">
            <label>问题描述</label>
            <textarea v-model="feedbackForm.issueDescription" placeholder="补充描述你遇到的问题、触发步骤和预期结果。" />
          </div>

          <div class="field">
            <label>保存位置</label>
            <div class="inline-action-row">
              <input v-model="feedbackForm.savePath" type="text" readonly placeholder="点击右侧按钮选择导出位置" />
              <button class="secondary-button" type="button" @click="chooseFeedbackPath">选择位置</button>
            </div>
          </div>
        </div>

        <div v-else class="modal-body feedback-result">
          <div class="feedback-result-row">
            <label>已生成</label>
            <strong>{{ feedbackResult?.zipFilename }}</strong>
          </div>
          <div class="feedback-result-row">
            <label>保存位置</label>
            <code>{{ feedbackResult?.zipPath }}</code>
          </div>
          <div class="feedback-result-row">
            <label>导出内容</label>
            <span>{{ feedbackResult?.appLogCount }} 条应用日志，{{ feedbackResult?.requestCount }} 条请求记录</span>
          </div>
          <div class="hint-box">
            <strong>使用说明</strong>
            <span>请将刚生成的反馈压缩包提交到 GitHub Issue，或通过你自己的沟通渠道发送给维护者。软件本身不内置邮件发送能力。</span>
          </div>
          <div class="button-row wrap">
            <button class="primary-button" type="button" @click="openFeedbackFolder">打开文件所在目录</button>
            <button class="secondary-button" type="button" @click="copyValue(feedbackResult?.zipPath, '附件路径')">复制附件路径</button>
            <button class="secondary-button" type="button" @click="copyValue(feedbackResult?.shareText, '反馈说明')">复制反馈说明</button>
          </div>
        </div>

        <div class="modal-footer">
          <button v-if="feedbackStep === 'form'" class="secondary-button" type="button" :disabled="feedbackBusy" @click="closeFeedbackDialog">取消</button>
          <button v-else class="secondary-button" type="button" :disabled="feedbackBusy" @click="closeFeedbackDialog">关闭</button>
          <button v-if="feedbackStep === 'form'" class="primary-button" type="button" :disabled="feedbackBusy" @click="exportFeedbackBundle">
            {{ feedbackBusy ? '导出中...' : '导出反馈包' }}
          </button>
        </div>
      </section>
    </div>
    <ConfirmDialog
      :open="forceQuitConfirmOpen"
      title="确认退出应用"
      message="这会先停止代理，再退出 Lingma Proxy。"
      confirm-label="停止并退出"
      @cancel="cancelForceQuit"
      @confirm="forceQuitApp"
    />
  </div>
</template>
