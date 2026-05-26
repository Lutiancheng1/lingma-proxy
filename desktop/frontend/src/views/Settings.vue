<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { BrowserOpenURL } from '../../wailsjs/runtime'
import {
  GetConfig,
  GetDetectionInfo,
  GetFeishuBridgeConfig,
  GetFeishuBridgeMCPJSON,
  GetFeishuBridgeSkills,
  GetFeishuBridgeStatus,
  GetModels,
  ChooseFeishuBridgeSkillFolder,
  ChooseFeishuBridgeSkillZip,
  DeleteFeishuBridgeSkill,
  ImportFeishuBridgeSkillPath,
  InstallFeishuCLI,
  RefreshFeishuBridgeStatus,
  RefreshModels,
  ReinstallFeishuSkills,
  ReloadFeishuBridgeSkills,
  SetFeishuBridgeSkillEnabled,
  StartFeishuBridge,
  StartFeishuCLILogin,
  StartFeishuCLISetupNew,
  StopFeishuBridge,
  UpdateConfig,
  UpdateFeishuBridgeConfig,
  SaveFeishuBridgeMCPJSON,
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
  botName: '',
  botIdentity: '',
  mcpEnabled: false,
  mcpServers: [],
  context: {
    autoCompact: true,
    compactWatermark: 75,
    toolResultRetention: 2,
    contextWindowOverride: 0,
    skillHttpTimeout: 60,
    skillHttpMaxBytes: 5242880,
  },
  maxToolRounds: 0,
})
const bridgeStatus = ref(null)
const bridgeStatusLoaded = ref(false)
const bridgeInitialStatusLoading = ref(true)
const bridgeSaving = ref(false)
const bridgeBusy = ref(false)
const bridgeRefreshing = ref(false)
const bridgeSetupGuideOpen = ref(false)
const bridgeAdvancedDialogOpen = ref(false)
const bridgeMCPJSONDialogOpen = ref(false)
const bridgeBotNameDraft = ref('')
const bridgeIdentityDraft = ref('')
const bridgeMCPEnabledDraft = ref(false)
const bridgeMCPServersDraft = ref([])
const bridgeMCPExpanded = ref({})
const bridgeMCPJSONDraft = ref('')
const bridgeMCPJSONPath = ref('')
const bridgeMCPJSONError = ref('')
const bridgeMCPJSONSaving = ref(false)
const bridgeSkills = ref([])
const bridgeSkillsLoading = ref(false)
const bridgeSkillImporting = ref(false)
const bridgeContextDraft = ref({
  autoCompact: true,
  compactWatermark: 75,
  toolResultRetention: 2,
  contextWindowOverride: 0,
  skillHttpTimeout: 60,
  skillHttpMaxBytes: 5242880,
})
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
  return bridgeInitialStatusLoading.value || !bridgeStatusLoaded.value || (bridgeRefreshing.value && !bridgeStatus.value?.lastCheckedAt)
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
const bridgeMCPServers = computed(() => Array.isArray(bridgeStatus.value?.mcpServers) ? bridgeStatus.value.mcpServers : [])
const bridgeMCPServerGroups = computed(() => groupMCPServersBySource(bridgeMCPServersDraft.value))
const bridgeAdvancedLabel = computed(() => {
  return '设置'
})
const bridgeInstallStepDone = computed(() => {
  const status = bridgeStatus.value
  return Boolean(status?.node?.found && status?.npm?.found && status?.npx?.found && status?.cli?.found && status?.skillsReady)
})
const bridgeSetupStepDone = computed(() => Boolean(bridgeStatus.value?.config?.configured))
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
      detail: status?.installRunning
        ? (status?.lastOutput || '正在安装飞书 CLI...')
        : (status?.skillsReady ? 'Node / npm / npx / lark-cli / skills 已就绪' : '先安装飞书 CLI，并确认必需 skills 完整'),
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
const remoteProxyStatus = computed(() => {
  if (!detection.value) return ''
  if (detection.value.remoteProxyError) return detection.value.remoteProxyError
  if (detection.value.remoteProxyUrl) {
    return `${detection.value.remoteProxyUrl}（${detection.value.remoteProxySource || '显式配置'}）`
  }
  return '未显式配置；如存在 HTTP_PROXY / HTTPS_PROXY，Go 默认传输仍会尊重系统环境变量'
})

const selectOptions = {
  Backend: [
    { value: 'ipc', label: 'IPC 插件' },
    { value: 'remote', label: '远端 API' },
  ],
  Transport: [
    { value: 'auto', label: '自动' },
    { value: 'pipe', label: 'Socket / Named Pipe' },
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

const bridgeSetupCommands = [
  {
    title: 'Windows 一键脚本（推荐兜底）',
    command: 'powershell.exe -NoProfile -ExecutionPolicy Bypass -Command "iwr https://raw.githubusercontent.com/Lutiancheng1/lingma-ipc-proxy/main/scripts/windows/install-feishu-cli.ps1 -UseBasicParsing -OutFile $env:TEMP\\install-feishu-cli.ps1; & $env:TEMP\\install-feishu-cli.ps1 -PersistPath"',
    note: 'App 已内置一键安装（上方"点击安装"按钮），仅在自动安装失败、想纯手动复现时才需要这条命令。脚本会从 GitHub 拉取后落到临时目录执行；如检测到 nvm-windows 会优先用 nvm 安装/切换 LTS，再修复 npm 全局目录并安装 lark-cli 与 Skills。',
  },
  {
    title: '可选：切换 npm 镜像',
    command: 'npm config set registry https://registry.npmmirror.com',
    note: '公司网络或 npmjs 访问慢时使用；能直接访问 npmjs 可跳过。',
  },
  {
    title: '安装飞书 CLI',
    command: 'npm install -g @larksuite/cli',
    note: '需要 Node.js 20.12+，推荐当前 LTS。macOS 一键安装会优先复用 nvm/fnm/volta/Homebrew 中可用的 Node。',
  },
  {
    title: '安装 CLI Skills',
    command: 'npx -y skills@1.5.6 add https://open.feishu.cn --skill -y -g',
    note: '使用飞书官方 well-known 清单源（含 26+ skills），固定 skills@1.5.6 以避开 latest 偶发安装异常。',
  },
  {
    title: '首次初始化飞书应用',
    command: 'lark-cli config init --new --lang zh',
    note: '会打开浏览器，按页面完成应用创建或选择已有应用。',
  },
  {
    title: '登录并授权',
    command: 'lark-cli auth login --recommend',
    note: '完成授权后回到 Lingma Proxy 点击“刷新状态”。',
  },
  {
    title: '验证授权',
    command: 'lark-cli auth status --verify',
    note: '返回 valid 后即可保存配置并启动 Bridge。',
  },
]

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

async function refreshAvailableBridgeModels({ background = false } = {}) {
  try {
    const models = background ? await RefreshModels() : await GetModels()
    availableBridgeModels.value = Array.isArray(models) ? models : []
  } catch (e) {
    if (!background) {
      emit('log', 'warn', 'Bridge 模型列表读取失败：' + (e.message || String(e)))
    }
  }
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
    await refreshAvailableBridgeModels()
    if (availableBridgeModels.value.length === 0) {
      refreshAvailableBridgeModels({ background: true })
    }
    fallbackModelsText.value = Array.isArray(config.value.RemoteFallbackModels)
      ? config.value.RemoteFallbackModels.join('\n')
      : ''
    await refreshDetection()
    bridgeStatus.value = await GetFeishuBridgeStatus()
    await refreshBridgeStatus(false)
  } catch (e) {
    emit('log', 'error', '配置加载失败：' + (e.message || String(e)))
    bridgeInitialStatusLoading.value = false
  }

  safeEventsOn('feishu:status', (nextStatus) => {
    applyBridgeStatus(nextStatus, true)
  })
  safeEventsOn('models:updated', (models) => {
    availableBridgeModels.value = Array.isArray(models) ? models : []
  })
})

onUnmounted(() => {
  safeEventsOff('feishu:status')
  safeEventsOff('models:updated')
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
  bridgeInitialStatusLoading.value = false
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
      maxToolRounds: 0,
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

function bridgeAdvancedServersFromStatus() {
  const saved = new Map((Array.isArray(bridgeConfig.value.mcpServers) ? bridgeConfig.value.mcpServers : []).map((server) => [String(server.name || '').toLowerCase(), server]))
  const statusServers = Array.isArray(bridgeStatus.value?.mcpServers) ? bridgeStatus.value.mcpServers : []
  const merged = new Map()
  for (const item of statusServers) {
    const key = String(item.name || '').toLowerCase()
    const savedItem = saved.get(key)
    const enabled = Boolean(savedItem?.enabled ?? item.enabled)
    const message = enabled && item.message === '未启用' ? '' : (item.message || '')
    merged.set(key, {
      name: item.name,
      source: item.source || savedItem?.source || '',
      sourceClient: item.sourceClient || savedItem?.sourceClient || sourceClientFromPath(item.source || savedItem?.source || ''),
      command: item.command || savedItem?.command || '',
      args: Array.isArray(item.args) ? item.args : (Array.isArray(savedItem?.args) ? savedItem.args : []),
      env: savedItem?.env || {},
      enabled,
      available: Boolean(item.available),
      toolCount: Number(item.toolCount || 0),
      tools: Array.isArray(item.tools) ? item.tools : [],
      message,
    })
  }
  for (const item of saved.values()) {
    const key = String(item.name || '').toLowerCase()
    if (!merged.has(key)) {
      merged.set(key, {
        ...item,
        available: false,
        toolCount: 0,
        tools: [],
        message: '未在本机扫描结果中发现',
      })
    }
  }
  return Array.from(merged.values()).sort((a, b) => String(a.name).localeCompare(String(b.name)))
}

function openBridgeAdvancedDialog() {
  bridgeBotNameDraft.value = String(bridgeConfig.value.botName || '')
  bridgeIdentityDraft.value = String(bridgeConfig.value.botIdentity || '')
  bridgeMCPEnabledDraft.value = Boolean(bridgeConfig.value.mcpEnabled)
  bridgeMCPServersDraft.value = bridgeAdvancedServersFromStatus()
  bridgeContextDraft.value = {
    autoCompact: bridgeConfig.value.context?.autoCompact !== false,
    compactWatermark: Number(bridgeConfig.value.context?.compactWatermark || 75),
    toolResultRetention: Number(bridgeConfig.value.context?.toolResultRetention || 2),
    contextWindowOverride: Number(bridgeConfig.value.context?.contextWindowOverride || 0),
    skillHttpTimeout: Number(bridgeConfig.value.context?.skillHttpTimeout || 60),
    skillHttpMaxBytes: Number(bridgeConfig.value.context?.skillHttpMaxBytes || 5242880),
  }
  refreshBridgeSkills()
  bridgeAdvancedDialogOpen.value = true
}

async function refreshBridgeSkills() {
  bridgeSkillsLoading.value = true
  try {
    bridgeSkills.value = await GetFeishuBridgeSkills()
  } catch (e) {
    emit('log', 'warn', 'Bridge Skills 读取失败：' + (e.message || String(e)))
    bridgeSkills.value = []
  } finally {
    bridgeSkillsLoading.value = false
  }
}

async function reloadBridgeSkills() {
  bridgeSkillsLoading.value = true
  try {
    await ReloadFeishuBridgeSkills()
    bridgeSkills.value = await GetFeishuBridgeSkills()
    emit('notice', 'Feishu Bridge Skills 已重新扫描')
  } catch (e) {
    emit('notice', 'Skills 扫描失败：' + (e.message || String(e)))
  } finally {
    bridgeSkillsLoading.value = false
  }
}

async function importBridgeSkill(kind) {
  bridgeSkillImporting.value = true
  try {
    const path = kind === 'zip' ? await ChooseFeishuBridgeSkillZip() : await ChooseFeishuBridgeSkillFolder()
    if (!path) return
    const result = await ImportFeishuBridgeSkillPath(path)
    bridgeSkills.value = await GetFeishuBridgeSkills()
    const imported = Array.isArray(result?.imported) ? result.imported.length : 0
    const errors = Array.isArray(result?.errors) && result.errors.length > 0 ? `；${result.errors.length} 个错误` : ''
    emit('notice', `导入 ${imported} 个 Skill${errors}`)
  } catch (e) {
    emit('notice', 'Skill 导入失败：' + (e.message || String(e)))
  } finally {
    bridgeSkillImporting.value = false
  }
}

async function toggleBridgeSkill(skill) {
  try {
    await SetFeishuBridgeSkillEnabled(skill.id, !skill.enabled)
    bridgeSkills.value = await GetFeishuBridgeSkills()
  } catch (e) {
    emit('notice', 'Skill 状态更新失败：' + (e.message || String(e)))
  }
}

async function deleteBridgeSkill(skill) {
  try {
    await DeleteFeishuBridgeSkill(skill.id)
    bridgeSkills.value = await GetFeishuBridgeSkills()
  } catch (e) {
    emit('notice', 'Skill 删除失败：' + (e.message || String(e)))
  }
}

async function refreshBridgeAdvancedMCP() {
  await refreshBridgeStatus(true, true)
  bridgeMCPServersDraft.value = bridgeAdvancedServersFromStatus()
}

async function openBridgeMCPJSONDialog() {
  bridgeMCPJSONError.value = ''
  try {
    const result = await GetFeishuBridgeMCPJSON()
    bridgeMCPJSONDraft.value = result?.content || ''
    bridgeMCPJSONPath.value = result?.path || ''
    bridgeMCPJSONDialogOpen.value = true
  } catch (e) {
    bridgeMCPJSONError.value = e.message || String(e)
    bridgeMCPJSONDraft.value = ''
    bridgeMCPJSONPath.value = ''
    bridgeMCPJSONDialogOpen.value = true
  }
}

async function saveBridgeMCPJSON() {
  bridgeMCPJSONSaving.value = true
  bridgeMCPJSONError.value = ''
  try {
    const result = await SaveFeishuBridgeMCPJSON(bridgeMCPJSONDraft.value)
    bridgeMCPJSONDraft.value = result?.content || bridgeMCPJSONDraft.value
    bridgeMCPJSONPath.value = result?.path || bridgeMCPJSONPath.value
    emit('notice', `自定义 MCP JSON 已保存，解析到 ${result?.serverCount || 0} 个 server`)
    await refreshBridgeStatus(false, true)
    bridgeMCPServersDraft.value = bridgeAdvancedServersFromStatus()
    bridgeMCPJSONDialogOpen.value = false
  } catch (e) {
    bridgeMCPJSONError.value = e.message || String(e)
  } finally {
    bridgeMCPJSONSaving.value = false
  }
}

function toggleBridgeMCPServer(name) {
  bridgeMCPServersDraft.value = bridgeMCPServersDraft.value.map((server) => {
    if (server.name !== name) return server
    const enabled = !server.enabled
    return { ...server, enabled, message: enabled && server.message === '未启用' ? '' : server.message }
  })
}

function toggleBridgeMCPTools(name) {
  const key = String(name || '')
  bridgeMCPExpanded.value = {
    ...bridgeMCPExpanded.value,
    [key]: !bridgeMCPExpanded.value[key],
  }
}

function bridgeMCPToolLabel(tool) {
  return tool?.function || tool?.name || 'unknown_tool'
}

function bridgeMCPToolDescription(tool) {
  const rawName = String(tool?.name || '').trim()
  const functionName = String(tool?.function || '').trim()
  const desc = String(tool?.description || '').trim()
  const parts = []
  if (rawName && functionName && rawName !== functionName) {
    parts.push(`原始：${rawName}`)
  }
  if (desc) {
    parts.push(desc)
  }
  return parts.join(' · ')
}

function bridgeMCPServerMeta(server) {
  if (!server?.enabled) return '未启用'
  if (!bridgeMCPEnabledDraft.value) return '需打开总开关'
  if (server.available) return `${server.toolCount || 0} tools`
  const message = String(server.message || '').trim()
  if (message && message !== '未启用') return message
  const saved = Array.isArray(bridgeConfig.value.mcpServers)
    ? bridgeConfig.value.mcpServers.find((item) => String(item.name || '').toLowerCase() === String(server.name || '').toLowerCase())
    : null
  if (!bridgeConfig.value.mcpEnabled || !saved?.enabled) return '保存后检测'
  return bridgeRefreshing.value ? '检测中...' : '待检测'
}

async function saveBridgeAdvancedSettings() {
  bridgeConfig.value = {
    ...bridgeConfig.value,
    botName: bridgeBotNameDraft.value.trim(),
    botIdentity: bridgeIdentityDraft.value.trim(),
    mcpEnabled: bridgeMCPEnabledDraft.value,
    context: {
      autoCompact: bridgeContextDraft.value.autoCompact,
      compactWatermark: Number(bridgeContextDraft.value.compactWatermark || 75),
      toolResultRetention: Number(bridgeContextDraft.value.toolResultRetention || 2),
      contextWindowOverride: Number(bridgeContextDraft.value.contextWindowOverride || 0),
      skillHttpTimeout: Number(bridgeContextDraft.value.skillHttpTimeout || 60),
      skillHttpMaxBytes: Number(bridgeContextDraft.value.skillHttpMaxBytes || 5242880),
    },
    mcpServers: bridgeMCPServersDraft.value.map((server) => ({
      name: server.name,
      source: server.source,
      sourceClient: server.sourceClient,
      command: server.command,
      args: Array.isArray(server.args) ? server.args : [],
      env: {},
      enabled: Boolean(server.enabled),
    })),
  }
  const saved = await saveBridgeConfig()
  if (saved) {
    bridgeAdvancedDialogOpen.value = false
    await refreshBridgeStatus(false, false)
  }
}

function clearBridgeIdentity() {
  bridgeIdentityDraft.value = ''
}

function groupMCPServersBySource(servers) {
  const groups = new Map()
  for (const server of Array.isArray(servers) ? servers : []) {
    const source = server.sourceClient || sourceClientFromPath(server.source) || '本机配置'
    if (!groups.has(source)) {
      groups.set(source, [])
    }
    groups.get(source).push(server)
  }
  return Array.from(groups.entries())
    .map(([source, items]) => ({
      source,
      servers: items.slice().sort((a, b) => String(a.name).localeCompare(String(b.name))),
    }))
    .sort((a, b) => a.source.localeCompare(b.source))
}

function sourceClientFromPath(path) {
  const value = String(path || '').toLowerCase()
  if (value.includes('qodercn')) return 'QoderCN'
  if (value.includes('qoder')) return 'Qoder'
  if (value.includes('lingma')) return 'Lingma'
  if (value.includes('antigravity')) return 'Antigravity'
  if (value.includes('cursor')) return 'Cursor'
  if (value.includes('claude')) return 'Claude'
  if (value.includes('codex')) return 'Codex'
  if (value.includes('windsurf') || value.includes('codeium')) return 'Windsurf'
  if (value.includes('vscodium')) return 'VSCodium'
  if (value.includes('code') || value.includes('.vscode')) return 'VS Code'
  if (value.includes('zed')) return 'Zed'
  if (value.includes('continue')) return 'Continue'
  return ''
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

async function copyBridgeSetupCommand(command) {
  try {
    await navigator.clipboard?.writeText(command)
    emit('notice', '命令已复制')
  } catch (e) {
    emit('log', 'warn', '命令复制失败：' + (e.message || String(e)))
  }
}

async function installBridgeCLI() {
  await withBridgeAction('飞书 CLI 安装已触发', () => InstallFeishuCLI())
}

async function reinstallBridgeSkills() {
  await withBridgeAction('飞书 Skills 重新安装已触发', () => ReinstallFeishuSkills())
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
  if (!bridgeStatusLoaded.value) return false
  if (step.key === 'install') return true
  if (step.key === 'setup') return bridgeInstallStepDone.value
  if (step.key === 'auth') return bridgeInstallStepDone.value && bridgeSetupStepDone.value
  return false
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
        <p>配置监听地址、Lingma / QoderCN 传输方式、模型探测超时、会话复用和请求超时。</p>
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
            <label>Lingma / QoderCN 传输方式</label>
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
            <input v-model="config.WebSocketURL" type="text" placeholder="留空自动探测 Lingma / QoderCN WebSocket" />
          </div>
          <div class="field span-2">
            <label>Socket / Named Pipe</label>
            <input v-model="config.Pipe" type="text" placeholder="留空自动探测 macOS Socket / Windows Named Pipe" />
          </div>
          <div class="field span-2">
            <label>远端 API 域名</label>
            <input v-model="config.RemoteBaseURL" type="text" placeholder="留空自动探测，默认 https://lingma.alibabacloud.com" />
          </div>
          <div class="field span-2">
            <label>远端代理地址</label>
            <input v-model="config.RemoteProxyURL" type="text" placeholder="可选，例如 http://127.0.0.1:7890" />
            <small>仅影响远端 API 上游 HTTP 请求，不影响本地 IPC WebSocket / Named Pipe。</small>
          </div>
          <div class="field span-2">
            <label>远端认证文件</label>
            <input v-model="config.RemoteAuthFile" type="text" placeholder="可选 credentials.json；留空只读本机 Lingma / QoderCN 登录缓存" />
          </div>
          <div class="field span-2">
            <label>远端 Cosy 版本</label>
            <input v-model="config.RemoteVersion" type="text" placeholder="默认 2.11.2" />
          </div>
        </div>
        <div class="hint-box">
          <strong>自动探测失败时</strong>
          <span>IPC 模式先确认 Lingma / QoderCN 已启动并登录。远端 API 模式会优先读取认证文件；留空时只读本机 Lingma / QoderCN 登录缓存，不会写入或上传登录态。</span>
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
              <dt>远端代理</dt>
              <dd :class="{ 'warn-text': detection.remoteProxyError }">{{ remoteProxyStatus }}</dd>
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
            <p>仅在 IPC 插件模式下生效，影响 Lingma / QoderCN 会话上下文和工具执行环境。</p>
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
            <input v-model="config.Cwd" type="text" placeholder="Lingma / QoderCN 创建 session 时使用的 cwd" />
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
                  <div class="inline-help-popover inline-help-popover--commands">
                    <strong>飞书会话命令</strong>
                    <em>在飞书内直接发给机器人，仅对当前会话生效</em>
                    <h5>上下文管理</h5>
                    <span><code>/help</code>：查看完整命令帮助</span>
                    <span><code>/init</code>：让机器人自我介绍当前能力</span>
                    <span><code>/status</code>：查看本会话运行状态</span>
                    <span><code>/summary</code>：查看本会话压缩摘要</span>
                    <span><code>/compact</code>：手动压缩本会话上下文</span>
                    <span><code>/reset</code> · <code>/clear</code> · <code>/new</code>：清空本会话上下文</span>
                    <span><code>/undo</code>：撤回最近一轮（assistant + 关联 tool）</span>
                    <span><code>/retry</code>：撤回最近一轮并提示重新发送</span>
                    <span><code>/stop</code>：停止当前正在处理的任务</span>
                    <h5>模型与代理</h5>
                    <span><code>/models</code>：列出代理可用模型</span>
                    <span><code>/model &lt;name&gt;</code>：本会话切换模型；不带参数查看当前</span>
                    <span><code>/model default</code>：恢复使用全局默认模型</span>
                    <span><code>/mcp</code>：查看已启用 MCP server 与具体 tools</span>
                    <span><code>/cost</code>：查看本会话 prompt / completion tokens 估算</span>
                    <span><code>/context</code>：查看上下文预算、水位和压缩状态</span>
                    <h5>Skills</h5>
                    <span><code>/skills</code>：列出启用的用户导入 Skills</span>
                    <span><code>/skill &lt;name&gt;</code>：查看某个 Skill 摘要</span>
                    <span><code>/reload-skills</code>：重新扫描用户导入 Skills</span>
                    <span><code>/skill-run &lt;skill&gt; &lt;script&gt; confirm</code>：确认执行脚本</span>
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
            <div class="field">
              <label>高级设置</label>
              <button class="identity-config-button" type="button" @click="openBridgeAdvancedDialog">
                <span>{{ bridgeAdvancedLabel }}</span>
                <i class="bi bi-sliders" aria-hidden="true"></i>
              </button>
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

          <div class="hint-box compact-hint bridge-setup-hint">
          <div class="hint-title-row">
            <strong>推荐接入路径</strong>
            <button
              type="button"
              class="inline-help-trigger guide-help-trigger"
              aria-label="查看 Feishu Bridge 安装步骤"
              @click="bridgeSetupGuideOpen = true"
            >
              <i class="bi bi-question-circle" aria-hidden="true"></i>
            </button>
          </div>
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
              {{ bridgeStatusPending ? '检测中...' : (bridgeRefreshing ? '检测中...' : '刷新状态') }}
            </button>
          </div>
          <div v-if="bridgeStatusPending" class="status-loading-row">
            <span class="loading-dot"></span>
            <span>正在检测 Feishu Bridge 运行环境...</span>
          </div>
          <dl>
            <div>
              <dt>平台</dt>
              <dd>{{ bridgeStatusPending ? '检测中...' : `${bridgeStatus.platform} · ${bridgeStatus.arch}` }}</dd>
            </div>
            <div>
              <dt>Node / npm / npx</dt>
              <dd :class="{ 'warn-text': !bridgeStatusPending && (!bridgeStatus.node?.found || !bridgeStatus.npm?.found || !bridgeStatus.npx?.found) }">
                {{ bridgeStatusPending ? '检测中...' : `${bridgeStatus.node?.version || '缺失'} / ${bridgeStatus.npm?.version || '缺失'} / ${bridgeStatus.npx?.version || '缺失'}` }}
              </dd>
            </div>
            <div>
              <dt>lark-cli</dt>
              <dd :class="{ 'warn-text': !bridgeStatusPending && !bridgeStatus.cli?.found }">
                {{ bridgeStatusPending ? '检测中...' : (bridgeStatus.cli?.version || '未安装') }}
              </dd>
            </div>
            <div>
              <dt>Skills</dt>
              <dd :class="{ 'warn-text': !bridgeStatusPending && !bridgeStatus.skillsReady }">
                <span>{{ bridgeStatusPending ? '检测中...' : (bridgeStatus.installRunning && !bridgeStatus.skillsReady ? '安装中…' : (bridgeStatus.skillsReady ? '已就绪' : '缺失或未完整安装')) }}</span>
                <button
                  v-if="!bridgeStatusPending && !bridgeStatus.skillsReady && bridgeStatus.cli?.found && !bridgeStatus.installRunning"
                  type="button"
                  class="inline-action-btn"
                  @click="reinstallBridgeSkills"
                >
                  重新安装 Skills
                </button>
              </dd>
            </div>
            <div>
              <dt>MCP</dt>
              <dd :class="{ 'warn-text': !bridgeStatusPending && bridgeConfig.mcpEnabled && bridgeMCPServers.filter((server) => server.enabled && server.available).length === 0 }">
                {{ bridgeStatusPending ? '检测中...' : (bridgeConfig.mcpEnabled ? `${bridgeMCPServers.filter((server) => server.enabled && server.available).length}/${bridgeMCPServers.filter((server) => server.enabled).length} 已启用可用` : '未启用') }}
              </dd>
            </div>
            <div>
              <dt>CLI 配置</dt>
              <dd :class="{ 'warn-text': !bridgeStatusPending && !bridgeStatus.config?.configured }">
                {{ bridgeStatusPending ? '检测中...' : (bridgeStatus.config?.configured ? `${bridgeStatus.config?.appId || '已配置'} · ${bridgeStatus.config?.brand || 'feishu'}` : (bridgeStatus.config?.message || '未初始化')) }}
              </dd>
            </div>
            <div>
              <dt>授权状态</dt>
              <dd :class="{ 'warn-text': !bridgeStatusPending && !bridgeStatus.auth?.authorized }">
                {{ bridgeStatusPending ? '检测中...' : (bridgeStatus.auth?.authorized ? `${bridgeStatus.auth?.userName || '已授权'} · ${bridgeStatus.auth?.tokenStatus || 'ok'}` : (bridgeStatus.auth?.message || '未授权')) }}
              </dd>
            </div>
            <div>
              <dt>运行状态</dt>
              <dd>{{ bridgeStatusPending ? '检测中...' : (bridgeStatus.running ? `运行中 · ${formatDateTime(bridgeStatus.lastStartedAt) || bridgeStatus.lastStartedAt || ''}` : '未运行') }}</dd>
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

    <div v-if="bridgeSetupGuideOpen" class="modal-backdrop" @click.self="bridgeSetupGuideOpen = false">
      <section class="modal-card bridge-guide-modal">
        <div class="modal-header">
          <div>
            <h2>Feishu Bridge 安装指南</h2>
            <p>优先使用设置页三张卡片完成接入；自动安装失败时，再按下方命令手动兜底。</p>
          </div>
          <button class="secondary-button" type="button" @click="bridgeSetupGuideOpen = false">关闭</button>
        </div>
        <div class="modal-body bridge-guide-body">
          <div class="guide-section">
            <h3>推荐流程</h3>
            <ol>
              <li>点击“安装 CLI 与 Skills”，等待状态变为“已完成”。</li>
              <li>点击“初始化飞书应用”，在浏览器里创建或选择已有飞书应用。</li>
              <li>点击“登录并授权”，在浏览器里完成账号授权。</li>
              <li>三步都完成后，保存 Bridge 配置并启动 Bridge。</li>
            </ol>
          </div>

          <div class="guide-section">
            <h3>手动兜底命令</h3>
            <p>如果自动安装因为 npm registry、公司网络、PATH 或临时目录清理失败，可以逐条执行下面命令。</p>
            <div class="command-list">
              <div v-for="item in bridgeSetupCommands" :key="item.command" class="command-card">
                <div class="command-card-head">
                  <strong>{{ item.title }}</strong>
                  <button class="secondary-button" type="button" @click="copyBridgeSetupCommand(item.command)">复制</button>
                </div>
                <code>{{ item.command }}</code>
                <span>{{ item.note }}</span>
              </div>
            </div>
          </div>

          <div class="guide-section">
            <h3>环境注意事项</h3>
            <p>Node.js 需要 20.12+，推荐使用当前 LTS。Node 16 不支持当前飞书 CLI 安装链路。</p>
            <p>macOS 点击“安装飞书 CLI”时，会先扫描已有 Node；不满足要求时依次尝试 nvm、fnm、volta、Homebrew。没有这些工具时，需要先手动安装 Node.js。</p>
            <p>Windows 点击“安装飞书 CLI”时，会先扫描已有 Node；不满足要求时优先走 nvm-windows 安装/切换 LTS，再尝试 winget。上方 PowerShell 脚本用于自动安装失败后的手动兜底。</p>
            <p>如果 npm 全局目录是 <code>D:\node.js\node_global</code> 这类自定义路径，新版会自动探测；手动安装完成后回到设置页点击“刷新状态”。</p>
            <p>如果提示缺少权限 scope，按飞书对话里返回的授权链接补授权，完成后再次发送原问题。</p>
          </div>
        </div>
      </section>
    </div>

    <div v-if="bridgeAdvancedDialogOpen" class="modal-backdrop" @click.self="bridgeAdvancedDialogOpen = false">
      <section class="modal-card bridge-advanced-modal">
        <div class="modal-header">
          <div>
            <h2>Feishu Bridge 高级设置</h2>
            <p>Bot 名称只影响飞书回复卡片显示；MCP 工具默认只扫描展示，启用后才会暴露给 Bot 调用。</p>
          </div>
          <button class="secondary-button" type="button" @click="bridgeAdvancedDialogOpen = false">关闭</button>
        </div>
        <div class="modal-body bridge-advanced-body">
          <div class="advanced-section">
            <div class="field">
              <label>Bot 名称</label>
              <input
                v-model="bridgeBotNameDraft"
                maxlength="40"
                placeholder="留空时卡片只显示“正在思考 / 已完成”"
              />
            </div>
            <div class="field">
              <label>Bot 身份描述</label>
              <textarea
                v-model="bridgeIdentityDraft"
                maxlength="2000"
                placeholder="例如：你是研发效能助手，面向内部开发同学。回复要直接、简洁，优先给可执行结论。"
              ></textarea>
            </div>
            <p class="identity-note">这段内容不会替代工具调用规则、权限规则和真实数据约束。需要恢复默认时清空后保存。</p>
          </div>

          <div class="advanced-section">
            <div class="advanced-section-head">
              <div>
                <h3>上下文管理</h3>
                <p>Bridge 会在请求前估算上下文水位，并按水位压缩旧工具结果或刷新摘要。</p>
              </div>
              <label class="switch">
                <input v-model="bridgeContextDraft.autoCompact" type="checkbox" />
                <span></span>
              </label>
            </div>
            <div class="context-grid">
              <label>
                <span>模型窗口覆盖</span>
                <input v-model.number="bridgeContextDraft.contextWindowOverride" type="number" min="0" step="1000" placeholder="0 表示自动" />
              </label>
              <label>
                <span>压缩水位 %</span>
                <input v-model.number="bridgeContextDraft.compactWatermark" type="number" min="50" max="92" step="1" />
              </label>
              <label>
                <span>保留工具结果</span>
                <input v-model.number="bridgeContextDraft.toolResultRetention" type="number" min="1" max="12" step="1" />
              </label>
              <label>
                <span>HTTP 超时秒</span>
                <input v-model.number="bridgeContextDraft.skillHttpTimeout" type="number" min="5" max="300" step="5" />
              </label>
              <label>
                <span>HTTP 上限 bytes</span>
                <input v-model.number="bridgeContextDraft.skillHttpMaxBytes" type="number" min="262144" max="52428800" step="262144" />
              </label>
            </div>
          </div>

          <div class="advanced-section">
            <div class="advanced-section-head">
              <div>
                <h3>用户 Skills</h3>
                <p>导入 zip 或包含 SKILL.md 的文件夹；Bot 只看索引，使用时再按需读取正文。</p>
              </div>
              <button class="secondary-button" type="button" :disabled="bridgeSkillsLoading" @click="reloadBridgeSkills">
                {{ bridgeSkillsLoading ? '扫描中...' : '重新扫描' }}
              </button>
            </div>
            <div class="actions-row">
              <button class="secondary-button" type="button" :disabled="bridgeSkillImporting" @click="importBridgeSkill('folder')">
                导入文件夹
              </button>
              <button class="secondary-button" type="button" :disabled="bridgeSkillImporting" @click="importBridgeSkill('zip')">
                导入 zip
              </button>
            </div>
            <div class="skill-list">
              <div v-if="!bridgeSkillsLoading && bridgeSkills.length === 0" class="mcp-empty">
                暂未导入用户 Skill。
              </div>
              <div v-for="skill in bridgeSkills" :key="skill.id" class="skill-card" :class="{ disabled: !skill.enabled, invalid: skill.error }">
                <button class="skill-row" type="button" @click="toggleBridgeSkill(skill)">
                  <span class="mcp-server-toggle" :class="{ on: skill.enabled }"></span>
                  <span class="skill-main">
                    <strong>{{ skill.name }}</strong>
                    <small>{{ skill.description || '无描述' }}</small>
                    <em>{{ skill.path }}</em>
                  </span>
                  <span class="mcp-server-meta">{{ skill.enabled ? '启用' : '停用' }}</span>
                </button>
                <div v-if="Array.isArray(skill.scripts) && skill.scripts.length > 0" class="skill-scripts">
                  scripts: {{ skill.scripts.join(', ') }}
                </div>
                <div v-if="skill.error" class="mcp-json-error">{{ skill.error }}</div>
                <button class="text-danger-button" type="button" @click.stop="deleteBridgeSkill(skill)">删除</button>
              </div>
            </div>
          </div>

          <div class="advanced-section">
            <div class="advanced-section-head">
              <div>
                <h3>MCP 工具</h3>
                <p>优先读取本机已有 MCP 配置；单个 server 启用后才会进入 Bot 工具清单。</p>
              </div>
              <label class="switch">
                <input v-model="bridgeMCPEnabledDraft" type="checkbox" />
                <span></span>
              </label>
            </div>
            <div class="actions-row">
              <button class="secondary-button" type="button" @click="openBridgeMCPJSONDialog">
                自定义 MCP JSON
              </button>
              <button class="secondary-button" type="button" :disabled="bridgeRefreshing" @click="refreshBridgeAdvancedMCP">
                {{ bridgeRefreshing ? '扫描中...' : '扫描本机 MCP' }}
              </button>
            </div>
            <div class="mcp-server-list">
              <div v-if="bridgeMCPServersDraft.length === 0" class="mcp-empty">
                未扫描到 MCP 配置。当前会检查 Cursor、Claude、Codex、QoderCN、Lingma、Antigravity、Windsurf、VS Code、Zed、Continue 和项目级 .mcp.json 等主流路径。
              </div>
              <div v-for="group in bridgeMCPServerGroups" :key="group.source" class="mcp-source-group">
                <div class="mcp-source-title">{{ group.source }}</div>
                <div
                  v-for="server in group.servers"
                  :key="server.name"
                  class="mcp-server-card"
                  :class="{ enabled: server.enabled, unavailable: server.enabled && !server.available && server.message !== '未启用' }"
                >
                  <button
                    type="button"
                    class="mcp-server-row"
                    @click="toggleBridgeMCPServer(server.name)"
                  >
                    <span class="mcp-server-toggle" :class="{ on: server.enabled }"></span>
                    <span class="mcp-server-main">
                      <strong>{{ server.name }}</strong>
                      <small>{{ server.command }} {{ Array.isArray(server.args) ? server.args.join(' ') : '' }}</small>
                      <em>{{ server.source || '自定义配置' }}</em>
                    </span>
                    <span class="mcp-server-meta">
                      {{ bridgeMCPServerMeta(server) }}
                    </span>
                  </button>
                  <button
                    v-if="Array.isArray(server.tools) && server.tools.length > 0"
                    type="button"
                    class="mcp-tools-toggle"
                    @click.stop="toggleBridgeMCPTools(server.name)"
                  >
                    <span>{{ bridgeMCPExpanded[server.name] ? '⌄' : '›' }}</span>
                    <span>{{ server.tools.length }} 个转换后的 tools</span>
                  </button>
                  <div v-if="bridgeMCPExpanded[server.name] && Array.isArray(server.tools) && server.tools.length > 0" class="mcp-tool-list">
                    <div v-for="tool in server.tools" :key="bridgeMCPToolLabel(tool)" class="mcp-tool-chip">
                      <code>{{ bridgeMCPToolLabel(tool) }}</code>
                      <small v-if="bridgeMCPToolDescription(tool)">{{ bridgeMCPToolDescription(tool) }}</small>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
        <div class="modal-footer">
          <button class="secondary-button" type="button" @click="clearBridgeIdentity">清空</button>
          <div class="actions-row">
            <button class="secondary-button" type="button" @click="bridgeAdvancedDialogOpen = false">取消</button>
            <button class="primary-button" type="button" :disabled="bridgeSaving" @click="saveBridgeAdvancedSettings">
              {{ bridgeSaving ? '保存中...' : '保存生效' }}
            </button>
          </div>
        </div>
      </section>
    </div>

    <div v-if="bridgeMCPJSONDialogOpen" class="modal-backdrop" @click.self="bridgeMCPJSONDialogOpen = false">
      <section class="modal-card bridge-mcp-json-modal">
        <div class="modal-header">
          <div>
            <h2>自定义 MCP JSON</h2>
            <p>保存到应用自己的 MCP 配置文件，保存成功后会自动解析并刷新 MCP 列表。</p>
          </div>
          <button class="secondary-button" type="button" @click="bridgeMCPJSONDialogOpen = false">关闭</button>
        </div>
        <div class="modal-body">
          <div class="field">
            <label>配置文件</label>
            <input :value="bridgeMCPJSONPath" readonly />
          </div>
          <div class="field mcp-json-edit-pane">
            <label>JSON 文件内容</label>
            <textarea
              v-model="bridgeMCPJSONDraft"
              class="mcp-json-editor"
              spellcheck="false"
              placeholder='{"mcpServers":{"context7":{"command":"npx","args":["-y","@upstash/context7-mcp@latest"]}}}'
            ></textarea>
          </div>
          <p v-if="bridgeMCPJSONError" class="mcp-json-error">{{ bridgeMCPJSONError }}</p>
        </div>
        <div class="modal-footer">
          <button class="secondary-button" type="button" @click="bridgeMCPJSONDialogOpen = false">取消</button>
          <button class="primary-button" type="button" :disabled="bridgeMCPJSONSaving" @click="saveBridgeMCPJSON">
            {{ bridgeMCPJSONSaving ? '保存中...' : '保存并解析' }}
          </button>
        </div>
      </section>
    </div>
  </div>
</template>
