<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { BrowserOpenURL } from '../../wailsjs/runtime'
import {
  GetConfig,
  GetDetectionInfo,
  GetFeishuAgentConfig,
  GetFeishuAgentMCPJSON,
  GetFeishuAgentSkills,
  GetFeishuAgentStatus,
  GetModels,
  ChooseFeishuAgentSkillFolder,
  ChooseFeishuAgentSkillZip,
  CleanupFeishuAgentArtifacts,
  CheckForUpdates,
  CheckPromptPackUpdate,
  DeleteFeishuAgentSkill,
  DownloadAndInstallUpdate,
  GetOnlineUpdateStatus,
  ImportFeishuAgentSkillPath,
  InstallFeishuCLI,
  RefreshFeishuAgentStatus,
  RefreshModels,
  ReinstallFeishuSkills,
  ReloadFeishuAgentSkills,
  SetFeishuAgentSkillEnabled,
  StartFeishuAgent,
  StartFeishuCLILogin,
  StartFeishuCLISetupNew,
  StopFeishuAgent,
  UpdateConfig,
  UpdateFeishuAgentConfig,
  SaveFeishuAgentMCPJSON,
} from '../../wailsjs/go/main/App.js'
import { safeEventsOff, safeEventsOn } from '../utils/wailsSafe'

const emit = defineEmits(['log', 'status-refresh', 'notice'])

const config = ref({})
const detection = ref(null)
const saving = ref(false)
const agentConfig = ref({
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
  safeFiles: {
    configured: true,
    enabled: true,
    workspaceDir: '',
    extraPaths: [],
  },
  maxToolRounds: 0,
})
const agentStatus = ref(null)
const agentStatusLoaded = ref(false)
const agentInitialStatusLoading = ref(true)
const agentSaving = ref(false)
const agentBusy = ref(false)
const agentRefreshing = ref(false)
const agentSetupGuideOpen = ref(false)
const agentAdvancedDialogOpen = ref(false)
const agentMCPJSONDialogOpen = ref(false)
const agentBotNameDraft = ref('')
const agentIdentityDraft = ref('')
const agentMCPEnabledDraft = ref(false)
const agentMCPServersDraft = ref([])
const agentMCPExpanded = ref({})
const agentMCPJSONDraft = ref('')
const agentMCPJSONPath = ref('')
const agentMCPJSONError = ref('')
const agentMCPJSONSaving = ref(false)
const agentCleanupDialogOpen = ref(false)
const agentCleanupImportedSkills = ref(false)
const agentCleanupMCPConfig = ref(false)
const agentSkills = ref([])
const agentSkillsLoading = ref(false)
const agentSkillImporting = ref(false)
const updateStatus = ref(null)
const updateBusy = ref(false)
const promptPackBusy = ref(false)
const agentContextDraft = ref({
  autoCompact: true,
  compactWatermark: 75,
  toolResultRetention: 2,
  contextWindowOverride: 0,
  skillHttpTimeout: 60,
  skillHttpMaxBytes: 5242880,
})
const agentSafeFilesDraft = ref({
  enabled: true,
  workspaceDir: '',
  extraPathsText: '',
})
const openSelect = ref('')
const fallbackModelsText = ref('')
const availableAgentModels = ref([])
const isIPCBackend = computed(() => (config.value.Backend || 'ipc') === 'ipc')
const formattedTokenExpireAt = computed(() => formatDateTime(detection.value?.remoteTokenExpireAt))
const updateLastCheckedAt = computed(() => formatDateTime(updateStatus.value?.lastCheckedAt))
const promptPackLastCheckedAt = computed(() => formatDateTime(updateStatus.value?.promptPack?.lastCheckedAt))
const agentCurrentModel = computed(() => agentConfig.value.model || agentStatus.value?.currentModel || 'kmodel')
const agentModelOptions = computed(() => {
  const items = Array.isArray(availableAgentModels.value) ? [...availableAgentModels.value] : []
  const currentID = String(agentConfig.value.model || agentStatus.value?.currentModel || '').trim()
  if (currentID && !items.some((item) => item.id === currentID)) {
    items.unshift({ id: currentID, name: currentID })
  }
  return items
})
const agentModelLabel = computed(() => {
  const currentID = String(agentConfig.value.model || agentStatus.value?.currentModel || '').trim()
  const option = agentModelOptions.value.find((item) => item.id === currentID)
  return option?.name || option?.id || '请选择模型'
})
const agentReady = computed(() => {
  const status = agentStatus.value
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
const agentStatusPending = computed(() => {
  return agentInitialStatusLoading.value || !agentStatusLoaded.value || agentRefreshing.value
})
const commandHelpPinned = ref(false)
const agentSetupLinkVisible = computed(() => Boolean(agentStatus.value?.setupUrl) && !agentStatus.value?.config?.configured)
const agentLoginLinkVisible = computed(() => Boolean(agentStatus.value?.loginUrl) && !agentStatus.value?.auth?.authorized)
const agentBrowserHintVisible = computed(() => agentSetupLinkVisible.value || agentLoginLinkVisible.value)
const agentStartButtonLabel = computed(() => {
  if (agentBusy.value) return agentStatus.value?.running ? '停止中...' : '启动中...'
  if (agentStatus.value?.running) return '停止 Agent'
  if (agentConfig.value.enabled) return '启动 Agent'
  return '启用并启动 Agent'
})
const agentMCPServers = computed(() => Array.isArray(agentStatus.value?.mcpServers) ? agentStatus.value.mcpServers : [])
const agentMCPServerGroups = computed(() => groupMCPServersBySource(agentMCPServersDraft.value))
const agentAdvancedLabel = computed(() => {
  return '设置'
})
const agentInstallStepDone = computed(() => {
  const status = agentStatus.value
  return Boolean(status?.node?.found && status?.npm?.found && status?.npx?.found && status?.cli?.found && status?.skillsReady)
})
const agentSetupStepDone = computed(() => Boolean(agentStatus.value?.config?.configured))
const agentStepItems = computed(() => {
  const status = agentStatus.value
  if (!agentStatusLoaded.value) {
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
const lastAgentSnapshot = ref({
  configured: false,
  authorized: false,
  running: false,
})
let agentStatusPollTimer = null
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

const agentSetupCommands = [
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
    note: '返回 valid 后即可保存配置并启动 Agent。',
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

function chooseAgentModel(modelID) {
  agentConfig.value.model = modelID
  openSelect.value = ''
}

function toggleCommandHelp(event) {
  event?.stopPropagation?.()
  commandHelpPinned.value = !commandHelpPinned.value
}

function closeCommandHelp() {
  commandHelpPinned.value = false
}

function handleDocumentPointerDown(event) {
  if (!commandHelpPinned.value) return
  if (event.target?.closest?.('.inline-help--commands')) return
  commandHelpPinned.value = false
}

async function refreshAvailableAgentModels({ background = false } = {}) {
  try {
    const models = background ? await RefreshModels() : await GetModels()
    availableAgentModels.value = Array.isArray(models) ? models : []
  } catch (e) {
    if (!background) {
      emit('log', 'warn', 'Agent 模型列表读取失败：' + (e.message || String(e)))
    }
  }
}

async function refreshUpdateStatus() {
  try {
    updateStatus.value = await GetOnlineUpdateStatus()
  } catch (e) {
    emit('log', 'warn', '更新状态读取失败：' + (e.message || String(e)))
  }
}

async function checkOnlineUpdate() {
  updateBusy.value = true
  try {
    updateStatus.value = await CheckForUpdates()
    emit('notice', updateStatus.value?.available ? `发现新版本 ${updateStatus.value.latest}` : '当前已是最新版本')
  } catch (e) {
    await refreshUpdateStatus()
    emit('notice', '检查更新失败：' + (e.message || String(e)))
  } finally {
    updateBusy.value = false
  }
}

async function installOnlineUpdate() {
  updateBusy.value = true
  try {
    updateStatus.value = await DownloadAndInstallUpdate()
    emit('notice', '更新包已下载，已打开所在位置。请手动覆盖安装后重启应用。')
  } catch (e) {
    await refreshUpdateStatus()
    emit('notice', '下载更新失败：' + (e.message || String(e)))
  } finally {
    updateBusy.value = false
  }
}

async function checkPromptPack() {
  promptPackBusy.value = true
  try {
    await CheckPromptPackUpdate()
    await refreshUpdateStatus()
    emit('notice', `Prompt Pack 已更新到 ${updateStatus.value?.promptPack?.version || '最新版本'}`)
  } catch (e) {
    await refreshUpdateStatus()
    emit('notice', 'Prompt Pack 更新失败：' + (e.message || String(e)))
  } finally {
    promptPackBusy.value = false
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
  document.addEventListener('pointerdown', handleDocumentPointerDown)
  try {
    config.value = await GetConfig()
    agentConfig.value = await GetFeishuAgentConfig()
    await refreshAvailableAgentModels()
    if (availableAgentModels.value.length === 0) {
      refreshAvailableAgentModels({ background: true })
    }
    fallbackModelsText.value = Array.isArray(config.value.RemoteFallbackModels)
      ? config.value.RemoteFallbackModels.join('\n')
      : ''
    await refreshDetection()
    await refreshUpdateStatus()
    agentStatus.value = await GetFeishuAgentStatus()
    await refreshAgentStatus(false)
  } catch (e) {
    emit('log', 'error', '配置加载失败：' + (e.message || String(e)))
    if (agentStatus.value) {
      agentStatusLoaded.value = true
    }
    agentInitialStatusLoading.value = false
  }

  safeEventsOn('feishu:status', (nextStatus) => {
    applyAgentStatus(nextStatus, true)
  })
  safeEventsOn('models:updated', (models) => {
    availableAgentModels.value = Array.isArray(models) ? models : []
  })
  safeEventsOn('updates:status', (status) => {
    updateStatus.value = status
  })
})

onUnmounted(() => {
  document.removeEventListener('pointerdown', handleDocumentPointerDown)
  safeEventsOff('feishu:status')
  safeEventsOff('models:updated')
  safeEventsOff('updates:status')
  stopAgentStatusPolling()
})

async function refreshDetection() {
  try {
    detection.value = await GetDetectionInfo()
  } catch (e) {
    emit('log', 'warn', '探测信息加载失败：' + (e.message || String(e)))
  }
}

function applyAgentStatus(nextStatus, announce = false) {
  const prev = { ...lastAgentSnapshot.value }
  agentStatus.value = nextStatus
  agentStatusLoaded.value = true
  agentInitialStatusLoading.value = false
  lastAgentSnapshot.value = {
    configured: Boolean(nextStatus?.config?.configured),
    authorized: Boolean(nextStatus?.auth?.authorized),
    running: Boolean(nextStatus?.running),
  }
  syncAgentStatusPolling(nextStatus)
  if (!announce) return
  if (!prev.configured && nextStatus?.config?.configured) {
    emit('notice', `飞书 CLI 应用初始化已完成：${nextStatus?.config?.appId || '已配置'}`)
  }
  if (!prev.authorized && nextStatus?.auth?.authorized) {
    emit('notice', `飞书 CLI 授权已完成：${nextStatus?.auth?.userName || '当前账号'}`)
  }
  if (!prev.running && nextStatus?.running) {
    emit('notice', 'Feishu Agent 已开始监听飞书消息')
  }
}

function shouldPollAgentStatus(status) {
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

function stopAgentStatusPolling() {
  if (agentStatusPollTimer) {
    clearInterval(agentStatusPollTimer)
    agentStatusPollTimer = null
  }
}

function syncAgentStatusPolling(status) {
  if (!shouldPollAgentStatus(status)) {
    stopAgentStatusPolling()
    return
  }
  if (agentStatusPollTimer) return
  agentStatusPollTimer = setInterval(() => {
    refreshAgentStatus(false, false)
  }, 2500)
}

async function refreshAgentStatus(announce = true, showLoading = true) {
  try {
    if (showLoading) {
      agentRefreshing.value = true
    }
    applyAgentStatus(await RefreshFeishuAgentStatus(), announce)
  } catch (e) {
    emit('log', 'warn', 'Feishu Agent 状态加载失败：' + (e.message || String(e)))
  } finally {
    if (showLoading) {
      agentRefreshing.value = false
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

async function saveAgentConfig() {
  agentSaving.value = true
  try {
    agentConfig.value = {
      ...agentConfig.value,
      brand: 'feishu',
      maxToolRounds: 0,
    }
    await UpdateFeishuAgentConfig(agentConfig.value)
    emit('notice', 'Feishu Agent 配置已保存')
    emit('log', 'info', 'Feishu Agent 配置已保存')
    return true
  } catch (e) {
    emit('log', 'error', 'Feishu Agent 配置保存失败：' + (e.message || String(e)))
    return false
  } finally {
    agentSaving.value = false
  }
}

function agentAdvancedServersFromStatus() {
  const saved = new Map((Array.isArray(agentConfig.value.mcpServers) ? agentConfig.value.mcpServers : []).map((server) => [String(server.name || '').toLowerCase(), server]))
  const statusServers = Array.isArray(agentStatus.value?.mcpServers) ? agentStatus.value.mcpServers : []
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

function openAgentAdvancedDialog() {
  agentBotNameDraft.value = String(agentConfig.value.botName || '')
  agentIdentityDraft.value = String(agentConfig.value.botIdentity || '')
  agentMCPEnabledDraft.value = Boolean(agentConfig.value.mcpEnabled)
  agentMCPServersDraft.value = agentAdvancedServersFromStatus()
  agentContextDraft.value = {
    autoCompact: agentConfig.value.context?.autoCompact !== false,
    compactWatermark: Number(agentConfig.value.context?.compactWatermark || 75),
    toolResultRetention: Number(agentConfig.value.context?.toolResultRetention || 2),
    contextWindowOverride: Number(agentConfig.value.context?.contextWindowOverride || 0),
    skillHttpTimeout: Number(agentConfig.value.context?.skillHttpTimeout || 60),
    skillHttpMaxBytes: Number(agentConfig.value.context?.skillHttpMaxBytes || 5242880),
  }
  agentSafeFilesDraft.value = {
    enabled: agentConfig.value.safeFiles?.enabled !== false,
    workspaceDir: String(agentConfig.value.safeFiles?.workspaceDir || ''),
    extraPathsText: formatSafeFilePaths(agentConfig.value.safeFiles?.extraPaths || []),
  }
  refreshAgentSkills()
  agentAdvancedDialogOpen.value = true
}

async function refreshAgentSkills() {
  agentSkillsLoading.value = true
  try {
    agentSkills.value = await GetFeishuAgentSkills()
  } catch (e) {
    emit('log', 'warn', 'Agent Skills 读取失败：' + (e.message || String(e)))
    agentSkills.value = []
  } finally {
    agentSkillsLoading.value = false
  }
}

async function reloadAgentSkills() {
  agentSkillsLoading.value = true
  try {
    await ReloadFeishuAgentSkills()
    agentSkills.value = await GetFeishuAgentSkills()
    emit('notice', 'Feishu Agent Skills 已重新扫描')
  } catch (e) {
    emit('notice', 'Skills 扫描失败：' + (e.message || String(e)))
  } finally {
    agentSkillsLoading.value = false
  }
}

async function importAgentSkill(kind) {
  agentSkillImporting.value = true
  try {
    const path = kind === 'zip' ? await ChooseFeishuAgentSkillZip() : await ChooseFeishuAgentSkillFolder()
    if (!path) return
    const result = await ImportFeishuAgentSkillPath(path)
    agentSkills.value = await GetFeishuAgentSkills()
    const imported = Array.isArray(result?.imported) ? result.imported.length : 0
    const errors = Array.isArray(result?.errors) && result.errors.length > 0 ? `；${result.errors.length} 个错误` : ''
    emit('notice', `导入 ${imported} 个 Skill${errors}`)
  } catch (e) {
    emit('notice', 'Skill 导入失败：' + (e.message || String(e)))
  } finally {
    agentSkillImporting.value = false
  }
}

async function toggleAgentSkill(skill) {
  try {
    await SetFeishuAgentSkillEnabled(skill.id, !skill.enabled)
    agentSkills.value = await GetFeishuAgentSkills()
  } catch (e) {
    emit('notice', 'Skill 状态更新失败：' + (e.message || String(e)))
  }
}

async function deleteAgentSkill(skill) {
  try {
    await DeleteFeishuAgentSkill(skill.id)
    agentSkills.value = await GetFeishuAgentSkills()
  } catch (e) {
    emit('notice', 'Skill 删除失败：' + (e.message || String(e)))
  }
}

async function refreshAgentAdvancedMCP() {
  await refreshAgentStatus(true, true)
  agentMCPServersDraft.value = agentAdvancedServersFromStatus()
}

async function openAgentMCPJSONDialog() {
  agentMCPJSONError.value = ''
  try {
    const result = await GetFeishuAgentMCPJSON()
    agentMCPJSONDraft.value = result?.content || ''
    agentMCPJSONPath.value = result?.path || ''
    agentMCPJSONDialogOpen.value = true
  } catch (e) {
    agentMCPJSONError.value = e.message || String(e)
    agentMCPJSONDraft.value = ''
    agentMCPJSONPath.value = ''
    agentMCPJSONDialogOpen.value = true
  }
}

async function saveAgentMCPJSON() {
  agentMCPJSONSaving.value = true
  agentMCPJSONError.value = ''
  try {
    const result = await SaveFeishuAgentMCPJSON(agentMCPJSONDraft.value)
    agentMCPJSONDraft.value = result?.content || agentMCPJSONDraft.value
    agentMCPJSONPath.value = result?.path || agentMCPJSONPath.value
    emit('notice', `自定义 MCP JSON 已保存，解析到 ${result?.serverCount || 0} 个 server`)
    await refreshAgentStatus(false, true)
    agentMCPServersDraft.value = agentAdvancedServersFromStatus()
    agentMCPJSONDialogOpen.value = false
  } catch (e) {
    agentMCPJSONError.value = e.message || String(e)
  } finally {
    agentMCPJSONSaving.value = false
  }
}

function toggleAgentMCPServer(name) {
  agentMCPServersDraft.value = agentMCPServersDraft.value.map((server) => {
    if (server.name !== name) return server
    const enabled = !server.enabled
    return { ...server, enabled, message: enabled && server.message === '未启用' ? '' : server.message }
  })
}

function toggleAgentMCPTools(name) {
  const key = String(name || '')
  agentMCPExpanded.value = {
    ...agentMCPExpanded.value,
    [key]: !agentMCPExpanded.value[key],
  }
}

function agentMCPToolLabel(tool) {
  return tool?.function || tool?.name || 'unknown_tool'
}

function agentMCPToolDescription(tool) {
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

function agentMCPServerMeta(server) {
  if (!server?.enabled) return '未启用'
  if (!agentMCPEnabledDraft.value) return '需打开总开关'
  if (server.available) return `${server.toolCount || 0} tools`
  const message = String(server.message || '').trim()
  if (message && message !== '未启用') return message
  const saved = Array.isArray(agentConfig.value.mcpServers)
    ? agentConfig.value.mcpServers.find((item) => String(item.name || '').toLowerCase() === String(server.name || '').toLowerCase())
    : null
  if (!agentConfig.value.mcpEnabled || !saved?.enabled) return '保存后检测'
  return agentRefreshing.value ? '检测中...' : '待检测'
}

function formatSafeFilePaths(paths) {
  return (Array.isArray(paths) ? paths : [])
    .map((item) => {
      const path = String(item?.path || '').trim()
      if (!path) return ''
      const mode = String(item?.mode || 'read').trim() || 'read'
      return `${mode} ${path}`
    })
    .filter(Boolean)
    .join('\n')
}

function parseSafeFilePaths(text) {
  const items = []
  const seen = new Set()
  for (const rawLine of String(text || '').split('\n')) {
    const line = rawLine.trim()
    if (!line) continue
    const match = line.match(/^(read|write|delete)\s+(.+)$/i)
    const mode = match ? match[1].toLowerCase() : 'read'
    const path = (match ? match[2] : line).trim()
    if (!path) continue
    const key = `${mode}\u0000${path}`
    if (seen.has(key)) continue
    seen.add(key)
    items.push({ mode, path })
  }
  return items
}

async function saveAgentAdvancedSettings() {
  agentConfig.value = {
    ...agentConfig.value,
    botName: agentBotNameDraft.value.trim(),
    botIdentity: agentIdentityDraft.value.trim(),
    mcpEnabled: agentMCPEnabledDraft.value,
    context: {
      autoCompact: agentContextDraft.value.autoCompact,
      compactWatermark: Number(agentContextDraft.value.compactWatermark || 75),
      toolResultRetention: Number(agentContextDraft.value.toolResultRetention || 2),
      contextWindowOverride: Number(agentContextDraft.value.contextWindowOverride || 0),
      skillHttpTimeout: Number(agentContextDraft.value.skillHttpTimeout || 60),
      skillHttpMaxBytes: Number(agentContextDraft.value.skillHttpMaxBytes || 5242880),
    },
    safeFiles: {
      configured: true,
      enabled: agentSafeFilesDraft.value.enabled !== false,
      workspaceDir: agentSafeFilesDraft.value.workspaceDir.trim(),
      extraPaths: parseSafeFilePaths(agentSafeFilesDraft.value.extraPathsText),
    },
    mcpServers: agentMCPServersDraft.value.map((server) => ({
      name: server.name,
      source: server.source,
      sourceClient: server.sourceClient,
      command: server.command,
      args: Array.isArray(server.args) ? server.args : [],
      env: {},
      enabled: Boolean(server.enabled),
    })),
  }
  const saved = await saveAgentConfig()
  if (saved) {
    agentAdvancedDialogOpen.value = false
    await refreshAgentStatus(false, false)
  }
}

function clearAgentIdentity() {
  agentIdentityDraft.value = ''
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

async function withAgentAction(message, action) {
  agentBusy.value = true
  try {
    await action()
    emit('notice', message)
    emit('log', 'info', message)
    await refreshAgentStatus(true, false)
  } catch (e) {
    emit('log', 'error', message + '失败：' + (e.message || String(e)))
  } finally {
    agentBusy.value = false
  }
}

function openAgentURL(url) {
  if (!url) return
  BrowserOpenURL(url)
}

async function copyAgentSetupCommand(command) {
  try {
    await navigator.clipboard?.writeText(command)
    emit('notice', '命令已复制')
  } catch (e) {
    emit('log', 'warn', '命令复制失败：' + (e.message || String(e)))
  }
}

async function installAgentCLI() {
  await withAgentAction('飞书 CLI 安装已触发', () => InstallFeishuCLI())
}

async function reinstallAgentSkills() {
  await withAgentAction('飞书 Skills 重新安装已触发', () => ReinstallFeishuSkills())
}

function openAgentCleanupDialog() {
  agentCleanupImportedSkills.value = false
  agentCleanupMCPConfig.value = false
  agentAdvancedDialogOpen.value = false
  agentCleanupDialogOpen.value = true
}

async function cleanupAgentArtifacts() {
  await withAgentAction('飞书 CLI/Skills/授权信息已清理', async () => {
    const results = await CleanupFeishuAgentArtifacts({
      includeImportedSkills: agentCleanupImportedSkills.value,
      includeMcpConfig: agentCleanupMCPConfig.value,
    })
    if (Array.isArray(results) && results.length > 0) {
      emit('log', 'info', results.join('；'))
    }
  })
  agentCleanupDialogOpen.value = false
}

async function startAgentSetupNew() {
  await withAgentAction('飞书 CLI 首次初始化已启动', () => StartFeishuCLISetupNew())
}

async function startAgentLogin() {
  await withAgentAction('飞书 CLI 授权流程已启动', () => StartFeishuCLILogin())
}

async function enableAgentAndStart() {
  const saved = await saveAgentConfig()
  if (!saved) return
  await withAgentAction('Feishu Agent 已启动', async () => {
    agentConfig.value.enabled = true
    await UpdateFeishuAgentConfig(agentConfig.value)
    await StartFeishuAgent()
  })
}

async function stopAgent() {
  await withAgentAction('Feishu Agent 已停止', async () => {
    agentConfig.value.enabled = false
    await UpdateFeishuAgentConfig(agentConfig.value)
    await StopFeishuAgent()
  })
}

async function handleAgentPrimaryAction() {
  if (agentStatus.value?.running) {
    await stopAgent()
    return
  }
  await enableAgentAndStart()
}

function isAgentStepClickable(step) {
  if (!step || step.done) return false
  if (agentBusy.value || agentRefreshing.value) return false
  if (!agentStatusLoaded.value) return false
  if (step.key === 'install') return true
  if (step.key === 'setup') return agentInstallStepDone.value
  if (step.key === 'auth') return agentInstallStepDone.value && agentSetupStepDone.value
  return false
}

function agentStepCTA(step) {
  if (!isAgentStepClickable(step)) return ''
  return {
    install: '点击安装',
    setup: agentStatus.value?.setupUrl ? '继续初始化' : '开始初始化',
    auth: agentStatus.value?.loginUrl ? '继续授权' : '开始授权',
  }[step.key] || '继续'
}

async function handleAgentStepClick(step) {
  if (!isAgentStepClickable(step)) return
  if (step.key === 'install') {
    await installAgentCLI()
    return
  }
  if (step.key === 'setup') {
    if (agentStatus.value?.setupUrl) {
      openAgentURL(agentStatus.value.setupUrl)
      emit('notice', '已打开飞书应用初始化链接')
      return
    }
    await startAgentSetupNew()
    return
  }
  if (step.key === 'auth') {
    if (agentStatus.value?.loginUrl) {
      openAgentURL(agentStatus.value.loginUrl)
      emit('notice', '已打开飞书授权链接')
      return
    }
    await startAgentLogin()
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

    <section class="glass-panel update-panel">
      <div class="panel-header">
        <div>
          <h2>在线更新</h2>
          <p>Feishu Agent 专用通道。App 只读取公开 manifest，下载前会校验 sha256 和签名，安装由你手动完成。</p>
        </div>
        <span class="status-chip" :class="updateStatus?.available ? 'warn' : 'ok'">
          {{ updateStatus?.available ? `发现 ${updateStatus.latest}` : '已就绪' }}
        </span>
      </div>
      <div class="update-grid">
        <div class="update-card">
          <strong>应用版本</strong>
          <span>当前 {{ updateStatus?.current || 'dev' }}</span>
          <small v-if="updateLastCheckedAt">上次检查：{{ updateLastCheckedAt }}</small>
          <small v-else>尚未检查更新</small>
          <em v-if="updateStatus?.lastError">{{ updateStatus.lastError }}</em>
        </div>
        <div class="update-card">
          <strong>Prompt Pack</strong>
          <span>{{ updateStatus?.promptPack?.version || 'embedded' }} · {{ updateStatus?.promptPack?.source || 'embedded' }}</span>
          <small v-if="promptPackLastCheckedAt">上次检查：{{ promptPackLastCheckedAt }}</small>
          <small v-else>使用内置规则</small>
          <em v-if="updateStatus?.promptPack?.lastError">{{ updateStatus.promptPack.lastError }}</em>
        </div>
        <div class="update-actions">
          <button class="secondary-button" type="button" :disabled="updateBusy" @click="checkOnlineUpdate">
            {{ updateBusy ? '检查中...' : '检查 App 更新' }}
          </button>
          <button class="primary-button" type="button" :disabled="updateBusy || !updateStatus?.available" @click="installOnlineUpdate">
            {{ updateStatus?.progress > 0 && updateStatus?.progress < 100 ? `下载 ${updateStatus.progress}%` : '下载更新包' }}
          </button>
          <button class="secondary-button" type="button" :disabled="promptPackBusy" @click="checkPromptPack">
            {{ promptPackBusy ? '更新中...' : '更新 Prompt Pack' }}
          </button>
        </div>
      </div>
    </section>

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
                <h2>Feishu Bot Agent</h2>
                <div class="inline-help inline-help--commands" :class="{ pinned: commandHelpPinned }">
                  <button type="button" class="inline-help-trigger" aria-label="查看会话命令说明" @click="toggleCommandHelp">
                    <i class="bi bi-question-circle" aria-hidden="true"></i>
                  </button>
                  <div class="inline-help-popover inline-help-popover--commands" @pointerdown.stop>
                    <div class="inline-help-popover-head">
                      <strong>飞书会话命令</strong>
                      <button type="button" class="inline-help-close" aria-label="关闭命令说明" @click="closeCommandHelp">
                        <i class="bi bi-x-lg" aria-hidden="true"></i>
                      </button>
                    </div>
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
                    <h5>定时任务</h5>
                    <span><code>/schedule</code>：列出本群所有定时任务</span>
                    <span><code>/schedule delete &lt;id&gt;</code>：删除指定定时任务</span>
                    <span><code>/schedule pause &lt;id&gt;</code>：暂停定时任务</span>
                    <span><code>/schedule resume &lt;id&gt;</code>：恢复已暂停的任务</span>
                    <span><code>/schedule run &lt;id&gt;</code>：手动立即执行一次</span>
                  </div>
                </div>
              </div>
              <p>通过本机 lark-cli 把飞书 Bot 消息桥接到 Lingma Proxy。默认关闭，不影响现有代理功能。</p>
            </div>
            <span class="status-chip" :class="agentStatus?.running ? 'ok' : 'warn'">
              {{ agentStatus?.running ? '运行中' : '未运行' }}
            </span>
          </div>

          <div class="form-grid compact-form-grid">
            <div class="field">
              <label>启用 Agent</label>
              <label class="switch">
                <input v-model="agentConfig.enabled" type="checkbox" />
                <span></span>
              </label>
            </div>
            <div class="field">
              <label>随代理自动启动</label>
              <label class="switch">
                <input v-model="agentConfig.autoStart" type="checkbox" />
                <span></span>
              </label>
            </div>
            <div class="field">
              <label>模型</label>
              <div class="custom-select" :class="{ open: openSelect === 'AgentModel' }">
                <button type="button" @click="toggleSelect('AgentModel')">
                  <span>{{ agentModelLabel }}</span>
                  <i class="bi bi-chevron-down" aria-hidden="true"></i>
                </button>
                <div v-if="openSelect === 'AgentModel'" class="select-menu">
                  <button
                    v-for="model in agentModelOptions"
                    :key="model.id"
                    :class="{ selected: model.id === agentConfig.model }"
                    type="button"
                    @click="chooseAgentModel(model.id)"
                  >
                    {{ model.name || model.id }}
                  </button>
                </div>
              </div>
            </div>
            <div class="field">
              <label>高级设置</label>
              <button class="identity-config-button" type="button" @click="openAgentAdvancedDialog">
                <span>{{ agentAdvancedLabel }}</span>
                <i class="bi bi-sliders" aria-hidden="true"></i>
              </button>
            </div>
          </div>

        <div class="actions-row agent-actions-top">
          <button class="primary-button" type="button" :disabled="agentBusy" @click="saveAgentConfig">
            {{ agentSaving ? '保存中...' : '保存 Agent 配置' }}
          </button>
          <button
            class="primary-button"
            type="button"
            :class="{ 'danger-button': agentStatus && agentStatus.running }"
            :disabled="agentBusy || (!(agentStatus && agentStatus.running) && !agentReady)"
            @click="handleAgentPrimaryAction"
          >
            {{ agentStartButtonLabel }}
          </button>
        </div>

          <div class="hint-box compact-hint agent-setup-hint">
          <div class="hint-title-row">
            <strong>推荐接入路径</strong>
            <button
              type="button"
              class="inline-help-trigger guide-help-trigger"
              aria-label="查看 Feishu Agent 安装步骤"
              @click="agentSetupGuideOpen = true"
            >
              <i class="bi bi-question-circle" aria-hidden="true"></i>
            </button>
          </div>
          <span>按顺序完成“安装 CLI 与 Skills” → “初始化飞书应用” → “登录并授权”，全部完成后再保存配置并启动 Agent。</span>
        </div>

          <div class="agent-progress">
          <button
            v-for="step in agentStepItems"
            :key="step.key"
            class="agent-progress-item"
            :class="{ done: step.done, active: step.active, clickable: isAgentStepClickable(step) }"
            type="button"
            :disabled="!isAgentStepClickable(step)"
            @click="handleAgentStepClick(step)"
          >
            <div class="agent-progress-head">
              <strong>{{ step.title }}</strong>
              <div class="agent-progress-meta">
                <span class="agent-progress-state">
                  {{ step.done ? '已完成' : (step.active ? '进行中' : '待完成') }}
                </span>
                <span v-if="isAgentStepClickable(step)" class="agent-progress-cta">
                  {{ agentStepCTA(step) }}
                  <i class="bi bi-arrow-right-short" aria-hidden="true"></i>
                </span>
              </div>
            </div>
            <p>{{ step.detail }}</p>
          </button>
        </div>

          <div v-if="agentStatus" class="detect-card">
          <div class="detect-title">
            <strong>当前状态</strong>
            <div class="actions-row compact-actions">
              <button type="button" :disabled="agentRefreshing || agentBusy" @click="refreshAgentStatus">
                {{ agentStatusPending ? '检测中...' : (agentRefreshing ? '检测中...' : '刷新状态') }}
              </button>
            </div>
          </div>
          <div v-if="agentStatusPending" class="status-loading-row">
            <span class="loading-dot"></span>
            <span>正在检测 Feishu Agent 运行环境...</span>
          </div>
          <dl>
            <div>
              <dt>平台</dt>
              <dd>{{ agentStatusPending ? '检测中...' : `${agentStatus.platform} · ${agentStatus.arch}` }}</dd>
            </div>
            <div>
              <dt>Node / npm / npx</dt>
              <dd :class="{ 'warn-text': !agentStatusPending && (!agentStatus.node?.found || !agentStatus.npm?.found || !agentStatus.npx?.found) }">
                {{ agentStatusPending ? '检测中...' : `${agentStatus.node?.version || '缺失'} / ${agentStatus.npm?.version || '缺失'} / ${agentStatus.npx?.version || '缺失'}` }}
              </dd>
            </div>
            <div>
              <dt>lark-cli</dt>
              <dd :class="{ 'warn-text': !agentStatusPending && !agentStatus.cli?.found }">
                {{ agentStatusPending ? '检测中...' : (agentStatus.cli?.version || '未安装') }}
              </dd>
            </div>
            <div>
              <dt>Skills</dt>
              <dd :class="{ 'warn-text': !agentStatusPending && !agentStatus.skillsReady }">
                <span>{{ agentStatusPending ? '检测中...' : (agentStatus.installRunning && !agentStatus.skillsReady ? '安装中…' : (agentStatus.skillsReady ? '已就绪' : '缺失或未完整安装')) }}</span>
                <button
                  v-if="!agentStatusPending && !agentStatus.skillsReady && agentStatus.cli?.found && !agentStatus.installRunning"
                  type="button"
                  class="inline-action-btn"
                  @click="reinstallAgentSkills"
                >
                  重新安装 Skills
                </button>
              </dd>
            </div>
            <div>
              <dt>MCP</dt>
              <dd :class="{ 'warn-text': !agentStatusPending && agentConfig.mcpEnabled && agentMCPServers.filter((server) => server.enabled && server.available).length === 0 }">
                {{ agentStatusPending ? '检测中...' : (agentConfig.mcpEnabled ? `${agentMCPServers.filter((server) => server.enabled && server.available).length}/${agentMCPServers.filter((server) => server.enabled).length} 已启用可用` : '未启用') }}
              </dd>
            </div>
            <div>
              <dt>CLI 配置</dt>
              <dd :class="{ 'warn-text': !agentStatusPending && !agentStatus.config?.configured }">
                {{ agentStatusPending ? '检测中...' : (agentStatus.config?.configured ? `${agentStatus.config?.appId || '已配置'} · ${agentStatus.config?.brand || 'feishu'}` : (agentStatus.config?.message || '未初始化')) }}
              </dd>
            </div>
            <div>
              <dt>授权状态</dt>
              <dd :class="{ 'warn-text': !agentStatusPending && !agentStatus.auth?.authorized }">
                {{ agentStatusPending ? '检测中...' : (agentStatus.auth?.authorized ? `${agentStatus.auth?.userName || '已授权'} · ${agentStatus.auth?.tokenStatus || 'ok'}` : (agentStatus.auth?.message || '未授权')) }}
              </dd>
            </div>
            <div>
              <dt>运行状态</dt>
              <dd>{{ agentStatusPending ? '检测中...' : (agentStatus.running ? `运行中 · ${formatDateTime(agentStatus.lastStartedAt) || agentStatus.lastStartedAt || ''}` : '未运行') }}</dd>
            </div>
            <div v-if="agentStatusPending || agentStatus.lastCheckedAt">
              <dt>最近检测</dt>
              <dd>{{ agentStatusPending ? '-' : (formatDateTime(agentStatus.lastCheckedAt) || agentStatus.lastCheckedAt) }}</dd>
            </div>
            <div v-if="agentStatus.lastOutput">
              <dt>最近输出</dt>
              <dd>{{ agentStatus.lastOutput }}</dd>
            </div>
            <div v-if="agentStatus.lastError">
              <dt>最近错误</dt>
              <dd class="warn-text">{{ agentStatus.lastError }}</dd>
            </div>
          </dl>
        </div>

          <div v-if="agentBrowserHintVisible" class="hint-box">
          <strong>浏览器继续</strong>
          <span>首次初始化和登录授权需要在浏览器中完成，点击下方链接继续。</span>
          <div class="actions-row">
            <button v-if="agentSetupLinkVisible" type="button" @click="openAgentURL(agentStatus.setupUrl)">打开初始化链接</button>
            <button v-if="agentLoginLinkVisible" type="button" @click="openAgentURL(agentStatus.loginUrl)">打开授权链接</button>
          </div>
        </div>
        </div>
      </div>
    </section>

    <div v-if="agentSetupGuideOpen" class="modal-backdrop" @click.self="agentSetupGuideOpen = false">
      <section class="modal-card agent-guide-modal">
        <div class="modal-header">
          <div>
            <h2>Feishu Agent 安装指南</h2>
            <p>优先使用设置页三张卡片完成接入；自动安装失败时，再按下方命令手动兜底。</p>
          </div>
          <button class="secondary-button" type="button" @click="agentSetupGuideOpen = false">关闭</button>
        </div>
        <div class="modal-body agent-guide-body">
          <div class="guide-section">
            <h3>推荐流程</h3>
            <ol>
              <li>点击“安装 CLI 与 Skills”，等待状态变为“已完成”。</li>
              <li>点击“初始化飞书应用”，在浏览器里创建或选择已有飞书应用。</li>
              <li>点击“登录并授权”，在浏览器里完成账号授权。</li>
              <li>三步都完成后，保存 Agent 配置并启动 Agent。</li>
            </ol>
          </div>

          <div class="guide-section">
            <h3>手动兜底命令</h3>
            <p>如果自动安装因为 npm registry、公司网络、PATH 或临时目录清理失败，可以逐条执行下面命令。</p>
            <div class="command-list">
              <div v-for="item in agentSetupCommands" :key="item.command" class="command-card">
                <div class="command-card-head">
                  <strong>{{ item.title }}</strong>
                  <button class="secondary-button" type="button" @click="copyAgentSetupCommand(item.command)">复制</button>
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

    <div v-if="agentCleanupDialogOpen" class="modal-backdrop" @click.self="agentCleanupDialogOpen = false">
      <section class="modal-card agent-cleanup-modal">
        <div class="modal-header">
          <div>
            <h2>清理 Feishu Agent 环境</h2>
            <p>用于排查 CLI、Skills 或授权异常。默认不动 Node/npm/npx。</p>
          </div>
          <button class="secondary-button" type="button" @click="agentCleanupDialogOpen = false">关闭</button>
        </div>
        <div class="modal-body agent-cleanup-body">
          <div class="hint-box compact-hint">
            <strong>默认清理</strong>
            <span>会卸载全局 @larksuite/cli，移除官方 lark-* Skills，并清理 ~/.lark-cli 下的配置、授权、事件和缓存。</span>
          </div>
          <div class="checkbox-grid cleanup-checkbox-grid">
            <label class="checkbox-item">
              <input v-model="agentCleanupImportedSkills" type="checkbox" />
              <span>同时清理用户导入的 Agent Skills</span>
            </label>
            <label class="checkbox-item">
              <input v-model="agentCleanupMCPConfig" type="checkbox" />
              <span>同时清理自定义 MCP JSON，并关闭 MCP</span>
            </label>
          </div>
          <div v-if="agentBusy || agentStatus?.lastOutput || agentStatus?.lastError" class="cleanup-progress-box">
            <strong>{{ agentBusy ? '清理进度' : '最近清理结果' }}</strong>
            <span v-if="agentStatus?.lastOutput">{{ agentStatus.lastOutput }}</span>
            <span v-if="agentStatus?.lastError" class="warn-text">{{ agentStatus.lastError }}</span>
          </div>
          <p class="identity-note">不会删除 Node.js、npm、npx，也不会修改 Cursor / Claude / Lingma / QoderCN 等外部客户端自己的 MCP 配置。</p>
        </div>
        <div class="modal-footer">
          <button class="secondary-button" type="button" @click="agentCleanupDialogOpen = false">取消</button>
          <button class="danger-button" type="button" :disabled="agentBusy" @click="cleanupAgentArtifacts">
            {{ agentBusy ? '清理中...' : '确认清理' }}
          </button>
        </div>
      </section>
    </div>

    <div v-if="agentAdvancedDialogOpen" class="modal-backdrop" @click.self="agentAdvancedDialogOpen = false">
      <section class="modal-card agent-advanced-modal">
        <div class="modal-header">
          <div>
            <h2>Feishu Agent 高级设置</h2>
            <p>Bot 名称只影响飞书回复卡片显示；MCP 工具默认只扫描展示，启用后才会暴露给 Bot 调用。</p>
          </div>
          <button class="secondary-button" type="button" @click="agentAdvancedDialogOpen = false">关闭</button>
        </div>
        <div class="modal-body agent-advanced-body">
          <div class="advanced-section">
            <div class="field">
              <label>Bot 名称</label>
              <input
                v-model="agentBotNameDraft"
                maxlength="40"
                placeholder="留空时卡片只显示“正在思考 / 已完成”"
              />
            </div>
            <div class="field">
              <label>Bot 身份描述</label>
              <textarea
                v-model="agentIdentityDraft"
                maxlength="2000"
                placeholder="例如：你是研发效能助手，面向内部开发同学。回复要直接、简洁，优先给可执行结论。"
              ></textarea>
            </div>
            <p class="identity-note">这段内容不会替代工具调用规则、权限规则和真实数据约束。需要恢复默认时清空后保存。</p>
          </div>

          <div class="advanced-section">
            <div class="advanced-section-head">
              <div>
                <h3>环境维护</h3>
                <p>排查飞书 CLI、官方 Skills 或授权异常时使用。默认不动 Node/npm/npx。</p>
              </div>
              <button type="button" class="danger-outline-button" :disabled="agentBusy || agentRefreshing" @click="openAgentCleanupDialog">
                清理 CLI/Skills/授权
              </button>
            </div>
          </div>

          <div class="advanced-section">
            <div class="advanced-section-head">
              <div>
                <h3>本机文件访问</h3>
                <p>默认只允许应用 workspace 可读写；其他目录读取需授权，写入和删除必须在这里显式配置。</p>
              </div>
              <label class="switch">
                <input v-model="agentSafeFilesDraft.enabled" type="checkbox" />
                <span></span>
              </label>
            </div>
            <div class="field">
              <label>默认 workspace</label>
              <input
                v-model="agentSafeFilesDraft.workspaceDir"
                placeholder="留空使用应用默认 workspace"
              />
            </div>
            <div class="field">
              <label>额外授权路径</label>
              <textarea
                v-model="agentSafeFilesDraft.extraPathsText"
                class="safe-files-textarea"
                placeholder="每行一条：read /绝对路径&#10;write /绝对路径&#10;delete /绝对路径"
              ></textarea>
            </div>
            <p class="identity-note">read 只允许读取；write 允许创建/覆盖文件；delete 允许删除文件。覆盖仍需用户发送“确认覆盖 文件名”，删除仍需“确认删除 文件名”。</p>
          </div>

          <div class="advanced-section">
            <div class="advanced-section-head">
              <div>
                <h3>上下文管理</h3>
                <p>Agent 会在请求前估算上下文水位，并按水位压缩旧工具结果或刷新摘要。</p>
              </div>
              <label class="switch">
                <input v-model="agentContextDraft.autoCompact" type="checkbox" />
                <span></span>
              </label>
            </div>
            <div class="context-grid">
              <label>
                <span>模型窗口覆盖</span>
                <input v-model.number="agentContextDraft.contextWindowOverride" type="number" min="0" step="1000" placeholder="0 表示自动" />
              </label>
              <label>
                <span>压缩水位 %</span>
                <input v-model.number="agentContextDraft.compactWatermark" type="number" min="50" max="92" step="1" />
              </label>
              <label>
                <span>保留工具结果</span>
                <input v-model.number="agentContextDraft.toolResultRetention" type="number" min="1" max="12" step="1" />
              </label>
              <label>
                <span>HTTP 超时秒</span>
                <input v-model.number="agentContextDraft.skillHttpTimeout" type="number" min="5" max="300" step="5" />
              </label>
              <label>
                <span>HTTP 上限 bytes</span>
                <input v-model.number="agentContextDraft.skillHttpMaxBytes" type="number" min="262144" max="52428800" step="262144" />
              </label>
            </div>
          </div>

          <div class="advanced-section">
            <div class="advanced-section-head">
              <div>
                <h3>用户 Skills</h3>
                <p>导入 zip 或包含 SKILL.md 的文件夹；Bot 只看索引，使用时再按需读取正文。</p>
              </div>
              <button class="secondary-button" type="button" :disabled="agentSkillsLoading" @click="reloadAgentSkills">
                {{ agentSkillsLoading ? '扫描中...' : '重新扫描' }}
              </button>
            </div>
            <div class="actions-row">
              <button class="secondary-button" type="button" :disabled="agentSkillImporting" @click="importAgentSkill('folder')">
                导入文件夹
              </button>
              <button class="secondary-button" type="button" :disabled="agentSkillImporting" @click="importAgentSkill('zip')">
                导入 zip
              </button>
            </div>
            <div class="skill-list">
              <div v-if="!agentSkillsLoading && agentSkills.length === 0" class="mcp-empty">
                暂未导入用户 Skill。
              </div>
              <div v-for="skill in agentSkills" :key="skill.id" class="skill-card" :class="{ disabled: !skill.enabled, invalid: skill.error }">
                <button class="skill-row" type="button" @click="toggleAgentSkill(skill)">
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
                <button class="text-danger-button" type="button" @click.stop="deleteAgentSkill(skill)">删除</button>
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
                <input v-model="agentMCPEnabledDraft" type="checkbox" />
                <span></span>
              </label>
            </div>
            <div class="actions-row">
              <button class="secondary-button" type="button" @click="openAgentMCPJSONDialog">
                自定义 MCP JSON
              </button>
              <button class="secondary-button" type="button" :disabled="agentRefreshing" @click="refreshAgentAdvancedMCP">
                {{ agentRefreshing ? '扫描中...' : '扫描本机 MCP' }}
              </button>
            </div>
            <div class="mcp-server-list">
              <div v-if="agentMCPServersDraft.length === 0" class="mcp-empty">
                未扫描到 MCP 配置。当前会检查 Cursor、Claude、Codex、QoderCN、Lingma、Antigravity、Windsurf、VS Code、Zed、Continue 和项目级 .mcp.json 等主流路径。
              </div>
              <div v-for="group in agentMCPServerGroups" :key="group.source" class="mcp-source-group">
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
                    @click="toggleAgentMCPServer(server.name)"
                  >
                    <span class="mcp-server-toggle" :class="{ on: server.enabled }"></span>
                    <span class="mcp-server-main">
                      <strong>{{ server.name }}</strong>
                      <small>{{ server.command }} {{ Array.isArray(server.args) ? server.args.join(' ') : '' }}</small>
                      <em>{{ server.source || '自定义配置' }}</em>
                    </span>
                    <span class="mcp-server-meta">
                      {{ agentMCPServerMeta(server) }}
                    </span>
                  </button>
                  <button
                    v-if="Array.isArray(server.tools) && server.tools.length > 0"
                    type="button"
                    class="mcp-tools-toggle"
                    @click.stop="toggleAgentMCPTools(server.name)"
                  >
                    <span>{{ agentMCPExpanded[server.name] ? '⌄' : '›' }}</span>
                    <span>{{ server.tools.length }} 个转换后的 tools</span>
                  </button>
                  <div v-if="agentMCPExpanded[server.name] && Array.isArray(server.tools) && server.tools.length > 0" class="mcp-tool-list">
                    <div v-for="tool in server.tools" :key="agentMCPToolLabel(tool)" class="mcp-tool-chip">
                      <code>{{ agentMCPToolLabel(tool) }}</code>
                      <small v-if="agentMCPToolDescription(tool)">{{ agentMCPToolDescription(tool) }}</small>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
        <div class="modal-footer">
          <button class="secondary-button" type="button" @click="clearAgentIdentity">清空</button>
          <div class="actions-row">
            <button class="secondary-button" type="button" @click="agentAdvancedDialogOpen = false">取消</button>
            <button class="primary-button" type="button" :disabled="agentSaving" @click="saveAgentAdvancedSettings">
              {{ agentSaving ? '保存中...' : '保存生效' }}
            </button>
          </div>
        </div>
      </section>
    </div>

    <div v-if="agentMCPJSONDialogOpen" class="modal-backdrop" @click.self="agentMCPJSONDialogOpen = false">
      <section class="modal-card agent-mcp-json-modal">
        <div class="modal-header">
          <div>
            <h2>自定义 MCP JSON</h2>
            <p>保存到应用自己的 MCP 配置文件，保存成功后会自动解析并刷新 MCP 列表。</p>
          </div>
          <button class="secondary-button" type="button" @click="agentMCPJSONDialogOpen = false">关闭</button>
        </div>
        <div class="modal-body">
          <div class="field">
            <label>配置文件</label>
            <input :value="agentMCPJSONPath" readonly />
          </div>
          <div class="field mcp-json-edit-pane">
            <label>JSON 文件内容</label>
            <textarea
              v-model="agentMCPJSONDraft"
              class="mcp-json-editor"
              spellcheck="false"
              placeholder='{"mcpServers":{"context7":{"command":"npx","args":["-y","@upstash/context7-mcp@latest"]}}}'
            ></textarea>
          </div>
          <p v-if="agentMCPJSONError" class="mcp-json-error">{{ agentMCPJSONError }}</p>
        </div>
        <div class="modal-footer">
          <button class="secondary-button" type="button" @click="agentMCPJSONDialogOpen = false">取消</button>
          <button class="primary-button" type="button" :disabled="agentMCPJSONSaving" @click="saveAgentMCPJSON">
            {{ agentMCPJSONSaving ? '保存中...' : '保存并解析' }}
          </button>
        </div>
      </section>
    </div>
  </div>
</template>
