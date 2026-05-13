export namespace main {
	
	export class AppLog {
	    createdAt?: string;
	    time: string;
	    level: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new AppLog(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.createdAt = source["createdAt"];
	        this.time = source["time"];
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
	    createdAt?: string;
	    time: string;
	    method: string;
	    path: string;
	    model?: string;
	    statusCode: number;
	    duration: string;
	    size?: string;
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
	        this.createdAt = source["createdAt"];
	        this.time = source["time"];
	        this.method = source["method"];
	        this.path = source["path"];
	        this.model = source["model"];
	        this.statusCode = source["statusCode"];
	        this.duration = source["duration"];
	        this.size = source["size"];
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

