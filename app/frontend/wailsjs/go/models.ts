export namespace core {
	
	export class BoardTask {
	    text: string;
	    done: boolean;
	    section?: string;
	
	    static createFrom(source: any = {}) {
	        return new BoardTask(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.text = source["text"];
	        this.done = source["done"];
	        this.section = source["section"];
	    }
	}
	export class BoardItem {
	    key: string;
	    reference: string;
	    startToken?: string;
	    id: string;
	    title: string;
	    summary?: string;
	    kind: string;
	    column: string;
	    total: number;
	    done: number;
	    specs: number;
	    hasPlan: boolean;
	    updated?: string;
	    tasks?: BoardTask[];
	    agents?: string[];
	    branches?: string[];
	    problems?: string[];
	
	    static createFrom(source: any = {}) {
	        return new BoardItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.reference = source["reference"];
	        this.startToken = source["startToken"];
	        this.id = source["id"];
	        this.title = source["title"];
	        this.summary = source["summary"];
	        this.kind = source["kind"];
	        this.column = source["column"];
	        this.total = source["total"];
	        this.done = source["done"];
	        this.specs = source["specs"];
	        this.hasPlan = source["hasPlan"];
	        this.updated = source["updated"];
	        this.tasks = this.convertValues(source["tasks"], BoardTask);
	        this.agents = source["agents"];
	        this.branches = source["branches"];
	        this.problems = source["problems"];
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
	export class BoardSource {
	    kind: string;
	    location: string;
	    items: number;
	    archived: number;
	    specs: number;
	    availability?: string;
	    problems?: string[];
	
	    static createFrom(source: any = {}) {
	        return new BoardSource(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.location = source["location"];
	        this.items = source["items"];
	        this.archived = source["archived"];
	        this.specs = source["specs"];
	        this.availability = source["availability"];
	        this.problems = source["problems"];
	    }
	}
	export class Board {
	    projectId?: string;
	    project: string;
	    kind: string;
	    sources?: BoardSource[];
	    items: BoardItem[];
	    archived: number;
	    specs: number;
	    err?: string;
	
	    static createFrom(source: any = {}) {
	        return new Board(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projectId = source["projectId"];
	        this.project = source["project"];
	        this.kind = source["kind"];
	        this.sources = this.convertValues(source["sources"], BoardSource);
	        this.items = this.convertValues(source["items"], BoardItem);
	        this.archived = source["archived"];
	        this.specs = source["specs"];
	        this.err = source["err"];
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
	
	
	
	export class BreakAdvice {
	    enabled: boolean;
	    level: string;
	    workedSecs: number;
	    restingSecs: number;
	    goodMoment: boolean;
	    waiting: number;
	    busy: number;
	    message: string;
	    snoozed: boolean;
	    nextDueSecs: number;
	
	    static createFrom(source: any = {}) {
	        return new BreakAdvice(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.level = source["level"];
	        this.workedSecs = source["workedSecs"];
	        this.restingSecs = source["restingSecs"];
	        this.goodMoment = source["goodMoment"];
	        this.waiting = source["waiting"];
	        this.busy = source["busy"];
	        this.message = source["message"];
	        this.snoozed = source["snoozed"];
	        this.nextDueSecs = source["nextDueSecs"];
	    }
	}
	export class BreakConfig {
	    enabled: boolean;
	    hintAfter: number;
	    dueAfter: number;
	    overdueAfter: number;
	    minBreak: number;
	    idleResets: number;
	    snoozeMins: number;
	    breakMins: number;
	
	    static createFrom(source: any = {}) {
	        return new BreakConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.hintAfter = source["hintAfter"];
	        this.dueAfter = source["dueAfter"];
	        this.overdueAfter = source["overdueAfter"];
	        this.minBreak = source["minBreak"];
	        this.idleResets = source["idleResets"];
	        this.snoozeMins = source["snoozeMins"];
	        this.breakMins = source["breakMins"];
	    }
	}
	export class RepositoryProblem {
	    operation: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new RepositoryProblem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.operation = source["operation"];
	        this.message = source["message"];
	    }
	}
	export class GraphBranch {
	    name: string;
	    lane: number;
	    isMain: boolean;
	    worktreeRef?: string;
	    worktreeLocation?: string;
	    ahead: number;
	    behind: number;
	    divergenceKnown: boolean;
	    merged: boolean;
	    mergedKnown: boolean;
	    agents?: string[];
	
	    static createFrom(source: any = {}) {
	        return new GraphBranch(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.lane = source["lane"];
	        this.isMain = source["isMain"];
	        this.worktreeRef = source["worktreeRef"];
	        this.worktreeLocation = source["worktreeLocation"];
	        this.ahead = source["ahead"];
	        this.behind = source["behind"];
	        this.divergenceKnown = source["divergenceKnown"];
	        this.merged = source["merged"];
	        this.mergedKnown = source["mergedKnown"];
	        this.agents = source["agents"];
	    }
	}
	export class GraphRef {
	    name: string;
	    kind: string;
	    worktreeRef?: string;
	    worktreeLocation?: string;
	    current?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new GraphRef(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.worktreeRef = source["worktreeRef"];
	        this.worktreeLocation = source["worktreeLocation"];
	        this.current = source["current"];
	    }
	}
	export class GraphCommit {
	    hash: string;
	    short: string;
	    parents: string[];
	    subject: string;
	    author: string;
	    age: string;
	    time: number;
	    lane: number;
	    merge: boolean;
	    refs: GraphRef[];
	    agents?: string[];
	
	    static createFrom(source: any = {}) {
	        return new GraphCommit(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hash = source["hash"];
	        this.short = source["short"];
	        this.parents = source["parents"];
	        this.subject = source["subject"];
	        this.author = source["author"];
	        this.age = source["age"];
	        this.time = source["time"];
	        this.lane = source["lane"];
	        this.merge = source["merge"];
	        this.refs = this.convertValues(source["refs"], GraphRef);
	        this.agents = source["agents"];
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
	export class GitGraph {
	    projectId: string;
	    project: string;
	    main: string;
	    lanes: number;
	    commits: GraphCommit[];
	    branches: GraphBranch[];
	    truncated: boolean;
	    availability: string;
	    problems?: RepositoryProblem[];
	    err?: string;
	
	    static createFrom(source: any = {}) {
	        return new GitGraph(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projectId = source["projectId"];
	        this.project = source["project"];
	        this.main = source["main"];
	        this.lanes = source["lanes"];
	        this.commits = this.convertValues(source["commits"], GraphCommit);
	        this.branches = this.convertValues(source["branches"], GraphBranch);
	        this.truncated = source["truncated"];
	        this.availability = source["availability"];
	        this.problems = this.convertValues(source["problems"], RepositoryProblem);
	        this.err = source["err"];
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
	
	
	
	export class HistoryMeasure {
	    value: number;
	    coverage: string;
	    knownEvents: number;
	    unknownEvents: number;
	
	    static createFrom(source: any = {}) {
	        return new HistoryMeasure(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.value = source["value"];
	        this.coverage = source["coverage"];
	        this.knownEvents = source["knownEvents"];
	        this.unknownEvents = source["unknownEvents"];
	    }
	}
	export class HistoryProblem {
	    provider: string;
	    sourceId?: string;
	    kind: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new HistoryProblem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider = source["provider"];
	        this.sourceId = source["sourceId"];
	        this.kind = source["kind"];
	        this.message = source["message"];
	    }
	}
	export class HistoryUsageSummary {
	    inputTokens: HistoryMeasure;
	    outputTokens: HistoryMeasure;
	    cacheReadTokens: HistoryMeasure;
	    cacheWriteTokens: HistoryMeasure;
	
	    static createFrom(source: any = {}) {
	        return new HistoryUsageSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.inputTokens = this.convertValues(source["inputTokens"], HistoryMeasure);
	        this.outputTokens = this.convertValues(source["outputTokens"], HistoryMeasure);
	        this.cacheReadTokens = this.convertValues(source["cacheReadTokens"], HistoryMeasure);
	        this.cacheWriteTokens = this.convertValues(source["cacheWriteTokens"], HistoryMeasure);
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
	export class OvQueuedMessage {
	    id: string;
	    kind: string;
	    preview: string;
	    age: string;
	    stuck: boolean;
	
	    static createFrom(source: any = {}) {
	        return new OvQueuedMessage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.preview = source["preview"];
	        this.age = source["age"];
	        this.stuck = source["stuck"];
	    }
	}
	export class OvAgent {
	    id: string;
	    name: string;
	    tool?: string;
	    status: string;
	    label: string;
	    detail: string;
	    age: string;
	    worktree: boolean;
	    term: boolean;
	    phase?: string;
	    phaseLabel?: string;
	    deployed: boolean;
	    known: boolean;
	    ownDirty: number;
	    ownCommits: number;
	    branch?: string;
	    unread: boolean;
	    dock: boolean;
	    handoffSource: boolean;
	    handoffTarget: boolean;
	    queued?: OvQueuedMessage[];
	
	    static createFrom(source: any = {}) {
	        return new OvAgent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.tool = source["tool"];
	        this.status = source["status"];
	        this.label = source["label"];
	        this.detail = source["detail"];
	        this.age = source["age"];
	        this.worktree = source["worktree"];
	        this.term = source["term"];
	        this.phase = source["phase"];
	        this.phaseLabel = source["phaseLabel"];
	        this.deployed = source["deployed"];
	        this.known = source["known"];
	        this.ownDirty = source["ownDirty"];
	        this.ownCommits = source["ownCommits"];
	        this.branch = source["branch"];
	        this.unread = source["unread"];
	        this.dock = source["dock"];
	        this.handoffSource = source["handoffSource"];
	        this.handoffTarget = source["handoffTarget"];
	        this.queued = this.convertValues(source["queued"], OvQueuedMessage);
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
	export class OvLater {
	    id: string;
	    name: string;
	    project: string;
	    age: string;
	    term: boolean;
	    tool?: string;
	
	    static createFrom(source: any = {}) {
	        return new OvLater(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.project = source["project"];
	        this.age = source["age"];
	        this.term = source["term"];
	        this.tool = source["tool"];
	    }
	}
	export class OvWorktree {
	    reference?: string;
	    location?: string;
	    branch: string;
	    isMain: boolean;
	    ahead: number;
	    behind: number;
	    staged: number;
	    modified: number;
	    untracked: number;
	    conflicted: number;
	    clean: boolean;
	    lastMsg: string;
	    checkoutKnown: boolean;
	    changesKnown: boolean;
	    divergenceKnown: boolean;
	    problems?: RepositoryProblem[];
	    agents: OvAgent[];
	    warnings: string[];
	
	    static createFrom(source: any = {}) {
	        return new OvWorktree(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.reference = source["reference"];
	        this.location = source["location"];
	        this.branch = source["branch"];
	        this.isMain = source["isMain"];
	        this.ahead = source["ahead"];
	        this.behind = source["behind"];
	        this.staged = source["staged"];
	        this.modified = source["modified"];
	        this.untracked = source["untracked"];
	        this.conflicted = source["conflicted"];
	        this.clean = source["clean"];
	        this.lastMsg = source["lastMsg"];
	        this.checkoutKnown = source["checkoutKnown"];
	        this.changesKnown = source["changesKnown"];
	        this.divergenceKnown = source["divergenceKnown"];
	        this.problems = this.convertValues(source["problems"], RepositoryProblem);
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
	    id: string;
	    name: string;
	    path: string;
	    mainBranch: string;
	    headBranch: string;
	    mainConfigured: boolean;
	    repositoryKnowledge: string;
	    mainBranchKnown: boolean;
	    headBranchKnown: boolean;
	    worktreesKnown: boolean;
	    problems?: RepositoryProblem[];
	    worktrees: OvWorktree[];
	
	    static createFrom(source: any = {}) {
	        return new OvProject(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.path = source["path"];
	        this.mainBranch = source["mainBranch"];
	        this.headBranch = source["headBranch"];
	        this.mainConfigured = source["mainConfigured"];
	        this.repositoryKnowledge = source["repositoryKnowledge"];
	        this.mainBranchKnown = source["mainBranchKnown"];
	        this.headBranchKnown = source["headBranchKnown"];
	        this.worktreesKnown = source["worktreesKnown"];
	        this.problems = this.convertValues(source["problems"], RepositoryProblem);
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
	    later: OvLater[];
	
	    static createFrom(source: any = {}) {
	        return new Overview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.generatedAt = source["generatedAt"];
	        this.counts = source["counts"];
	        this.usage = this.convertValues(source["usage"], OvUsage);
	        this.projects = this.convertValues(source["projects"], OvProject);
	        this.later = this.convertValues(source["later"], OvLater);
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
	
	export class StatsTotals {
	    days: number;
	    prompts: number;
	    turns: number;
	    sessions: number;
	    tokens: number;
	    input: number;
	    output: number;
	    cacheRead: number;
	    cacheWrite: number;
	    cost: number;
	    costState: string;
	    commits: number;
	    cacheHit: number;
	    busiestDay: string;
	    streak: number;
	
	    static createFrom(source: any = {}) {
	        return new StatsTotals(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.days = source["days"];
	        this.prompts = source["prompts"];
	        this.turns = source["turns"];
	        this.sessions = source["sessions"];
	        this.tokens = source["tokens"];
	        this.input = source["input"];
	        this.output = source["output"];
	        this.cacheRead = source["cacheRead"];
	        this.cacheWrite = source["cacheWrite"];
	        this.cost = source["cost"];
	        this.costState = source["costState"];
	        this.commits = source["commits"];
	        this.cacheHit = source["cacheHit"];
	        this.busiestDay = source["busiestDay"];
	        this.streak = source["streak"];
	    }
	}
	export class StatsCommitProblem {
	    project: string;
	    kind: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new StatsCommitProblem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.project = source["project"];
	        this.kind = source["kind"];
	        this.message = source["message"];
	    }
	}
	export class StatsCommitCoverage {
	    state: string;
	    repositories: number;
	    availableRepositories: number;
	    problems?: StatsCommitProblem[];
	
	    static createFrom(source: any = {}) {
	        return new StatsCommitCoverage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.repositories = source["repositories"];
	        this.availableRepositories = source["availableRepositories"];
	        this.problems = this.convertValues(source["problems"], StatsCommitProblem);
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
	export class StatsProvider {
	    provider: string;
	    source: string;
	    state: string;
	    prompts: number;
	    turns: number;
	    tokens: number;
	    usage: HistoryUsageSummary;
	    problems?: HistoryProblem[];
	
	    static createFrom(source: any = {}) {
	        return new StatsProvider(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider = source["provider"];
	        this.source = source["source"];
	        this.state = source["state"];
	        this.prompts = source["prompts"];
	        this.turns = source["turns"];
	        this.tokens = source["tokens"];
	        this.usage = this.convertValues(source["usage"], HistoryUsageSummary);
	        this.problems = this.convertValues(source["problems"], HistoryProblem);
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
	export class StatsModel {
	    model: string;
	    provider: string;
	    source?: string;
	    turns: number;
	    input: number;
	    output: number;
	    cacheRead: number;
	    cacheWrite: number;
	    cost: number;
	    costState: string;
	
	    static createFrom(source: any = {}) {
	        return new StatsModel(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.model = source["model"];
	        this.provider = source["provider"];
	        this.source = source["source"];
	        this.turns = source["turns"];
	        this.input = source["input"];
	        this.output = source["output"];
	        this.cacheRead = source["cacheRead"];
	        this.cacheWrite = source["cacheWrite"];
	        this.cost = source["cost"];
	        this.costState = source["costState"];
	    }
	}
	export class StatsProject {
	    name: string;
	    tokens: number;
	    cost: number;
	    costState: string;
	    prompts: number;
	    sessions: number;
	    commits: number;
	    commitState: string;
	    active: number;
	
	    static createFrom(source: any = {}) {
	        return new StatsProject(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.tokens = source["tokens"];
	        this.cost = source["cost"];
	        this.costState = source["costState"];
	        this.prompts = source["prompts"];
	        this.sessions = source["sessions"];
	        this.commits = source["commits"];
	        this.commitState = source["commitState"];
	        this.active = source["active"];
	    }
	}
	export class StatsDay {
	    date: string;
	    weekday: string;
	    prompts: number;
	    turns: number;
	    input: number;
	    output: number;
	    cacheRead: number;
	    cacheWrite: number;
	    cost: number;
	    costState: string;
	    sessions: number;
	    commits: number;
	
	    static createFrom(source: any = {}) {
	        return new StatsDay(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.date = source["date"];
	        this.weekday = source["weekday"];
	        this.prompts = source["prompts"];
	        this.turns = source["turns"];
	        this.input = source["input"];
	        this.output = source["output"];
	        this.cacheRead = source["cacheRead"];
	        this.cacheWrite = source["cacheWrite"];
	        this.cost = source["cost"];
	        this.costState = source["costState"];
	        this.sessions = source["sessions"];
	        this.commits = source["commits"];
	    }
	}
	export class Stats {
	    range: number;
	    days: StatsDay[];
	    projects: StatsProject[];
	    models: StatsModel[];
	    providers: StatsProvider[];
	    commitCoverage: StatsCommitCoverage;
	    heatmap: number[][];
	    hours: number[];
	    totals: StatsTotals;
	    err?: string;
	
	    static createFrom(source: any = {}) {
	        return new Stats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.range = source["range"];
	        this.days = this.convertValues(source["days"], StatsDay);
	        this.projects = this.convertValues(source["projects"], StatsProject);
	        this.models = this.convertValues(source["models"], StatsModel);
	        this.providers = this.convertValues(source["providers"], StatsProvider);
	        this.commitCoverage = this.convertValues(source["commitCoverage"], StatsCommitCoverage);
	        this.heatmap = source["heatmap"];
	        this.hours = source["hours"];
	        this.totals = this.convertValues(source["totals"], StatsTotals);
	        this.err = source["err"];
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
	
	
	
	
	
	
	
	export class ZgProject {
	    id: string;
	    name: string;
	    client: string;
	    rate: number;
	    color: string;
	
	    static createFrom(source: any = {}) {
	        return new ZgProject(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.client = source["client"];
	        this.rate = source["rate"];
	        this.color = source["color"];
	    }
	}
	export class ZgInfo {
	    exists: boolean;
	    active: boolean;
	    state: string;
	    project: string;
	    rate: number;
	    start: string;
	    elapsedSec: number;
	    earnings: number;
	    todaySec: number;
	    todayCash: number;
	    lastProject: string;
	    projects: ZgProject[];
	
	    static createFrom(source: any = {}) {
	        return new ZgInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.exists = source["exists"];
	        this.active = source["active"];
	        this.state = source["state"];
	        this.project = source["project"];
	        this.rate = source["rate"];
	        this.start = source["start"];
	        this.elapsedSec = source["elapsedSec"];
	        this.earnings = source["earnings"];
	        this.todaySec = source["todaySec"];
	        this.todayCash = source["todayCash"];
	        this.lastProject = source["lastProject"];
	        this.projects = this.convertValues(source["projects"], ZgProject);
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
	
	export class ZgStopped {
	    id: string;
	    project: string;
	    durationSec: number;
	    earnings: number;
	
	    static createFrom(source: any = {}) {
	        return new ZgStopped(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.project = source["project"];
	        this.durationSec = source["durationSec"];
	        this.earnings = source["earnings"];
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
	export class AzAccount {
	    id: string;
	    name: string;
	    isDefault: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AzAccount(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.isDefault = source["isDefault"];
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
	export class DeployRemoteProblem {
	    project: string;
	    state: string;
	    operation: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new DeployRemoteProblem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.project = source["project"];
	        this.state = source["state"];
	        this.operation = source["operation"];
	        this.message = source["message"];
	    }
	}
	export class DeployRemoteCoverage {
	    state: string;
	    projects: number;
	    availableProjects: number;
	    problems?: DeployRemoteProblem[];
	
	    static createFrom(source: any = {}) {
	        return new DeployRemoteCoverage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.projects = source["projects"];
	        this.availableProjects = source["availableProjects"];
	        this.problems = this.convertValues(source["problems"], DeployRemoteProblem);
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
	
	export class DeployStatus {
	    azOk: boolean;
	    azErr: string;
	    azSub: string;
	    azSubId: string;
	    azRemoteCoverage: DeployRemoteCoverage;
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
	        this.azSub = source["azSub"];
	        this.azSubId = source["azSubId"];
	        this.azRemoteCoverage = this.convertValues(source["azRemoteCoverage"], DeployRemoteCoverage);
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
	export class DockSessionRef {
	    id: string;
	    name: string;
	
	    static createFrom(source: any = {}) {
	        return new DockSessionRef(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	    }
	}
	export class LinkInfo {
	    url: string;
	    time: string;
	
	    static createFrom(source: any = {}) {
	        return new LinkInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	        this.time = source["time"];
	    }
	}
	export class SearchHit {
	    project: string;
	    projectKnown: boolean;
	    attributionProblem?: string;
	    provider: string;
	    role: string;
	    time: string;
	    timeRaw: string;
	    snippet: string;
	    full: string;
	
	    static createFrom(source: any = {}) {
	        return new SearchHit(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.project = source["project"];
	        this.projectKnown = source["projectKnown"];
	        this.attributionProblem = source["attributionProblem"];
	        this.provider = source["provider"];
	        this.role = source["role"];
	        this.time = source["time"];
	        this.timeRaw = source["timeRaw"];
	        this.snippet = source["snippet"];
	        this.full = source["full"];
	    }
	}
	export class TimelineSource {
	    source: string;
	    state: string;
	    problems?: string[];
	
	    static createFrom(source: any = {}) {
	        return new TimelineSource(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.source = source["source"];
	        this.state = source["state"];
	        this.problems = source["problems"];
	    }
	}
	export class SearchResult {
	    hits: SearchHit[];
	    sources: TimelineSource[];
	
	    static createFrom(source: any = {}) {
	        return new SearchResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hits = this.convertValues(source["hits"], SearchHit);
	        this.sources = this.convertValues(source["sources"], TimelineSource);
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
	export class SessionLinksResult {
	    links: LinkInfo[];
	    sources: TimelineSource[];
	
	    static createFrom(source: any = {}) {
	        return new SessionLinksResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.links = this.convertValues(source["links"], LinkInfo);
	        this.sources = this.convertValues(source["sources"], TimelineSource);
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
	export class SessionPreviewResult {
	    content: string;
	    contentKnown: boolean;
	    source: TimelineSource;
	
	    static createFrom(source: any = {}) {
	        return new SessionPreviewResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.content = source["content"];
	        this.contentKnown = source["contentKnown"];
	        this.source = this.convertValues(source["source"], TimelineSource);
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
	export class TimelineEntry {
	    agent: string;
	    project: string;
	    projectKnown: boolean;
	    attributionProblem?: string;
	    source: string;
	    day: string;
	    time: string;
	    timeRaw: string;
	    text: string;
	
	    static createFrom(source: any = {}) {
	        return new TimelineEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.agent = source["agent"];
	        this.project = source["project"];
	        this.projectKnown = source["projectKnown"];
	        this.attributionProblem = source["attributionProblem"];
	        this.source = source["source"];
	        this.day = source["day"];
	        this.time = source["time"];
	        this.timeRaw = source["timeRaw"];
	        this.text = source["text"];
	    }
	}
	export class TimelineResult {
	    entries: TimelineEntry[];
	    sources: TimelineSource[];
	
	    static createFrom(source: any = {}) {
	        return new TimelineResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.entries = this.convertValues(source["entries"], TimelineEntry);
	        this.sources = this.convertValues(source["sources"], TimelineSource);
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

