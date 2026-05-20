<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { BrowserOpenURL } from '../../wailsjs/runtime'
import {
  GetConfig,
  GetDetectionInfo,
  GetFeishuBridgeConfig,
  GetFeishuBridgeStatus,
  GetModels,
  InstallFeishuCLI,
  RefreshFeishuBridgeStatus,
  StartFeishuBridge,
  StartFeishuCLILogin,
  StartFeishuCLISetupNew,
  StopFeishuBridge,
  UpdateConfig,
  UpdateFeishuBridgeConfig,
} from '../../wailsjs/go/main/App.js'
import { safeEventsOff, safeEventsOn } from '../utils/wailsSafe'

const emit = defineEmits(['log', 'status-refresh', 'notice'])

const config = ref({})
const detection = ref(null)
const saving = ref(false)
const bridgeConfig = ref({
  enabled: false,
  autoStart: false,
  brand: 'feishu',
  model: 'kmodel',
  maxToolRounds: 5,
  setupMode: 'new',
})
const bridgeStatus = ref(null)
const bridgeStatusLoaded = ref(false)
const bridgeSaving = ref(false)
const bridgeBusy = ref(false)
const bridgeRefreshing = ref(false)
const openSelect = ref('')
const fallbackModelsText = ref('')
const availableBridgeModels = ref([])
const isIPCBackend = computed(() => (config.value.Backend || 'ipc') === 'ipc')
const formattedTokenExpireAt = computed(() => formatDateTime(detection.value?.remoteTokenExpireAt))
const bridgeCurrentModel = computed(() => bridgeConfig.value.model || bridgeStatus.value?.currentModel || 'kmodel')
const bridgeModelOptions = computed(() => {
  const items = Array.isArray(availableBridgeModels.value) ? [...availableBridgeModels.value] : []
  const currentID = String(bridgeConfig.value.model || bridgeStatus.value?.currentModel || '').trim()
  if (currentID && !items.some((item) => item.id === currentID)) {
    items.unshift({ id: currentID, name: currentID })
  }
  return items
})
const bridgeModelLabel = computed(() => {
  const currentID = String(bridgeConfig.value.model || bridgeStatus.value?.currentModel || '').trim()
  const option = bridgeModelOptions.value.find((item) => item.id === currentID)
  return option?.name || option?.id || '请选择模型'
})
const bridgeReady = computed(() => {
  const status = bridgeStatus.value
  return Boolean(
    status?.node?.found &&
    status?.npm?.found &&
    status?.npx?.found &&
    status?.cli?.found &&
    status?.skillsReady &&
    status?.config?.configured &&
    status?.auth?.authorized,
  )
})
const bridgeStatusPending = computed(() => {
  return !bridgeStatusLoaded.value || (bridgeRefreshing.value && !bridgeStatus.value?.lastCheckedAt)
})
const bridgeSetupLinkVisible = computed(() => Boolean(bridgeStatus.value?.setupUrl) && !bridgeStatus.value?.config?.configured)
const bridgeLoginLinkVisible = computed(() => Boolean(bridgeStatus.value?.loginUrl) && !bridgeStatus.value?.auth?.authorized)
const bridgeBrowserHintVisible = computed(() => bridgeSetupLinkVisible.value || bridgeLoginLinkVisible.value)
const bridgeStartButtonLabel = computed(() => {
  if (bridgeBusy.value) return bridgeStatus.value?.running ? '停止中...' : '启动中...'
  if (bridgeStatus.value?.running) return '停止 Bridge'
  if (bridgeConfig.value.enabled) return '启动 Bridge'
  return '启用并启动 Bridge'
})
const bridgeStepItems = computed(() => {
  const status = bridgeStatus.value
  if (!bridgeStatusLoaded.value) {
    return [
      { key: 'install', title: '安装 CLI 与 Skills', done: false, active: true, detail: '应用已启动后台预检测，正在读取当前安装状态…' },
      { key: 'setup', title: '初始化飞书应用', done: false, active: false, detail: '等待检测当前 CLI 配置和应用初始化结果…' },
      { key: 'auth', title: '登录并授权', done: false, active: false, detail: '等待检测当前授权状态…' },
    ]
  }
  return [
    {
      key: 'install',
      title: '安装 CLI 与 Skills',
      done: Boolean(status?.node?.found && status?.npm?.found && status?.npx?.found && status?.cli?.found && status?.skillsReady),
      active: Boolean(status?.installRunning),
      detail: status?.skillsReady ? 'Node / npm / npx / lark-cli / skills 已就绪' : '先安装飞书 CLI，并确认必需 skills 完整',
    },
    {
      key: 'setup',
      title: '初始化飞书应用',
      done: Boolean(status?.config?.configured),
      active: Boolean(status?.setupRunning),
      detail: status?.config?.configured ? `${status?.config?.appId || '已配置'} · ${status?.config?.brand || 'feishu'}` : '点击“首次初始化（推荐）”，在浏览器完成应用创建',
    },
    {
      key: 'auth',
      title: '登录并授权',
      done: Boolean(status?.auth?.authorized),
      active: Boolean(status?.loginRunning),
      detail: status?.auth?.authorized ? `${status?.auth?.userName || '已授权'} · ${status?.auth?.tokenStatus || 'valid'}` : '点击“登录授权”，在浏览器完成账号授权',
    },
  ]
})
const lastBridgeSnapshot = ref({
  configured: false,
  authorized: false,
  running: false,
})
let bridgeStatusPollTimer = null

const selectOptions = {
  Backend: [
    { value: 'ipc', label: 'IPC 插件' },
    { value: 'remote', label: '远端 API' },
  ],
  Transport: [
    { value: 'auto', label: '自动' },
    { value: 'pipe', label: '命名管道' },
    { value: 'websocket', label: 'WebSocket' },
  ],
  Mode: [
    { value: 'agent', label: 'Agent' },
    { value: 'chat', label: 'Chat' },
  ],
  ShellType: [
    { value: 'zsh', label: 'zsh' },
    { value: 'bash', label: 'bash' },
    { value: 'powershell', label: 'PowerShell' },
    { value: 'cmd', label: 'cmd' },
  ],
  SessionMode: [
    { value: 'auto', label: '自动' },
    { value: 'reuse', label: '复用' },
    { value: 'fresh', label: '每次新建' },
  ],
}

const selectLabel = computed(() => (field) => {
  const option = selectOptions[field]?.find((item) => item.value === config.value[field])
  return option?.label || '请选择'
})

function toggleSelect(field) {
  openSelect.value = openSelect.value === field ? '' : field
}

function chooseOption(field, value) {
  config.value[field] = value
  openSelect.value = ''
  refreshDetection()
}

function chooseBridgeModel(modelID) {
  bridgeConfig.value.model = modelID
  openSelect.value = ''
}

function formatDateTime(value) {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(date)
}

onMounted(async () => {
  try {
    config.value = await GetConfig()
    bridgeConfig.value = await GetFeishuBridgeConfig()
    availableBridgeModels.value = await GetModels()
    fallbackModelsText.value = Array.isArray(config.value.RemoteFallbackModels)
      ? config.value.RemoteFallbackModels.join('\n')
      : ''
    await refreshDetection()
    const cachedStatus = await GetFeishuBridgeStatus()
    applyBridgeStatus(cachedStatus, false)
    bridgeStatusLoaded.value = Boolean(cachedStatus?.lastCheckedAt || cachedStatus?.config?.configured || cachedStatus?.auth?.authorized || cachedStatus?.cli?.found)
    if (!bridgeStatusLoaded.value) {
      await refreshBridgeStatus(false)
    }
  } catch (e) {
    emit('log', 'error', '配置加载失败：' + (e.message || String(e)))
  }

  safeEventsOn('feishu:status', (nextStatus) => {
    applyBridgeStatus(nextStatus, true)
  })
})

onUnmounted(() => {
  safeEventsOff('feishu:status')
  stopBridgeStatusPolling()
})

async function refreshDetection() {
  try {
    detection.value = await GetDetectionInfo()
  } catch (e) {
    emit('log', 'warn', '探测信息加载失败：' + (e.message || String(e)))
  }
}

function applyBridgeStatus(nextStatus, announce = false) {
  const prev = { ...lastBridgeSnapshot.value }
  bridgeStatus.value = nextStatus
  bridgeStatusLoaded.value = true
  lastBridgeSnapshot.value = {
    configured: Boolean(nextStatus?.config?.configured),
    authorized: Boolean(nextStatus?.auth?.authorized),
    running: Boolean(nextStatus?.running),
  }
  syncBridgeStatusPolling(nextStatus)
  if (!announce) return
  if (!prev.configured && nextStatus?.config?.configured) {
    emit('notice', `飞书 CLI 应用初始化已完成：${nextStatus?.config?.appId || '已配置'}`)
  }
  if (!prev.authorized && nextStatus?.auth?.authorized) {
    emit('notice', `飞书 CLI 授权已完成：${nextStatus?.auth?.userName || '当前账号'}`)
  }
  if (!prev.running && nextStatus?.running) {
    emit('notice', 'Feishu Bridge 已开始监听飞书消息')
  }
}

function shouldPollBridgeStatus(status) {
  return Boolean(
    status && (
      status.installRunning ||
      status.setupRunning ||
      status.loginRunning ||
      (status.setupUrl && !status.config?.configured) ||
      (status.loginUrl && !status.auth?.authorized)
    ),
  )
}

function stopBridgeStatusPolling() {
  if (bridgeStatusPollTimer) {
    clearInterval(bridgeStatusPollTimer)
    bridgeStatusPollTimer = null
  }
}

function syncBridgeStatusPolling(status) {
  if (!shouldPollBridgeStatus(status)) {
    stopBridgeStatusPolling()
    return
  }
  if (bridgeStatusPollTimer) return
  bridgeStatusPollTimer = setInterval(() => {
    refreshBridgeStatus(false, false)
  }, 2500)
}

async function refreshBridgeStatus(announce = true, showLoading = true) {
  try {
    if (showLoading) {
      bridgeRefreshing.value = true
    }
    applyBridgeStatus(await RefreshFeishuBridgeStatus(), announce)
  } catch (e) {
    emit('log', 'warn', 'Feishu Bridge 状态加载失败：' + (e.message || String(e)))
  } finally {
    if (showLoading) {
      bridgeRefreshing.value = false
    }
  }
}

async function save() {
  saving.value = true
  try {
    config.value.RemoteFallbackModels = fallbackModelsText.value
      .split(/\n|,/)
      .map((item) => item.trim())
      .filter(Boolean)
    await UpdateConfig(config.value)
    await refreshDetection()
    emit('log', 'info', '配置已保存，代理已按需重启')
    emit('status-refresh')
  } catch (e) {
    emit('log', 'error', '配置保存失败：' + (e.message || String(e)))
  } finally {
    saving.value = false
  }
}

async function saveBridgeConfig() {
  bridgeSaving.value = true
  try {
    bridgeConfig.value = {
      ...bridgeConfig.value,
      brand: 'feishu',
      maxToolRounds: Number(bridgeConfig.value.maxToolRounds) || 5,
    }
    await UpdateFeishuBridgeConfig(bridgeConfig.value)
    emit('notice', 'Feishu Bridge 配置已保存')
    emit('log', 'info', 'Feishu Bridge 配置已保存')
    return true
  } catch (e) {
    emit('log', 'error', 'Feishu Bridge 配置保存失败：' + (e.message || String(e)))
    return false
  } finally {
    bridgeSaving.value = false
  }
}

async function withBridgeAction(message, action) {
  bridgeBusy.value = true
  try {
    await action()
    emit('notice', message)
    emit('log', 'info', message)
    await refreshBridgeStatus(true, false)
  } catch (e) {
    emit('log', 'error', message + '失败：' + (e.message || String(e)))
  } finally {
    bridgeBusy.value = false
  }
}

function openBridgeURL(url) {
  if (!url) return
  BrowserOpenURL(url)
}

async function installBridgeCLI() {
  await withBridgeAction('飞书 CLI 安装已触发', () => InstallFeishuCLI())
}

async function startBridgeSetupNew() {
  await withBridgeAction('飞书 CLI 首次初始化已启动', () => StartFeishuCLISetupNew())
}

async function startBridgeLogin() {
  await withBridgeAction('飞书 CLI 授权流程已启动', () => StartFeishuCLILogin())
}

async function enableBridgeAndStart() {
  const saved = await saveBridgeConfig()
  if (!saved) return
  await withBridgeAction('Feishu Bridge 已启动', async () => {
    bridgeConfig.value.enabled = true
    await UpdateFeishuBridgeConfig(bridgeConfig.value)
    await StartFeishuBridge()
  })
}

async function stopBridge() {
  await withBridgeAction('Feishu Bridge 已停止', async () => {
    bridgeConfig.value.enabled = false
    await UpdateFeishuBridgeConfig(bridgeConfig.value)
    await StopFeishuBridge()
  })
}

async function handleBridgePrimaryAction() {
  if (bridgeStatus.value?.running) {
    await stopBridge()
    return
  }
  await enableBridgeAndStart()
}

function isBridgeStepClickable(step) {
  if (!step || step.done) return false
  if (bridgeBusy.value || bridgeRefreshing.value) return false
  return bridgeStatusLoaded.value
}

function bridgeStepCTA(step) {
  if (!isBridgeStepClickable(step)) return ''
  return {
    install: '点击安装',
    setup: bridgeStatus.value?.setupUrl ? '继续初始化' : '开始初始化',
    auth: bridgeStatus.value?.loginUrl ? '继续授权' : '开始授权',
  }[step.key] || '继续'
}

async function handleBridgeStepClick(step) {
  if (!isBridgeStepClickable(step)) return
  if (step.key === 'install') {
    await installBridgeCLI()
    return
  }
  if (step.key === 'setup') {
    if (bridgeStatus.value?.setupUrl) {
      openBridgeURL(bridgeStatus.value.setupUrl)
      emit('notice', '已打开飞书应用初始化链接')
      return
    }
    await startBridgeSetupNew()
    return
  }
  if (step.key === 'auth') {
    if (bridgeStatus.value?.loginUrl) {
      openBridgeURL(bridgeStatus.value.loginUrl)
      emit('notice', '已打开飞书授权链接')
      return
    }
    await startBridgeLogin()
  }
}
</script>

<template>
  <div class="page">
    <div class="page-title">
      <div>
        <h1>设置</h1>
        <p>配置监听地址、Lingma 传输方式、模型探测超时、会话复用和请求超时。</p>
      </div>
      <button class="primary-button" type="button" :disabled="saving" @click="save">
        {{ saving ? '保存中...' : '保存并重启' }}
      </button>
    </div>

    <section class="grid-2 settings-grid">
      <div class="glass-panel">
        <div class="panel-header">
          <div>
            <h2>服务监听</h2>
            <p>第三方客户端连接本地代理使用这组地址。</p>
          </div>
        </div>
        <div class="form-grid">
          <div class="field">
            <label>连接模式</label>
            <div class="custom-select" :class="{ open: openSelect === 'Backend' }">
              <button type="button" @click="toggleSelect('Backend')">
                <span>{{ selectLabel('Backend') }}</span>
                <i class="bi bi-chevron-down" aria-hidden="true"></i>
              </button>
              <div v-if="openSelect === 'Backend'" class="select-menu">
                <button
                  v-for="option in selectOptions.Backend"
                  :key="option.value"
                  :class="{ selected: option.value === config.Backend }"
                  type="button"
                  @click="chooseOption('Backend', option.value)"
                >
                  {{ option.label }}
                </button>
              </div>
            </div>
          </div>
          <div class="field">
            <label>主机</label>
            <input v-model="config.Host" type="text" placeholder="127.0.0.1" />
          </div>
          <div class="field">
            <label>端口</label>
            <input v-model.number="config.Port" type="number" placeholder="8095" />
          </div>
          <div class="field">
            <label>传输方式</label>
            <div class="custom-select" :class="{ open: openSelect === 'Transport' }">
              <button type="button" @click="toggleSelect('Transport')">
                <span>{{ selectLabel('Transport') }}</span>
                <i class="bi bi-chevron-down" aria-hidden="true"></i>
              </button>
              <div v-if="openSelect === 'Transport'" class="select-menu">
                <button
                  v-for="option in selectOptions.Transport"
                  :key="option.value"
                  :class="{ selected: option.value === config.Transport }"
                  type="button"
                  @click="chooseOption('Transport', option.value)"
                >
                  {{ option.label }}
                </button>
              </div>
            </div>
          </div>
          <div class="field">
            <label>超时秒数</label>
            <input v-model.number="config.Timeout" type="number" min="0" />
            <small>0 表示不设置代理层单次请求超时，适合长流程任务。</small>
          </div>
          <div class="field">
            <label>探测超时秒数</label>
            <input v-model.number="config.WarmupTimeout" type="number" min="1" placeholder="30" />
            <small>用于启动代理和手动探测模型时的 warmup 超时，默认 30 秒。</small>
          </div>
          <div class="field span-2 switch-field">
            <div>
              <label>远端超时兜底</label>
              <p>设置正数超时后，远端 API 超时、限流或 5xx 且尚未流式输出时，自动切换到下一个可用模型。</p>
            </div>
            <label class="switch">
              <input v-model="config.RemoteFallbackEnabled" type="checkbox" />
              <span></span>
            </label>
          </div>
          <div class="field span-2">
            <label>兜底模型顺序</label>
            <textarea
              v-model="fallbackModelsText"
              placeholder="kmodel&#10;mmodel&#10;dashscope_qwen3_coder&#10;dashscope_qmodel"
            ></textarea>
          </div>
          <div class="field span-2">
            <label>WebSocket 地址</label>
            <input v-model="config.WebSocketURL" type="text" placeholder="留空自动探测 Lingma WebSocket" />
          </div>
          <div class="field span-2">
            <label>命名管道</label>
            <input v-model="config.Pipe" type="text" placeholder="留空自动探测 Windows Named Pipe" />
          </div>
          <div class="field span-2">
            <label>远端 API 域名</label>
            <input v-model="config.RemoteBaseURL" type="text" placeholder="留空自动探测，默认 https://lingma.alibabacloud.com" />
          </div>
          <div class="field span-2">
            <label>远端认证文件</label>
            <input v-model="config.RemoteAuthFile" type="text" placeholder="可选 credentials.json；留空只读 ~/.lingma/cache/user" />
          </div>
          <div class="field span-2">
            <label>远端 Cosy 版本</label>
            <input v-model="config.RemoteVersion" type="text" placeholder="默认 2.11.2" />
          </div>
        </div>
        <div class="hint-box">
          <strong>自动探测失败时</strong>
          <span>IPC 模式先确认 VS Code / Lingma 插件已启动并登录。远端 API 模式会优先读取认证文件；留空时只读 <code>~/.lingma/cache/user</code>，不会写入或上传登录态。</span>
        </div>
        <div v-if="detection" class="detect-card">
          <div class="detect-title">
            <strong>当前解析结果</strong>
            <button type="button" @click="refreshDetection">刷新</button>
          </div>
          <dl>
            <div>
              <dt>监听地址</dt>
              <dd>{{ detection.listenUrl || '未启动' }}</dd>
            </div>
            <div>
              <dt>当前后端</dt>
              <dd>{{ detection.backendLabel || detection.backend }}</dd>
            </div>
            <div>
              <dt>IPC 地址</dt>
              <dd v-if="detection.ipcSuccess">{{ detection.ipcTransport }} · {{ detection.ipcEndpoint }}</dd>
              <dd v-else class="warn-text">{{ detection.ipcError || '未探测到' }}</dd>
            </div>
            <div>
              <dt>远端域名</dt>
              <dd>
                {{ detection.remoteBaseUrl }}
                <span v-if="detection.remoteBaseUrlSource" class="muted-inline">来自 {{ detection.remoteBaseUrlSource }}</span>
              </dd>
            </div>
            <div>
              <dt>登录态来源</dt>
              <dd v-if="detection.remoteCredentialSuccess">{{ detection.remoteCredentialSource }}</dd>
              <dd v-else class="warn-text">{{ detection.remoteCredentialError || '未探测到' }}</dd>
            </div>
            <div v-if="detection.remoteCredentialSuccess">
              <dt>账号 / 机器</dt>
              <dd>{{ detection.remoteUserId || '未知用户' }} · {{ detection.remoteMachineId || '未知机器' }}</dd>
            </div>
            <div v-if="detection.remoteCredentialSuccess">
              <dt>登录态有效期</dt>
              <dd :class="{ 'warn-text': detection.remoteTokenExpired }">
                {{ formattedTokenExpireAt || '未提供' }}
                <span v-if="detection.remoteTokenExpired">（已过期）</span>
              </dd>
            </div>
          </dl>
        </div>
      </div>

      <div class="panel-stack">
        <div class="glass-panel">
        <div class="panel-header">
          <div>
            <h2>会话与环境</h2>
            <p>仅在 IPC 插件模式下生效，影响 Lingma 会话上下文和工具执行环境。</p>
          </div>
          <span class="status-chip" :class="isIPCBackend ? 'ok' : 'warn'">{{ isIPCBackend ? '仅 IPC 生效' : '远端模式忽略' }}</span>
        </div>
        <div v-if="!isIPCBackend" class="hint-box compact-hint">
          <strong>当前为远端 API 模式</strong>
          <span>右侧这组参数不会参与远端请求，只在切换到 IPC 插件模式后生效。</span>
        </div>
        <fieldset class="settings-fieldset" :disabled="!isIPCBackend">
        <div class="form-grid compact-form-grid">
            <div class="field">
            <label>模式</label>
            <div class="custom-select" :class="{ open: openSelect === 'Mode' }">
              <button type="button" @click="toggleSelect('Mode')">
                <span>{{ selectLabel('Mode') }}</span>
                <i class="bi bi-chevron-down" aria-hidden="true"></i>
              </button>
              <div v-if="openSelect === 'Mode'" class="select-menu">
                <button
                  v-for="option in selectOptions.Mode"
                  :key="option.value"
                  :class="{ selected: option.value === config.Mode }"
                  type="button"
                  @click="chooseOption('Mode', option.value)"
                >
                  {{ option.label }}
                </button>
              </div>
            </div>
          </div>
          <div class="field">
            <label>Shell 类型</label>
            <div class="custom-select" :class="{ open: openSelect === 'ShellType' }">
              <button type="button" @click="toggleSelect('ShellType')">
                <span>{{ selectLabel('ShellType') }}</span>
                <i class="bi bi-chevron-down" aria-hidden="true"></i>
              </button>
              <div v-if="openSelect === 'ShellType'" class="select-menu">
                <button
                  v-for="option in selectOptions.ShellType"
                  :key="option.value"
                  :class="{ selected: option.value === config.ShellType }"
                  type="button"
                  @click="chooseOption('ShellType', option.value)"
                >
                  {{ option.label }}
                </button>
              </div>
            </div>
          </div>
          <div class="field">
            <label>会话策略</label>
            <div class="custom-select" :class="{ open: openSelect === 'SessionMode' }">
              <button type="button" @click="toggleSelect('SessionMode')">
                <span>{{ selectLabel('SessionMode') }}</span>
                <i class="bi bi-chevron-down" aria-hidden="true"></i>
              </button>
              <div v-if="openSelect === 'SessionMode'" class="select-menu">
                <button
                  v-for="option in selectOptions.SessionMode"
                  :key="option.value"
                  :class="{ selected: option.value === config.SessionMode }"
                  type="button"
                  @click="chooseOption('SessionMode', option.value)"
                >
                  {{ option.label }}
                </button>
              </div>
            </div>
          </div>
          <div class="field">
            <label>当前文件</label>
            <input v-model="config.CurrentFilePath" type="text" placeholder="可选" />
          </div>
            <div class="field span-2">
              <label>工作目录</label>
              <input v-model="config.Cwd" type="text" placeholder="Lingma 创建 session 时使用的 cwd" />
            </div>
          </div>
        </fieldset>
        </div>

        <div class="glass-panel">
          <div class="panel-header">
          <div>
            <div class="panel-title-row">
              <h2>Feishu Bot Bridge</h2>
              <div class="inline-help">
                <button type="button" class="inline-help-trigger" aria-label="查看会话命令说明">
                  <i class="bi bi-question-circle" aria-hidden="true"></i>
                </button>
                <div class="inline-help-popover">
                  <strong>会话命令</strong>
                  <span><code>/help</code>：查看命令帮助</span>
                  <span><code>/compact</code>：手动压缩当前会话上下文</span>
                  <span><code>/summary</code>：查看当前会话摘要</span>
                  <span><code>/reset</code>：清空当前飞书会话上下文</span>
                </div>
              </div>
            </div>
            <p>通过本机 lark-cli 把飞书 Bot 消息桥接到 Lingma Proxy。默认关闭，不影响现有代理功能。</p>
          </div>
          <span class="status-chip" :class="bridgeStatus?.running ? 'ok' : 'warn'">
            {{ bridgeStatus?.running ? '运行中' : '未运行' }}
          </span>
        </div>

          <div class="form-grid compact-form-grid">
          <div class="field">
            <label>启用 Bridge</label>
            <label class="switch">
              <input v-model="bridgeConfig.enabled" type="checkbox" />
              <span></span>
            </label>
          </div>
          <div class="field">
            <label>随代理自动启动</label>
            <label class="switch">
              <input v-model="bridgeConfig.autoStart" type="checkbox" />
              <span></span>
            </label>
          </div>
          <div class="field">
            <label>模型</label>
            <div class="custom-select" :class="{ open: openSelect === 'BridgeModel' }">
              <button type="button" @click="toggleSelect('BridgeModel')">
                <span>{{ bridgeModelLabel }}</span>
                <i class="bi bi-chevron-down" aria-hidden="true"></i>
              </button>
              <div v-if="openSelect === 'BridgeModel'" class="select-menu">
                <button
                  v-for="model in bridgeModelOptions"
                  :key="model.id"
                  :class="{ selected: model.id === bridgeConfig.model }"
                  type="button"
                  @click="chooseBridgeModel(model.id)"
                >
                  {{ model.name || model.id }}
                </button>
              </div>
            </div>
          </div>
        </div>

        <div class="actions-row bridge-actions-top">
          <button class="primary-button" type="button" :disabled="bridgeBusy" @click="saveBridgeConfig">
            {{ bridgeSaving ? '保存中...' : '保存 Bridge 配置' }}
          </button>
          <button
            class="primary-button"
            type="button"
            :class="{ 'danger-button': bridgeStatus && bridgeStatus.running }"
            :disabled="bridgeBusy || (!(bridgeStatus && bridgeStatus.running) && !bridgeReady)"
            @click="handleBridgePrimaryAction"
          >
            {{ bridgeStartButtonLabel }}
          </button>
        </div>

          <div class="hint-box compact-hint">
          <strong>推荐接入路径</strong>
          <span>按顺序完成“安装 CLI 与 Skills” → “初始化飞书应用” → “登录并授权”，全部完成后再保存配置并启动 Bridge。</span>
        </div>

          <div class="bridge-progress">
          <button
            v-for="step in bridgeStepItems"
            :key="step.key"
            class="bridge-progress-item"
            :class="{ done: step.done, active: step.active, clickable: isBridgeStepClickable(step) }"
            type="button"
            :disabled="!isBridgeStepClickable(step)"
            @click="handleBridgeStepClick(step)"
          >
            <div class="bridge-progress-head">
              <strong>{{ step.title }}</strong>
              <div class="bridge-progress-meta">
                <span class="bridge-progress-state">
                  {{ step.done ? '已完成' : (step.active ? '进行中' : '待完成') }}
                </span>
                <span v-if="isBridgeStepClickable(step)" class="bridge-progress-cta">
                  {{ bridgeStepCTA(step) }}
                  <i class="bi bi-arrow-right-short" aria-hidden="true"></i>
                </span>
              </div>
            </div>
            <p>{{ step.detail }}</p>
          </button>
        </div>

          <div v-if="bridgeStatus" class="detect-card">
          <div class="detect-title">
            <strong>当前状态</strong>
            <button type="button" :disabled="bridgeRefreshing" @click="refreshBridgeStatus">
              {{ bridgeRefreshing ? '检测中...' : '刷新状态' }}
            </button>
          </div>
          <dl>
            <div>
              <dt>平台</dt>
              <dd>{{ bridgeStatusPending ? '-' : `${bridgeStatus.platform} · ${bridgeStatus.arch}` }}</dd>
            </div>
            <div>
              <dt>Node / npm / npx</dt>
              <dd :class="{ 'warn-text': !bridgeStatusPending && (!bridgeStatus.node?.found || !bridgeStatus.npm?.found || !bridgeStatus.npx?.found) }">
                {{ bridgeStatusPending ? '-' : `${bridgeStatus.node?.version || '缺失'} / ${bridgeStatus.npm?.version || '缺失'} / ${bridgeStatus.npx?.version || '缺失'}` }}
              </dd>
            </div>
            <div>
              <dt>lark-cli</dt>
              <dd :class="{ 'warn-text': !bridgeStatusPending && !bridgeStatus.cli?.found }">
                {{ bridgeStatusPending ? '-' : (bridgeStatus.cli?.version || '未安装') }}
              </dd>
            </div>
            <div>
              <dt>Skills</dt>
              <dd :class="{ 'warn-text': !bridgeStatusPending && !bridgeStatus.skillsReady }">
                {{ bridgeStatusPending ? '-' : (bridgeStatus.skillsReady ? '已就绪' : '缺失或未完整安装') }}
              </dd>
            </div>
            <div>
              <dt>CLI 配置</dt>
              <dd :class="{ 'warn-text': !bridgeStatusPending && !bridgeStatus.config?.configured }">
                {{ bridgeStatusPending ? '-' : (bridgeStatus.config?.configured ? `${bridgeStatus.config?.appId || '已配置'} · ${bridgeStatus.config?.brand || 'feishu'}` : (bridgeStatus.config?.message || '未初始化')) }}
              </dd>
            </div>
            <div>
              <dt>授权状态</dt>
              <dd :class="{ 'warn-text': !bridgeStatusPending && !bridgeStatus.auth?.authorized }">
                {{ bridgeStatusPending ? '-' : (bridgeStatus.auth?.authorized ? `${bridgeStatus.auth?.userName || '已授权'} · ${bridgeStatus.auth?.tokenStatus || 'ok'}` : (bridgeStatus.auth?.message || '未授权')) }}
              </dd>
            </div>
            <div>
              <dt>运行状态</dt>
              <dd>{{ bridgeStatusPending ? '-' : (bridgeStatus.running ? `运行中 · ${formatDateTime(bridgeStatus.lastStartedAt) || bridgeStatus.lastStartedAt || ''}` : '未运行') }}</dd>
            </div>
            <div v-if="bridgeStatusPending || bridgeStatus.lastCheckedAt">
              <dt>最近检测</dt>
              <dd>{{ bridgeStatusPending ? '-' : (formatDateTime(bridgeStatus.lastCheckedAt) || bridgeStatus.lastCheckedAt) }}</dd>
            </div>
            <div v-if="bridgeStatus.lastOutput">
              <dt>最近输出</dt>
              <dd>{{ bridgeStatus.lastOutput }}</dd>
            </div>
            <div v-if="bridgeStatus.lastError">
              <dt>最近错误</dt>
              <dd class="warn-text">{{ bridgeStatus.lastError }}</dd>
            </div>
          </dl>
        </div>

          <div v-if="bridgeBrowserHintVisible" class="hint-box">
          <strong>浏览器继续</strong>
          <span>首次初始化和登录授权需要在浏览器中完成，点击下方链接继续。</span>
          <div class="actions-row">
            <button v-if="bridgeSetupLinkVisible" type="button" @click="openBridgeURL(bridgeStatus.setupUrl)">打开初始化链接</button>
            <button v-if="bridgeLoginLinkVisible" type="button" @click="openBridgeURL(bridgeStatus.loginUrl)">打开授权链接</button>
          </div>
        </div>
        </div>
      </div>
    </section>
  </div>
</template>
