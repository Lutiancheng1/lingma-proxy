export namespace feishu {

	export class AuthStatus {
	    authorized: boolean;
	    verified: boolean;
	    identity?: string;
	    userName?: string;
	    userOpenId?: string;
	    expiresAt?: string;
	    refreshExpiresAt?: string;
	    tokenStatus?: string;
	    message?: string;

	    static createFrom(source: any = {}) {
	        return new AuthStatus(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.authorized = source["authorized"];
	        this.verified = source["verified"];
	        this.identity = source["identity"];
	        this.userName = source["userName"];
	        this.userOpenId = source["userOpenId"];
	        this.expiresAt = source["expiresAt"];
	        this.refreshExpiresAt = source["refreshExpiresAt"];
	        this.tokenStatus = source["tokenStatus"];
	        this.message = source["message"];
	    }
	}
	export class BinaryStatus {
	    found: boolean;
	    version?: string;
	    path?: string;
	    hint?: string;

	    static createFrom(source: any = {}) {
	        return new BinaryStatus(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.found = source["found"];
	        this.version = source["version"];
	        this.path = source["path"];
	        this.hint = source["hint"];
	    }
	}
	export class MCPServerConfig {
	    name: string;
	    source?: string;
	    sourceClient?: string;
	    command: string;
	    args?: string[];
	    env?: Record<string, string>;
	    enabled: boolean;

	    static createFrom(source: any = {}) {
	        return new MCPServerConfig(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.source = source["source"];
	        this.sourceClient = source["sourceClient"];
	        this.command = source["command"];
	        this.args = source["args"];
	        this.env = source["env"];
	        this.enabled = source["enabled"];
	    }
	}
	export class Config {
	    enabled: boolean;
	    autoStart: boolean;
	    brand: string;
	    model: string;
	    botName: string;
	    botIdentity: string;
	    mcpEnabled: boolean;
	    mcpServers?: MCPServerConfig[];
	    maxToolRounds: number;
	    groupOnlyAtBot: boolean;

	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.autoStart = source["autoStart"];
	        this.brand = source["brand"];
	        this.model = source["model"];
	        this.botName = source["botName"];
	        this.botIdentity = source["botIdentity"];
	        this.mcpEnabled = source["mcpEnabled"];
	        this.mcpServers = this.convertValues(source["mcpServers"], MCPServerConfig);
	        this.maxToolRounds = source["maxToolRounds"];
	        this.groupOnlyAtBot = source["groupOnlyAtBot"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ConfigStatus {
	    configured: boolean;
	    appId?: string;
	    brand?: string;
	    path?: string;
	    message?: string;

	    static createFrom(source: any = {}) {
	        return new ConfigStatus(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.configured = source["configured"];
	        this.appId = source["appId"];
	        this.brand = source["brand"];
	        this.path = source["path"];
	        this.message = source["message"];
	    }
	}

	export class MCPToolStatus {
	    name: string;
	    function?: string;
	    description?: string;

	    static createFrom(source: any = {}) {
	        return new MCPToolStatus(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.function = source["function"];
	        this.description = source["description"];
	    }
	}
	export class MCPServerStatus {
	    name: string;
	    source?: string;
	    sourceClient?: string;
	    command?: string;
	    args?: string[];
	    enabled: boolean;
	    available: boolean;
	    toolCount: number;
	    tools?: MCPToolStatus[];
	    message?: string;

	    static createFrom(source: any = {}) {
	        return new MCPServerStatus(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.source = source["source"];
	        this.sourceClient = source["sourceClient"];
	        this.command = source["command"];
	        this.args = source["args"];
	        this.enabled = source["enabled"];
	        this.available = source["available"];
	        this.toolCount = source["toolCount"];
	        this.tools = this.convertValues(source["tools"], MCPToolStatus);
	        this.message = source["message"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

	export class SkillStatus {
	    name: string;
	    found: boolean;
	    path?: string;
	    message?: string;

	    static createFrom(source: any = {}) {
	        return new SkillStatus(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.found = source["found"];
	        this.path = source["path"];
	        this.message = source["message"];
	    }
	}
	export class Status {
	    platform: string;
	    arch: string;
	    node: BinaryStatus;
	    npm: BinaryStatus;
	    npx: BinaryStatus;
	    cli: BinaryStatus;
	    skills: SkillStatus[];
	    skillsReady: boolean;
	    mcpServers?: MCPServerStatus[];
	    config: ConfigStatus;
	    auth: AuthStatus;
	    running: boolean;
	    setupRunning: boolean;
	    loginRunning: boolean;
	    installRunning: boolean;
	    setupUrl?: string;
	    loginUrl?: string;
	    lastOutput?: string;
	    lastError?: string;
	    lastCheckedAt?: string;
	    lastStartedAt?: string;
	    currentModel?: string;
	    requiredSkills?: string[];

	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.platform = source["platform"];
	        this.arch = source["arch"];
	        this.node = this.convertValues(source["node"], BinaryStatus);
	        this.npm = this.convertValues(source["npm"], BinaryStatus);
	        this.npx = this.convertValues(source["npx"], BinaryStatus);
	        this.cli = this.convertValues(source["cli"], BinaryStatus);
	        this.skills = this.convertValues(source["skills"], SkillStatus);
	        this.skillsReady = source["skillsReady"];
	        this.mcpServers = this.convertValues(source["mcpServers"], MCPServerStatus);
	        this.config = this.convertValues(source["config"], ConfigStatus);
	        this.auth = this.convertValues(source["auth"], AuthStatus);
	        this.running = source["running"];
	        this.setupRunning = source["setupRunning"];
	        this.loginRunning = source["loginRunning"];
	        this.installRunning = source["installRunning"];
	        this.setupUrl = source["setupUrl"];
	        this.loginUrl = source["loginUrl"];
	        this.lastOutput = source["lastOutput"];
	        this.lastError = source["lastError"];
	        this.lastCheckedAt = source["lastCheckedAt"];
	        this.lastStartedAt = source["lastStartedAt"];
	        this.currentModel = source["currentModel"];
	        this.requiredSkills = source["requiredSkills"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace main {

	export class AppLog {
	    createdAt?: string;
	    time: string;
	    source?: string;
	    sessionId?: string;
	    chatId?: string;
	    messageId?: string;
	    level: string;
	    message: string;

	    static createFrom(source: any = {}) {
	        return new AppLog(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.createdAt = source["createdAt"];
	        this.time = source["time"];
	        this.source = source["source"];
	        this.sessionId = source["sessionId"];
	        this.chatId = source["chatId"];
	        this.messageId = source["messageId"];
	        this.level = source["level"];
	        this.message = source["message"];
	    }
	}
	export class DetectionInfo {
	    listenUrl: string;
	    backend: string;
	    backendLabel: string;
	    ipcSuccess: boolean;
	    ipcTransport?: string;
	    ipcEndpoint?: string;
	    ipcError?: string;
	    remoteBaseUrl: string;
	    remoteBaseUrlSource?: string;
	    remoteProxyUrl?: string;
	    remoteProxySource?: string;
	    remoteProxyError?: string;
	    remoteCredentialSuccess: boolean;
	    remoteCredentialSource?: string;
	    remoteUserId?: string;
	    remoteMachineId?: string;
	    remoteTokenExpireAt?: string;
	    remoteTokenExpired: boolean;
	    remoteCredentialError?: string;

	    static createFrom(source: any = {}) {
	        return new DetectionInfo(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.listenUrl = source["listenUrl"];
	        this.backend = source["backend"];
	        this.backendLabel = source["backendLabel"];
	        this.ipcSuccess = source["ipcSuccess"];
	        this.ipcTransport = source["ipcTransport"];
	        this.ipcEndpoint = source["ipcEndpoint"];
	        this.ipcError = source["ipcError"];
	        this.remoteBaseUrl = source["remoteBaseUrl"];
	        this.remoteBaseUrlSource = source["remoteBaseUrlSource"];
	        this.remoteProxyUrl = source["remoteProxyUrl"];
	        this.remoteProxySource = source["remoteProxySource"];
	        this.remoteProxyError = source["remoteProxyError"];
	        this.remoteCredentialSuccess = source["remoteCredentialSuccess"];
	        this.remoteCredentialSource = source["remoteCredentialSource"];
	        this.remoteUserId = source["remoteUserId"];
	        this.remoteMachineId = source["remoteMachineId"];
	        this.remoteTokenExpireAt = source["remoteTokenExpireAt"];
	        this.remoteTokenExpired = source["remoteTokenExpired"];
	        this.remoteCredentialError = source["remoteCredentialError"];
	    }
	}
	export class FeedbackExportOptions {
	    rangePreset: string;
	    startAt?: string;
	    endAt?: string;
	    includeAppLogs: boolean;
	    includeRequests: boolean;
	    includeConfigSummary: boolean;
	    includeEnvironment: boolean;
	    includeDetectionInfo: boolean;
	    issueDescription?: string;
	    savePath?: string;

	    static createFrom(source: any = {}) {
	        return new FeedbackExportOptions(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rangePreset = source["rangePreset"];
	        this.startAt = source["startAt"];
	        this.endAt = source["endAt"];
	        this.includeAppLogs = source["includeAppLogs"];
	        this.includeRequests = source["includeRequests"];
	        this.includeConfigSummary = source["includeConfigSummary"];
	        this.includeEnvironment = source["includeEnvironment"];
	        this.includeDetectionInfo = source["includeDetectionInfo"];
	        this.issueDescription = source["issueDescription"];
	        this.savePath = source["savePath"];
	    }
	}
	export class FeedbackExportResult {
	    zipPath: string;
	    zipFilename: string;
	    saveDir: string;
	    shareText: string;
	    exportedAt: string;
	    appLogCount: number;
	    requestCount: number;

	    static createFrom(source: any = {}) {
	        return new FeedbackExportResult(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.zipPath = source["zipPath"];
	        this.zipFilename = source["zipFilename"];
	        this.saveDir = source["saveDir"];
	        this.shareText = source["shareText"];
	        this.exportedAt = source["exportedAt"];
	        this.appLogCount = source["appLogCount"];
	        this.requestCount = source["requestCount"];
	    }
	}
	export class MCPJSONFile {
	    path: string;
	    content: string;
	    serverCount: number;

	    static createFrom(source: any = {}) {
	        return new MCPJSONFile(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.content = source["content"];
	        this.serverCount = source["serverCount"];
	    }
	}
	export class ModelInfo {
	    id: string;
	    name: string;

	    static createFrom(source: any = {}) {
	        return new ModelInfo(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	    }
	}
	export class ProxyStatus {
	    running: boolean;
	    addr: string;
	    backend: string;
	    models: number;
	    model?: string;
	    startedAt?: string;

	    static createFrom(source: any = {}) {
	        return new ProxyStatus(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	        this.addr = source["addr"];
	        this.backend = source["backend"];
	        this.models = source["models"];
	        this.model = source["model"];
	        this.startedAt = source["startedAt"];
	    }
	}
	export class RequestRecord {
	    id?: string;
	    createdAt?: string;
	    time: string;
	    method: string;
	    path: string;
	    model?: string;
	    statusCode: number;
	    duration: string;
	    size?: string;
	    hasReqBody?: boolean;
	    hasRespBody?: boolean;
	    inputTokens?: number;
	    outputTokens?: number;
	    totalTokens?: number;
	    reqBody?: string;
	    respBody?: string;

	    static createFrom(source: any = {}) {
	        return new RequestRecord(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.createdAt = source["createdAt"];
	        this.time = source["time"];
	        this.method = source["method"];
	        this.path = source["path"];
	        this.model = source["model"];
	        this.statusCode = source["statusCode"];
	        this.duration = source["duration"];
	        this.size = source["size"];
	        this.hasReqBody = source["hasReqBody"];
	        this.hasRespBody = source["hasRespBody"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.totalTokens = source["totalTokens"];
	        this.reqBody = source["reqBody"];
	        this.respBody = source["respBody"];
	    }
	}
	export class TokenStats {
	    totalRequests: number;
	    successRequests: number;
	    inputTokens: number;
	    outputTokens: number;
	    totalTokens: number;
	    byModel?: Record<string, number>;
	    lastModel?: string;
	    lastUpdated?: string;

	    static createFrom(source: any = {}) {
	        return new TokenStats(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.totalRequests = source["totalRequests"];
	        this.successRequests = source["successRequests"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.totalTokens = source["totalTokens"];
	        this.byModel = source["byModel"];
	        this.lastModel = source["lastModel"];
	        this.lastUpdated = source["lastUpdated"];
	    }
	}

}

export namespace service {

	export class Config {
	    Host: string;
	    Port: number;
	    Backend: string;
	    Transport: string;
	    Pipe: string;
	    WebSocketURL: string;
	    RemoteBaseURL: string;
	    RemoteAuthFile: string;
	    RemoteProxyURL: string;
	    RemoteVersion: string;
	    Cwd: string;
	    CurrentFilePath: string;
	    Mode: string;
	    Model: string;
	    ShellType: string;
	    SessionMode: string;
	    Timeout: number;
	    WarmupTimeout: number;
	    RemoteFallbackEnabled: boolean;
	    RemoteFallbackModels: string[];

	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Host = source["Host"];
	        this.Port = source["Port"];
	        this.Backend = source["Backend"];
	        this.Transport = source["Transport"];
	        this.Pipe = source["Pipe"];
	        this.WebSocketURL = source["WebSocketURL"];
	        this.RemoteBaseURL = source["RemoteBaseURL"];
	        this.RemoteAuthFile = source["RemoteAuthFile"];
	        this.RemoteProxyURL = source["RemoteProxyURL"];
	        this.RemoteVersion = source["RemoteVersion"];
	        this.Cwd = source["Cwd"];
	        this.CurrentFilePath = source["CurrentFilePath"];
	        this.Mode = source["Mode"];
	        this.Model = source["Model"];
	        this.ShellType = source["ShellType"];
	        this.SessionMode = source["SessionMode"];
	        this.Timeout = source["Timeout"];
	        this.WarmupTimeout = source["WarmupTimeout"];
	        this.RemoteFallbackEnabled = source["RemoteFallbackEnabled"];
	        this.RemoteFallbackModels = source["RemoteFallbackModels"];
	    }
	}

}

