export namespace agent {
	
	export class Preset {
	    id: string;
	    name: string;
	    description: string;
	    model: string;
	    systemPrompt: string;
	    tools: string[];
	
	    static createFrom(source: any = {}) {
	        return new Preset(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.model = source["model"];
	        this.systemPrompt = source["systemPrompt"];
	        this.tools = source["tools"];
	    }
	}

}

export namespace apikey {
	
	export class Key {
	    id: string;
	    name: string;
	    key: string;
	    enabled: boolean;
	    createdAt: string;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new Key(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.key = source["key"];
	        this.enabled = source["enabled"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	    }
	}

}

export namespace config {
	
	export class ModelMapping {
	    id: string;
	    clientModel: string;
	    providerId: string;
	    upstreamModel: string;
	    aliases: string[];
	    enabled: boolean;
	    inputTypes: string[];
	    inputContextSize: number;
	    outputSize: number;
	    inputPrice: number;
	    outputPrice: number;
	    cacheReadPrice: number;
	
	    static createFrom(source: any = {}) {
	        return new ModelMapping(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.clientModel = source["clientModel"];
	        this.providerId = source["providerId"];
	        this.upstreamModel = source["upstreamModel"];
	        this.aliases = source["aliases"];
	        this.enabled = source["enabled"];
	        this.inputTypes = source["inputTypes"];
	        this.inputContextSize = source["inputContextSize"];
	        this.outputSize = source["outputSize"];
	        this.inputPrice = source["inputPrice"];
	        this.outputPrice = source["outputPrice"];
	        this.cacheReadPrice = source["cacheReadPrice"];
	    }
	}

}

export namespace credential {
	
	export class Credential {
	    id: string;
	    providerId: string;
	    name: string;
	    mask: string;
	    enabled: boolean;
	    weight: number;
	    status: string;
	    lastError: string;
	    createdAt: string;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new Credential(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.providerId = source["providerId"];
	        this.name = source["name"];
	        this.mask = source["mask"];
	        this.enabled = source["enabled"];
	        this.weight = source["weight"];
	        this.status = source["status"];
	        this.lastError = source["lastError"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	    }
	}

}

export namespace envcfg {
	
	export class Status {
	    varName: string;
	    value: string;
	    set: boolean;
	    matches: boolean;
	    written: boolean;
	    os: string;
	    profilePath: string;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.varName = source["varName"];
	        this.value = source["value"];
	        this.set = source["set"];
	        this.matches = source["matches"];
	        this.written = source["written"];
	        this.os = source["os"];
	        this.profilePath = source["profilePath"];
	        this.error = source["error"];
	    }
	}

}

export namespace main {
	
	export class Bootstrap {
	    providers: provider.Provider[];
	    mappings: config.ModelMapping[];
	    agents: agent.Preset[];
	    usage: usage.Summary;
	    apiKeys: apikey.Key[];
	    proxyRunning: boolean;
	    settings: settings.Settings;
	    chainModes: Record<string, string>;
	    plugins: plugin.Info[];
	
	    static createFrom(source: any = {}) {
	        return new Bootstrap(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.providers = this.convertValues(source["providers"], provider.Provider);
	        this.mappings = this.convertValues(source["mappings"], config.ModelMapping);
	        this.agents = this.convertValues(source["agents"], agent.Preset);
	        this.usage = this.convertValues(source["usage"], usage.Summary);
	        this.apiKeys = this.convertValues(source["apiKeys"], apikey.Key);
	        this.proxyRunning = source["proxyRunning"];
	        this.settings = this.convertValues(source["settings"], settings.Settings);
	        this.chainModes = source["chainModes"];
	        this.plugins = this.convertValues(source["plugins"], plugin.Info);
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
	export class PlaygroundResult {
	    status: number;
	    latencyMs: number;
	
	    static createFrom(source: any = {}) {
	        return new PlaygroundResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.latencyMs = source["latencyMs"];
	    }
	}
	export class SkillSummary {
	    roots: skills.Root[];
	    skills: skills.Skill[];
	    conflicts: string[];
	
	    static createFrom(source: any = {}) {
	        return new SkillSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.roots = this.convertValues(source["roots"], skills.Root);
	        this.skills = this.convertValues(source["skills"], skills.Skill);
	        this.conflicts = source["conflicts"];
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
	export class ToolSkillLinks {
	    targetDir: string;
	    links: skills.SkillLink[];
	
	    static createFrom(source: any = {}) {
	        return new ToolSkillLinks(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.targetDir = source["targetDir"];
	        this.links = this.convertValues(source["links"], skills.SkillLink);
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

export namespace plugin {
	
	export class Info {
	    id: string;
	    name: string;
	    description: string;
	    kind: string;
	    hasConfig: boolean;
	    enabled: boolean;
	    config: Record<string, string>;
	    inputTokens: number;
	    outputTokens: number;
	    savedTokens: number;
	    compressionRate: number;
	    responseTokens: number;
	    responseCount: number;
	    baselineTokens: number;
	    baselineCount: number;
	    responseAvg: number;
	    baselineAvg: number;
	    outputSavingsRate: number;
	
	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.kind = source["kind"];
	        this.hasConfig = source["hasConfig"];
	        this.enabled = source["enabled"];
	        this.config = source["config"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.savedTokens = source["savedTokens"];
	        this.compressionRate = source["compressionRate"];
	        this.responseTokens = source["responseTokens"];
	        this.responseCount = source["responseCount"];
	        this.baselineTokens = source["baselineTokens"];
	        this.baselineCount = source["baselineCount"];
	        this.responseAvg = source["responseAvg"];
	        this.baselineAvg = source["baselineAvg"];
	        this.outputSavingsRate = source["outputSavingsRate"];
	    }
	}

}

export namespace provider {
	
	export class AvailableModel {
	    id: string;
	    created: number;
	
	    static createFrom(source: any = {}) {
	        return new AvailableModel(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.created = source["created"];
	    }
	}
	export class DevModelLimit {
	    context: number;
	    output: number;
	
	    static createFrom(source: any = {}) {
	        return new DevModelLimit(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.context = source["context"];
	        this.output = source["output"];
	    }
	}
	export class DevModelModalities {
	    input: string[];
	    output: string[];
	
	    static createFrom(source: any = {}) {
	        return new DevModelModalities(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.input = source["input"];
	        this.output = source["output"];
	    }
	}
	export class DevModel {
	    id: string;
	    name: string;
	    description: string;
	    reasoning: boolean;
	    tool_call: boolean;
	    structured_output: boolean;
	    release_date: string;
	    modalities: DevModelModalities;
	    limit: DevModelLimit;
	
	    static createFrom(source: any = {}) {
	        return new DevModel(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.reasoning = source["reasoning"];
	        this.tool_call = source["tool_call"];
	        this.structured_output = source["structured_output"];
	        this.release_date = source["release_date"];
	        this.modalities = this.convertValues(source["modalities"], DevModelModalities);
	        this.limit = this.convertValues(source["limit"], DevModelLimit);
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
	
	
	export class DevProvider {
	    id: string;
	    env: string[];
	    npm: string;
	    api: string;
	    name: string;
	    doc: string;
	
	    static createFrom(source: any = {}) {
	        return new DevProvider(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.env = source["env"];
	        this.npm = source["npm"];
	        this.api = source["api"];
	        this.name = source["name"];
	        this.doc = source["doc"];
	    }
	}
	export class Provider {
	    id: string;
	    name: string;
	    kind: string;
	    baseUrl: string;
	    apiKeyRef: string;
	    icon: string;
	    modelPrefix: string;
	    enabled: boolean;
	    models: string[];
	    availableModels: AvailableModel[];
	    credentialMode: string;
	    devId: string;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new Provider(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.baseUrl = source["baseUrl"];
	        this.apiKeyRef = source["apiKeyRef"];
	        this.icon = source["icon"];
	        this.modelPrefix = source["modelPrefix"];
	        this.enabled = source["enabled"];
	        this.models = source["models"];
	        this.availableModels = this.convertValues(source["availableModels"], AvailableModel);
	        this.credentialMode = source["credentialMode"];
	        this.devId = source["devId"];
	        this.updatedAt = source["updatedAt"];
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

export namespace proxy {
	
	export class Message {
	    role: string;
	    content: number[];
	    name?: string;
	    tool_calls?: number[];
	    tool_call_id?: string;
	
	    static createFrom(source: any = {}) {
	        return new Message(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.content = source["content"];
	        this.name = source["name"];
	        this.tool_calls = source["tool_calls"];
	        this.tool_call_id = source["tool_call_id"];
	    }
	}

}

export namespace settings {
	
	export class Settings {
	    host: string;
	    port: number;
	    theme: string;
	    defaultModel: string;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.host = source["host"];
	        this.port = source["port"];
	        this.theme = source["theme"];
	        this.defaultModel = source["defaultModel"];
	    }
	}

}

export namespace skills {
	
	export class Detail {
	    name: string;
	    description: string;
	    dir: string;
	    root: string;
	    source: string;
	    enabled: boolean;
	    files: string[];
	    // Go type: time
	    updatedAt: any;
	    missingFrontmatter: boolean;
	    tokenEstimate: number;
	    body: string;
	
	    static createFrom(source: any = {}) {
	        return new Detail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.dir = source["dir"];
	        this.root = source["root"];
	        this.source = source["source"];
	        this.enabled = source["enabled"];
	        this.files = source["files"];
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	        this.missingFrontmatter = source["missingFrontmatter"];
	        this.tokenEstimate = source["tokenEstimate"];
	        this.body = source["body"];
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
	export class Root {
	    path: string;
	    source: string;
	
	    static createFrom(source: any = {}) {
	        return new Root(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.source = source["source"];
	    }
	}
	export class Skill {
	    name: string;
	    description: string;
	    dir: string;
	    root: string;
	    source: string;
	    enabled: boolean;
	    files: string[];
	    // Go type: time
	    updatedAt: any;
	    missingFrontmatter: boolean;
	    tokenEstimate: number;
	
	    static createFrom(source: any = {}) {
	        return new Skill(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.dir = source["dir"];
	        this.root = source["root"];
	        this.source = source["source"];
	        this.enabled = source["enabled"];
	        this.files = source["files"];
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	        this.missingFrontmatter = source["missingFrontmatter"];
	        this.tokenEstimate = source["tokenEstimate"];
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
	export class SkillLink {
	    name: string;
	    dir: string;
	    target: string;
	    state: string;
	
	    static createFrom(source: any = {}) {
	        return new SkillLink(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.dir = source["dir"];
	        this.target = source["target"];
	        this.state = source["state"];
	    }
	}

}

export namespace templates {
	
	export class Model {
	    id: string;
	    name: string;
	    inputContextSize?: number;
	    outputSize?: number;
	    inputTypes?: string[];
	
	    static createFrom(source: any = {}) {
	        return new Model(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.inputContextSize = source["inputContextSize"];
	        this.outputSize = source["outputSize"];
	        this.inputTypes = source["inputTypes"];
	    }
	}
	export class ModelSlot {
	    key: string;
	    label: string;
	
	    static createFrom(source: any = {}) {
	        return new ModelSlot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.label = source["label"];
	    }
	}
	export class ProfilePreview {
	    name: string;
	    model: string;
	    path: string;
	    exists: boolean;
	    current: string;
	    content: string;
	
	    static createFrom(source: any = {}) {
	        return new ProfilePreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.model = source["model"];
	        this.path = source["path"];
	        this.exists = source["exists"];
	        this.current = source["current"];
	        this.content = source["content"];
	    }
	}
	export class Preview {
	    id: string;
	    name: string;
	    cli: string;
	    installed: boolean;
	    configPath: string;
	    skillsPath: string;
	    exists: boolean;
	    current: string;
	    content: string;
	    multiProvider: boolean;
	    modelSlots: ModelSlot[];
	    routable: Model[];
	    slotModels: Record<string, string>;
	    profiles: ProfilePreview[];
	    selectedModels: string[];
	
	    static createFrom(source: any = {}) {
	        return new Preview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.cli = source["cli"];
	        this.installed = source["installed"];
	        this.configPath = source["configPath"];
	        this.skillsPath = source["skillsPath"];
	        this.exists = source["exists"];
	        this.current = source["current"];
	        this.content = source["content"];
	        this.multiProvider = source["multiProvider"];
	        this.modelSlots = this.convertValues(source["modelSlots"], ModelSlot);
	        this.routable = this.convertValues(source["routable"], Model);
	        this.slotModels = source["slotModels"];
	        this.profiles = this.convertValues(source["profiles"], ProfilePreview);
	        this.selectedModels = source["selectedModels"];
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

export namespace usage {
	
	export class UsageStat {
	    key: string;
	    name?: string;
	    mask?: string;
	    providerId?: string;
	    provider?: string;
	    requests: number;
	    successes: number;
	    inputTokens: number;
	    outputTokens: number;
	    cachedInputTokens: number;
	    reasoningOutputTokens: number;
	    inputCost: number;
	    outputCost: number;
	    cacheCost: number;
	    totalCost: number;
	
	    static createFrom(source: any = {}) {
	        return new UsageStat(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.name = source["name"];
	        this.mask = source["mask"];
	        this.providerId = source["providerId"];
	        this.provider = source["provider"];
	        this.requests = source["requests"];
	        this.successes = source["successes"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.cachedInputTokens = source["cachedInputTokens"];
	        this.reasoningOutputTokens = source["reasoningOutputTokens"];
	        this.inputCost = source["inputCost"];
	        this.outputCost = source["outputCost"];
	        this.cacheCost = source["cacheCost"];
	        this.totalCost = source["totalCost"];
	    }
	}
	export class Breakdown {
	    providers: UsageStat[];
	    models: UsageStat[];
	    keys: UsageStat[];
	    credentials: UsageStat[];
	
	    static createFrom(source: any = {}) {
	        return new Breakdown(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.providers = this.convertValues(source["providers"], UsageStat);
	        this.models = this.convertValues(source["models"], UsageStat);
	        this.keys = this.convertValues(source["keys"], UsageStat);
	        this.credentials = this.convertValues(source["credentials"], UsageStat);
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
	export class PluginDelta {
	    pluginId: string;
	    before: number;
	    after: number;
	    saved: number;
	
	    static createFrom(source: any = {}) {
	        return new PluginDelta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pluginId = source["pluginId"];
	        this.before = source["before"];
	        this.after = source["after"];
	        this.saved = source["saved"];
	    }
	}
	export class RequestLog {
	    id: number;
	    createdAt: string;
	    tokenId: string;
	    tokenName: string;
	    providerId: string;
	    providerName: string;
	    clientModel: string;
	    upstreamModel: string;
	    userAgent: string;
	    requestBody: string;
	    responseBody: string;
	    inputTokens: number;
	    outputTokens: number;
	    cachedInputTokens: number;
	    reasoningOutputTokens: number;
	    success: boolean;
	    latencyMs: number;
	    errorMessage: string;
	    credentialId: string;
	    credentialName: string;
	    credentialMask: string;
	    pluginId: string;
	    pluginBeforeTokens: number;
	    pluginAfterTokens: number;
	    pluginSavedTokens: number;
	    pluginDeltas: PluginDelta[];
	
	    static createFrom(source: any = {}) {
	        return new RequestLog(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.createdAt = source["createdAt"];
	        this.tokenId = source["tokenId"];
	        this.tokenName = source["tokenName"];
	        this.providerId = source["providerId"];
	        this.providerName = source["providerName"];
	        this.clientModel = source["clientModel"];
	        this.upstreamModel = source["upstreamModel"];
	        this.userAgent = source["userAgent"];
	        this.requestBody = source["requestBody"];
	        this.responseBody = source["responseBody"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.cachedInputTokens = source["cachedInputTokens"];
	        this.reasoningOutputTokens = source["reasoningOutputTokens"];
	        this.success = source["success"];
	        this.latencyMs = source["latencyMs"];
	        this.errorMessage = source["errorMessage"];
	        this.credentialId = source["credentialId"];
	        this.credentialName = source["credentialName"];
	        this.credentialMask = source["credentialMask"];
	        this.pluginId = source["pluginId"];
	        this.pluginBeforeTokens = source["pluginBeforeTokens"];
	        this.pluginAfterTokens = source["pluginAfterTokens"];
	        this.pluginSavedTokens = source["pluginSavedTokens"];
	        this.pluginDeltas = this.convertValues(source["pluginDeltas"], PluginDelta);
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
	export class RequestLogFilter {
	    token: string;
	    model: string;
	    provider: string;
	    status: string;
	    from: string;
	    to: string;
	
	    static createFrom(source: any = {}) {
	        return new RequestLogFilter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.token = source["token"];
	        this.model = source["model"];
	        this.provider = source["provider"];
	        this.status = source["status"];
	        this.from = source["from"];
	        this.to = source["to"];
	    }
	}
	export class RequestLogStats {
	    requests: number;
	    successes: number;
	    inputTokens: number;
	    outputTokens: number;
	    cachedInputTokens: number;
	    reasoningOutputTokens: number;
	    inputCost: number;
	    outputCost: number;
	    cacheCost: number;
	    totalCost: number;
	
	    static createFrom(source: any = {}) {
	        return new RequestLogStats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.requests = source["requests"];
	        this.successes = source["successes"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.cachedInputTokens = source["cachedInputTokens"];
	        this.reasoningOutputTokens = source["reasoningOutputTokens"];
	        this.inputCost = source["inputCost"];
	        this.outputCost = source["outputCost"];
	        this.cacheCost = source["cacheCost"];
	        this.totalCost = source["totalCost"];
	    }
	}
	export class RequestLogPage {
	    items: RequestLog[];
	    page: number;
	    pageSize: number;
	    total: number;
	    totalPages: number;
	    stats: RequestLogStats;
	
	    static createFrom(source: any = {}) {
	        return new RequestLogPage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.items = this.convertValues(source["items"], RequestLog);
	        this.page = source["page"];
	        this.pageSize = source["pageSize"];
	        this.total = source["total"];
	        this.totalPages = source["totalPages"];
	        this.stats = this.convertValues(source["stats"], RequestLogStats);
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
	
	export class Summary {
	    requests: number;
	    inputTokens: number;
	    outputTokens: number;
	    cachedInputTokens: number;
	    reasoningOutputTokens: number;
	    inputCost: number;
	    outputCost: number;
	    cacheCost: number;
	    costUsd: number;
	    successRate: number;
	
	    static createFrom(source: any = {}) {
	        return new Summary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.requests = source["requests"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.cachedInputTokens = source["cachedInputTokens"];
	        this.reasoningOutputTokens = source["reasoningOutputTokens"];
	        this.inputCost = source["inputCost"];
	        this.outputCost = source["outputCost"];
	        this.cacheCost = source["cacheCost"];
	        this.costUsd = source["costUsd"];
	        this.successRate = source["successRate"];
	    }
	}

}

