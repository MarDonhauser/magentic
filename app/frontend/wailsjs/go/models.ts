export namespace core {
	
	export class OvAgent {
	    name: string;
	    status: string;
	    label: string;
	    age: string;
	    worktree: boolean;
	
	    static createFrom(source: any = {}) {
	        return new OvAgent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.status = source["status"];
	        this.label = source["label"];
	        this.age = source["age"];
	        this.worktree = source["worktree"];
	    }
	}
	export class OvWorktree {
	    path: string;
	    ShortPath: string;
	    branch: string;
	    isMain: boolean;
	    ahead: number;
	    behind: number;
	    staged: number;
	    modified: number;
	    untracked: number;
	    clean: boolean;
	    lastMsg: string;
	    agents: OvAgent[];
	    warnings: string[];
	
	    static createFrom(source: any = {}) {
	        return new OvWorktree(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.ShortPath = source["ShortPath"];
	        this.branch = source["branch"];
	        this.isMain = source["isMain"];
	        this.ahead = source["ahead"];
	        this.behind = source["behind"];
	        this.staged = source["staged"];
	        this.modified = source["modified"];
	        this.untracked = source["untracked"];
	        this.clean = source["clean"];
	        this.lastMsg = source["lastMsg"];
	        this.agents = this.convertValues(source["agents"], OvAgent);
	        this.warnings = source["warnings"];
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
	export class OvProject {
	    name: string;
	    path: string;
	    mainBranch: string;
	    headBranch: string;
	    mainConfigured: boolean;
	    worktrees: OvWorktree[];
	
	    static createFrom(source: any = {}) {
	        return new OvProject(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.mainBranch = source["mainBranch"];
	        this.headBranch = source["headBranch"];
	        this.mainConfigured = source["mainConfigured"];
	        this.worktrees = this.convertValues(source["worktrees"], OvWorktree);
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
	export class OvUsage {
	    fiveHour: number;
	    fiveHourReset: string;
	    sevenDay: number;
	    sevenDayReset: string;
	
	    static createFrom(source: any = {}) {
	        return new OvUsage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.fiveHour = source["fiveHour"];
	        this.fiveHourReset = source["fiveHourReset"];
	        this.sevenDay = source["sevenDay"];
	        this.sevenDayReset = source["sevenDayReset"];
	    }
	}
	
	export class Overview {
	    generatedAt: string;
	    counts: Record<string, number>;
	    usage?: OvUsage;
	    projects: OvProject[];
	
	    static createFrom(source: any = {}) {
	        return new Overview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.generatedAt = source["generatedAt"];
	        this.counts = source["counts"];
	        this.usage = this.convertValues(source["usage"], OvUsage);
	        this.projects = this.convertValues(source["projects"], OvProject);
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
	
	export class ArgoApp {
	    name: string;
	    namespace: string;
	    sync: string;
	    health: string;
	    url: string;
	
	    static createFrom(source: any = {}) {
	        return new ArgoApp(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.namespace = source["namespace"];
	        this.sync = source["sync"];
	        this.health = source["health"];
	        this.url = source["url"];
	    }
	}
	export class BuildInfo {
	    repo: string;
	    status: string;
	    result: string;
	    branch: string;
	    age: string;
	    url: string;
	
	    static createFrom(source: any = {}) {
	        return new BuildInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.repo = source["repo"];
	        this.status = source["status"];
	        this.result = source["result"];
	        this.branch = source["branch"];
	        this.age = source["age"];
	        this.url = source["url"];
	    }
	}
	export class DeployStatus {
	    azOk: boolean;
	    azErr: string;
	    argoOk: boolean;
	    argoServer: string;
	    argoErr: string;
	    builds: BuildInfo[];
	    apps: ArgoApp[];
	
	    static createFrom(source: any = {}) {
	        return new DeployStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.azOk = source["azOk"];
	        this.azErr = source["azErr"];
	        this.argoOk = source["argoOk"];
	        this.argoServer = source["argoServer"];
	        this.argoErr = source["argoErr"];
	        this.builds = this.convertValues(source["builds"], BuildInfo);
	        this.apps = this.convertValues(source["apps"], ArgoApp);
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
	export class TodoInfo {
	    index: number;
	    text: string;
	    project: string;
	    age: string;
	
	    static createFrom(source: any = {}) {
	        return new TodoInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.text = source["text"];
	        this.project = source["project"];
	        this.age = source["age"];
	    }
	}

}

