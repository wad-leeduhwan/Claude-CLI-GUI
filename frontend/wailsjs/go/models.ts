export namespace claude {
	
	export class SessionInfo {
	    sessionId: string;
	    timestamp: string;
	    lastActivity: string;
	    preview: string;
	    messageCount: number;
	    projectPath: string;
	
	    static createFrom(source: any = {}) {
	        return new SessionInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.timestamp = source["timestamp"];
	        this.lastActivity = source["lastActivity"];
	        this.preview = source["preview"];
	        this.messageCount = source["messageCount"];
	        this.projectPath = source["projectPath"];
	    }
	}

}

export namespace main {
	
	export class AutoContextFile {
	    path: string;
	    name: string;
	    scope: string;
	    exists: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AutoContextFile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.name = source["name"];
	        this.scope = source["scope"];
	        this.exists = source["exists"];
	    }
	}
	export class ReleaseNote {
	    version: string;
	    publishedAt: string;
	    body: string;
	
	    static createFrom(source: any = {}) {
	        return new ReleaseNote(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.publishedAt = source["publishedAt"];
	        this.body = source["body"];
	    }
	}
	export class UpdateInfo {
	    currentVersion: string;
	    latestVersion: string;
	    updateAvailable: boolean;
	
	    static createFrom(source: any = {}) {
	        return new UpdateInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.currentVersion = source["currentVersion"];
	        this.latestVersion = source["latestVersion"];
	        this.updateAvailable = source["updateAvailable"];
	    }
	}
	export class UpdateResult {
	    success: boolean;
	    output: string;
	    error: string;
	    newVersion: string;
	    installMethod: string;
	
	    static createFrom(source: any = {}) {
	        return new UpdateResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.output = source["output"];
	        this.error = source["error"];
	        this.newVersion = source["newVersion"];
	        this.installMethod = source["installMethod"];
	    }
	}

}

export namespace models {
	
	export class TeamPreset {
	    id: string;
	    name: string;
	    workerNames: string[];
	    createdAt: number;
	
	    static createFrom(source: any = {}) {
	        return new TeamPreset(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.workerNames = source["workerNames"];
	        this.createdAt = source["createdAt"];
	    }
	}
	export class GlobalSettings {
	    planModeDefault: boolean;
	    adminMode: boolean;
	    tabSettings: Record<string, boolean>;
	    agentModel: string;
	    teamPresets: TeamPreset[];
	
	    static createFrom(source: any = {}) {
	        return new GlobalSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.planModeDefault = source["planModeDefault"];
	        this.adminMode = source["adminMode"];
	        this.tabSettings = source["tabSettings"];
	        this.agentModel = source["agentModel"];
	        this.teamPresets = this.convertValues(source["teamPresets"], TeamPreset);
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
	export class ToolUse {
	    toolName: string;
	    detail: string;
	
	    static createFrom(source: any = {}) {
	        return new ToolUse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.toolName = source["toolName"];
	        this.detail = source["detail"];
	    }
	}
	export class Message {
	    role: string;
	    content: string;
	    attachments: string[];
	    timestamp: number;
	    durationMs?: number;
	    inputTokens?: number;
	    outputTokens?: number;
	    toolUses?: ToolUse[];
	    metadata?: Record<string, string>;
	
	    static createFrom(source: any = {}) {
	        return new Message(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.content = source["content"];
	        this.attachments = source["attachments"];
	        this.timestamp = source["timestamp"];
	        this.durationMs = source["durationMs"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.toolUses = this.convertValues(source["toolUses"], ToolUse);
	        this.metadata = source["metadata"];
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
	export class WorkerTask {
	    id: string;
	    workerTabId: string;
	    adminTabId: string;
	    description: string;
	    prompt: string;
	    status: string;
	    result: string;
	    error?: string;
	    // Go type: time
	    startedAt?: any;
	    // Go type: time
	    completedAt?: any;
	    durationMs?: number;
	
	    static createFrom(source: any = {}) {
	        return new WorkerTask(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.workerTabId = source["workerTabId"];
	        this.adminTabId = source["adminTabId"];
	        this.description = source["description"];
	        this.prompt = source["prompt"];
	        this.status = source["status"];
	        this.result = source["result"];
	        this.error = source["error"];
	        this.startedAt = this.convertValues(source["startedAt"], null);
	        this.completedAt = this.convertValues(source["completedAt"], null);
	        this.durationMs = source["durationMs"];
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
	export class OrchestrationJob {
	    id: string;
	    adminTabId: string;
	    userRequest: string;
	    tasks: WorkerTask[];
	    status: string;
	    synthesisResult?: string;
	
	    static createFrom(source: any = {}) {
	        return new OrchestrationJob(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.adminTabId = source["adminTabId"];
	        this.userRequest = source["userRequest"];
	        this.tasks = this.convertValues(source["tasks"], WorkerTask);
	        this.status = source["status"];
	        this.synthesisResult = source["synthesisResult"];
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
	export class OrchestratorState {
	    connectedTabs: string[];
	    currentJob?: OrchestrationJob;
	    jobHistory: OrchestrationJob[];
	
	    static createFrom(source: any = {}) {
	        return new OrchestratorState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.connectedTabs = source["connectedTabs"];
	        this.currentJob = this.convertValues(source["currentJob"], OrchestrationJob);
	        this.jobHistory = this.convertValues(source["jobHistory"], OrchestrationJob);
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
	export class TeamsState {
	    connectedTabs: string[];
	    agentMapping?: Record<string, string>;
	    isRunning: boolean;
	
	    static createFrom(source: any = {}) {
	        return new TeamsState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.connectedTabs = source["connectedTabs"];
	        this.agentMapping = source["agentMapping"];
	        this.isRunning = source["isRunning"];
	    }
	}
	export class TabState {
	    id: string;
	    name: string;
	    messages: Message[];
	    conversationId: string;
	    isActive: boolean;
	    adminMode: boolean;
	    planMode: boolean;
	    teamsMode: boolean;
	    orchestrator?: OrchestratorState;
	    teamsState?: TeamsState;
	    workDir: string;
	    contextFiles: string[];
	
	    static createFrom(source: any = {}) {
	        return new TabState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.messages = this.convertValues(source["messages"], Message);
	        this.conversationId = source["conversationId"];
	        this.isActive = source["isActive"];
	        this.adminMode = source["adminMode"];
	        this.planMode = source["planMode"];
	        this.teamsMode = source["teamsMode"];
	        this.orchestrator = this.convertValues(source["orchestrator"], OrchestratorState);
	        this.teamsState = this.convertValues(source["teamsState"], TeamsState);
	        this.workDir = source["workDir"];
	        this.contextFiles = source["contextFiles"];
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
	
	
	
	export class UsageInfo {
	    inputTokens: number;
	    outputTokens: number;
	    totalTokens: number;
	    messageCount: number;
	
	    static createFrom(source: any = {}) {
	        return new UsageInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.totalTokens = source["totalTokens"];
	        this.messageCount = source["messageCount"];
	    }
	}

}

export namespace utils {
	
	export class FileInfo {
	    name: string;
	    path: string;
	    size: number;
	    mimeType: string;
	    data: string;
	
	    static createFrom(source: any = {}) {
	        return new FileInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.size = source["size"];
	        this.mimeType = source["mimeType"];
	        this.data = source["data"];
	    }
	}

}

