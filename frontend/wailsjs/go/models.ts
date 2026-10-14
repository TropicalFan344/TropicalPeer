export namespace appcore {
	
	export class DeviceInfo {
	    name: string;
	    id: string;
	    port: number;
	    version: string;
	
	    static createFrom(source: any = {}) {
	        return new DeviceInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.id = source["id"];
	        this.port = source["port"];
	        this.version = source["version"];
	    }
	}
	export class FileDTO {
	    name: string;
	    size: number;
	
	    static createFrom(source: any = {}) {
	        return new FileDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.size = source["size"];
	    }
	}
	export class Invite {
	    code: string;
	    expiresAt: number;
	
	    static createFrom(source: any = {}) {
	        return new Invite(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.code = source["code"];
	        this.expiresAt = source["expiresAt"];
	    }
	}
	export class VisitorDTO {
	    session: string;
	    name: string;
	    status: string;
	    done: number;
	    total: number;
	    speed: number;
	    error: string;
	    seenAt: number;
	
	    static createFrom(source: any = {}) {
	        return new VisitorDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.session = source["session"];
	        this.name = source["name"];
	        this.status = source["status"];
	        this.done = source["done"];
	        this.total = source["total"];
	        this.speed = source["speed"];
	        this.error = source["error"];
	        this.seenAt = source["seenAt"];
	    }
	}
	export class LinkDTO {
	    id: string;
	    url: string;
	    qr: string[];
	    files: FileDTO[];
	    total: number;
	    unlimited: boolean;
	    ended: string;
	    offline: boolean;
	    createdAt: number;
	    visitors: VisitorDTO[];
	
	    static createFrom(source: any = {}) {
	        return new LinkDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.url = source["url"];
	        this.qr = source["qr"];
	        this.files = this.convertValues(source["files"], FileDTO);
	        this.total = source["total"];
	        this.unlimited = source["unlimited"];
	        this.ended = source["ended"];
	        this.offline = source["offline"];
	        this.createdAt = source["createdAt"];
	        this.visitors = this.convertValues(source["visitors"], VisitorDTO);
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
	export class NetworkDTO {
	    serverAddr: string;
	    serverOnline: boolean;
	    serverError: string;
	    rttMs: number;
	    relay: boolean;
	    detection: boolean;
	    publicAddr: string;
	    router: string;
	    portMapping: boolean;
	    forwarded: string;
	    mappingErr: string;
	    checking: boolean;
	
	    static createFrom(source: any = {}) {
	        return new NetworkDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.serverAddr = source["serverAddr"];
	        this.serverOnline = source["serverOnline"];
	        this.serverError = source["serverError"];
	        this.rttMs = source["rttMs"];
	        this.relay = source["relay"];
	        this.detection = source["detection"];
	        this.publicAddr = source["publicAddr"];
	        this.router = source["router"];
	        this.portMapping = source["portMapping"];
	        this.forwarded = source["forwarded"];
	        this.mappingErr = source["mappingErr"];
	        this.checking = source["checking"];
	    }
	}
	export class OfferDTO {
	    id: string;
	    peerId: string;
	    peerName: string;
	    files: FileDTO[];
	    total: number;
	    saveDir: string;
	    expiresAt: number;
	
	    static createFrom(source: any = {}) {
	        return new OfferDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.peerId = source["peerId"];
	        this.peerName = source["peerName"];
	        this.files = this.convertValues(source["files"], FileDTO);
	        this.total = source["total"];
	        this.saveDir = source["saveDir"];
	        this.expiresAt = source["expiresAt"];
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
	export class PeerDTO {
	    id: string;
	    name: string;
	    policy: string;
	    maxAutoSize: number;
	    saveDir: string;
	    addedAt: number;
	    removedUs: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PeerDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.policy = source["policy"];
	        this.maxAutoSize = source["maxAutoSize"];
	        this.saveDir = source["saveDir"];
	        this.addedAt = source["addedAt"];
	        this.removedUs = source["removedUs"];
	    }
	}
	export class PeerUpdate {
	    name: string;
	    policy: string;
	    maxAutoSize: number;
	    saveDir: string;
	
	    static createFrom(source: any = {}) {
	        return new PeerUpdate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.policy = source["policy"];
	        this.maxAutoSize = source["maxAutoSize"];
	        this.saveDir = source["saveDir"];
	    }
	}
	export class ServerStatus {
	    address: string;
	    online: boolean;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new ServerStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.address = source["address"];
	        this.online = source["online"];
	        this.error = source["error"];
	    }
	}
	export class SettingsDTO {
	    serverHost: string;
	    serverPort: number;
	    listenPort: number;
	    downloadDir: string;
	    portMapping: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SettingsDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.serverHost = source["serverHost"];
	        this.serverPort = source["serverPort"];
	        this.listenPort = source["listenPort"];
	        this.downloadDir = source["downloadDir"];
	        this.portMapping = source["portMapping"];
	    }
	}
	export class TransferDTO {
	    id: string;
	    peerId: string;
	    peerName: string;
	    direction: string;
	    status: string;
	    files: FileDTO[];
	    fileIndex: number;
	    done: number;
	    total: number;
	    speed: number;
	    relayed: boolean;
	    upgraded: boolean;
	    resuming: boolean;
	    pausedByPeer: boolean;
	    error: string;
	    saveDir: string;
	    saved: string[];
	    startedAt: number;
	    endedAt: number;
	
	    static createFrom(source: any = {}) {
	        return new TransferDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.peerId = source["peerId"];
	        this.peerName = source["peerName"];
	        this.direction = source["direction"];
	        this.status = source["status"];
	        this.files = this.convertValues(source["files"], FileDTO);
	        this.fileIndex = source["fileIndex"];
	        this.done = source["done"];
	        this.total = source["total"];
	        this.speed = source["speed"];
	        this.relayed = source["relayed"];
	        this.upgraded = source["upgraded"];
	        this.resuming = source["resuming"];
	        this.pausedByPeer = source["pausedByPeer"];
	        this.error = source["error"];
	        this.saveDir = source["saveDir"];
	        this.saved = source["saved"];
	        this.startedAt = source["startedAt"];
	        this.endedAt = source["endedAt"];
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
	export class UpdateDTO {
	    state: string;
	    current: string;
	    version: string;
	    done: number;
	    total: number;
	    error: string;
	    checkedAt: number;
	    auto: boolean;
	    restartAt: number;
	
	    static createFrom(source: any = {}) {
	        return new UpdateDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.current = source["current"];
	        this.version = source["version"];
	        this.done = source["done"];
	        this.total = source["total"];
	        this.error = source["error"];
	        this.checkedAt = source["checkedAt"];
	        this.auto = source["auto"];
	        this.restartAt = source["restartAt"];
	    }
	}

}

