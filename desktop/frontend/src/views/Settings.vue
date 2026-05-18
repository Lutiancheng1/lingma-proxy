<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { BrowserOpenURL } from '../../wailsjs/runtime'
import {
  BindFeishuCLIWithAppSecret,
  GetConfig,
  GetDetectionInfo,
  GetFeishuBridgeConfig,
  GetFeishuBridgeStatus,
  InstallFeishuCLI,
  StartFeishuBridge,
  StartFeishuCLILogin,
  StartFeishuCLISetupNew,
  StopFeishuBridge,
  UpdateConfig,
  UpdateFeishuBridgeConfig,
} from '../../wailsjs/go/main/App.js'
import { safeEventsOff, safeEventsOn } from '../utils/wailsSafe'

const emit = defineEmits(['log', 'status-refresh'])

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
  appId: '',
})
const bridgeStatus = ref(null)
const bridgeSaving = ref(false)
const bridgeBusy = ref(false)
const manualBindExpanded = ref(false)
const manualAppID = ref('')
const manualAppSecret = ref('')
const openSelect = ref('')
const fallbackModelsText = ref('')
const isIPCBackend = computed(() => (config.value.Backend || 'ipc') === 'ipc')
const formattedTokenExpireAt = computed(() => formatDateTime(detection.value?.remoteTokenExpireAt))
const bridgeCurrentModel = computed(() => bridgeConfig.value.model || bridgeStatus.value?.currentModel || 'kmodel')
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
    manualAppID.value = bridgeConfig.value.appId || ''
    fallbackModelsText.value = Array.isArray(config.value.RemoteFallbackModels)
      ? config.value.RemoteFallbackModels.join('\n')
      : ''
    await refreshDetection()
    await refreshBridgeStatus()
  } catch (e) {
    emit('log', 'error', '配置加载失败：' + (e.message || String(e)))
  }

  safeEventsOn('feishu:status', (nextStatus) => {
    bridgeStatus.value = nextStatus
  })
})

onUnmounted(() => {
  safeEventsOff('feishu:status')
})

async function refreshDetection() {
  try {
    detection.value = await GetDetectionInfo()
  } catch (e) {
    emit('log', 'warn', '探测信息加载失败：' + (e.message || String(e)))
  }
}

async function refreshBridgeStatus() {
  try {
    bridgeStatus.value = await GetFeishuBridgeStatus()
  } catch (e) {
    emit('log', 'warn', 'Feishu Bridge 状态加载失败：' + (e.message || String(e)))
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
      appId: manualAppID.value.trim(),
      maxToolRounds: Number(bridgeConfig.value.maxToolRounds) || 5,
    }
    await UpdateFeishuBridgeConfig(bridgeConfig.value)
    emit('log', 'info', 'Feishu Bridge 配置已保存')
    await refreshBridgeStatus()
  } catch (e) {
    emit('log', 'error', 'Feishu Bridge 配置保存失败：' + (e.message || String(e)))
  } finally {
    bridgeSaving.value = false
  }
}

async function withBridgeAction(message, action) {
  bridgeBusy.value = true
  try {
    await action()
    emit('log', 'info', message)
    await refreshBridgeStatus()
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
  manualBindExpanded.value = false
  await withBridgeAction('飞书 CLI 首次初始化已启动', () => StartFeishuCLISetupNew())
}

async function bindExistingApp() {
  await withBridgeAction('飞书 CLI 已开始绑定现有应用', async () => {
    await BindFeishuCLIWithAppSecret(manualAppID.value.trim(), manualAppSecret.value)
    manualAppSecret.value = ''
    bridgeConfig.value.appId = manualAppID.value.trim()
    bridgeConfig.value.setupMode = 'manual'
    await UpdateFeishuBridgeConfig(bridgeConfig.value)
  })
}

async function startBridgeLogin() {
  await withBridgeAction('飞书 CLI 授权流程已启动', () => StartFeishuCLILogin())
}

async function enableBridgeAndStart() {
  await saveBridgeConfig()
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
    </section>

    <section class="grid-2 settings-grid">
      <div class="glass-panel">
        <div class="panel-header">
          <div>
            <h2>Feishu Bot Bridge</h2>
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
            <input v-model="bridgeConfig.model" type="text" :placeholder="bridgeCurrentModel" />
          </div>
          <div class="field">
            <label>最大 Tool 轮次</label>
            <input v-model.number="bridgeConfig.maxToolRounds" type="number" min="1" max="8" />
          </div>
        </div>

        <div class="hint-box compact-hint">
          <strong>推荐接入路径</strong>
          <span>先安装飞书 CLI，再走“首次初始化（推荐）”和“登录授权”。只有前置条件全部满足后，Bridge 才允许启动。</span>
        </div>

        <div v-if="bridgeStatus" class="detect-card">
          <div class="detect-title">
            <strong>当前状态</strong>
            <button type="button" @click="refreshBridgeStatus">刷新</button>
          </div>
          <dl>
            <div>
              <dt>平台</dt>
              <dd>{{ bridgeStatus.platform }} · {{ bridgeStatus.arch }}</dd>
            </div>
            <div>
              <dt>Node / npm / npx</dt>
              <dd :class="{ 'warn-text': !bridgeStatus.node?.found || !bridgeStatus.npm?.found || !bridgeStatus.npx?.found }">
                {{ bridgeStatus.node?.version || '缺失' }} / {{ bridgeStatus.npm?.version || '缺失' }} / {{ bridgeStatus.npx?.version || '缺失' }}
              </dd>
            </div>
            <div>
              <dt>lark-cli</dt>
              <dd :class="{ 'warn-text': !bridgeStatus.cli?.found }">
                {{ bridgeStatus.cli?.version || '未安装' }}
              </dd>
            </div>
            <div>
              <dt>Skills</dt>
              <dd :class="{ 'warn-text': !bridgeStatus.skillsReady }">
                {{ bridgeStatus.skillsReady ? '已就绪' : '缺失或未完整安装' }}
              </dd>
            </div>
            <div>
              <dt>CLI 配置</dt>
              <dd :class="{ 'warn-text': !bridgeStatus.config?.configured }">
                {{ bridgeStatus.config?.configured ? `${bridgeStatus.config?.appId || '已配置'} · ${bridgeStatus.config?.brand || 'feishu'}` : (bridgeStatus.config?.message || '未初始化') }}
              </dd>
            </div>
            <div>
              <dt>授权状态</dt>
              <dd :class="{ 'warn-text': !bridgeStatus.auth?.authorized }">
                {{ bridgeStatus.auth?.authorized ? `${bridgeStatus.auth?.userName || '已授权'} · ${bridgeStatus.auth?.tokenStatus || 'ok'}` : (bridgeStatus.auth?.message || '未授权') }}
              </dd>
            </div>
            <div>
              <dt>运行状态</dt>
              <dd>{{ bridgeStatus.running ? `运行中 · ${bridgeStatus.lastStartedAt || ''}` : '未运行' }}</dd>
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

        <div v-if="bridgeStatus?.skills?.length" class="hint-box compact-hint">
          <strong>必需 Skills</strong>
          <span>{{ bridgeStatus.skills.map((item) => `${item.name}${item.found ? '' : '（缺失）'}`).join('、') }}</span>
        </div>

        <div class="actions-row">
          <button class="primary-button" type="button" :disabled="bridgeBusy" @click="saveBridgeConfig">
            {{ bridgeSaving ? '保存中...' : '保存 Bridge 配置' }}
          </button>
          <button type="button" :disabled="bridgeBusy" @click="installBridgeCLI">安装飞书 CLI</button>
          <button type="button" :disabled="bridgeBusy" @click="startBridgeSetupNew">首次初始化（推荐）</button>
          <button type="button" :disabled="bridgeBusy" @click="startBridgeLogin">登录授权</button>
        </div>

        <div class="actions-row">
          <button class="primary-button" type="button" :disabled="bridgeBusy || !bridgeReady" @click="enableBridgeAndStart">
            启用并启动 Bridge
          </button>
          <button type="button" :disabled="bridgeBusy || !bridgeStatus?.running" @click="stopBridge">停止 Bridge</button>
          <button type="button" :disabled="bridgeBusy" @click="manualBindExpanded = !manualBindExpanded">
            {{ manualBindExpanded ? '收起已有 App 配置' : '我已有 App ID / Secret' }}
          </button>
        </div>

        <div v-if="bridgeStatus?.setupUrl || bridgeStatus?.loginUrl" class="hint-box">
          <strong>浏览器继续</strong>
          <span>首次初始化和登录授权需要在浏览器中完成，点击下方链接继续。</span>
          <div class="actions-row">
            <button v-if="bridgeStatus?.setupUrl" type="button" @click="openBridgeURL(bridgeStatus.setupUrl)">打开初始化链接</button>
            <button v-if="bridgeStatus?.loginUrl" type="button" @click="openBridgeURL(bridgeStatus.loginUrl)">打开授权链接</button>
          </div>
        </div>

        <div v-if="manualBindExpanded" class="hint-box">
          <strong>已有应用（高级）</strong>
          <span>仅当你已经拥有 App ID / App Secret 时使用。保存后会调用 `lark-cli config init --app-id ... --app-secret-stdin --brand feishu`。</span>
          <div class="form-grid compact-form-grid">
            <div class="field span-2">
              <label>App ID</label>
              <input v-model="manualAppID" type="text" placeholder="cli_xxx" />
            </div>
            <div class="field span-2">
              <label>App Secret</label>
              <input v-model="manualAppSecret" type="password" placeholder="输入后只写入系统钥匙串，不落盘" />
            </div>
          </div>
          <div class="actions-row">
            <button class="primary-button" type="button" :disabled="bridgeBusy || !manualAppID || !manualAppSecret" @click="bindExistingApp">
              绑定现有应用
            </button>
          </div>
        </div>
      </div>
    </section>
  </div>
</template>
