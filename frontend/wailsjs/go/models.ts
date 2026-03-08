export namespace db {
	
	export class Access {
	    id: number;
	    name: string;
	    hostname: string;
	    localPort: number;
	    serviceTokenId: string;
	    serviceTokenSecret: string;
	    autoStart: boolean;
	    createdAt: string;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new Access(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.hostname = source["hostname"];
	        this.localPort = source["localPort"];
	        this.serviceTokenId = source["serviceTokenId"];
	        this.serviceTokenSecret = source["serviceTokenSecret"];
	        this.autoStart = source["autoStart"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	    }
	}
	export class Tunnel {
	    id: number;
	    name: string;
	    localHost: string;
	    localPort: number;
	    protocol: string;
	    tunnelType: string;
	    token: string;
	    customDomain: string;
	    autoStart: boolean;
	    noTLSVerify: boolean;
	    createdAt: string;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new Tunnel(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.localHost = source["localHost"];
	        this.localPort = source["localPort"];
	        this.protocol = source["protocol"];
	        this.tunnelType = source["tunnelType"];
	        this.token = source["token"];
	        this.customDomain = source["customDomain"];
	        this.autoStart = source["autoStart"];
	        this.noTLSVerify = source["noTLSVerify"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	    }
	}

}

export namespace main {
	
	export class AccessStatus {
	    running: boolean;
	    error: string;
	    lastLog: string;
	
	    static createFrom(source: any = {}) {
	        return new AccessStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	        this.error = source["error"];
	        this.lastLog = source["lastLog"];
	    }
	}
	export class ScanResult {
	    ip: string;
	    port: number;
	    proto: string;
	    latency: number;
	
	    static createFrom(source: any = {}) {
	        return new ScanResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ip = source["ip"];
	        this.port = source["port"];
	        this.proto = source["proto"];
	        this.latency = source["latency"];
	    }
	}
	export class TunnelStatus {
	    running: boolean;
	    url: string;
	    error: string;
	    lastLog: string;
	
	    static createFrom(source: any = {}) {
	        return new TunnelStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	        this.url = source["url"];
	        this.error = source["error"];
	        this.lastLog = source["lastLog"];
	    }
	}

}

export namespace tunnel {
	
	export class Metrics {
	    haConnections: number;
	    totalRequests: number;
	    requestErrors: number;
	    latestRtt: number;
	    sentBytes: number;
	    receivedBytes: number;
	    concurrentRequests: number;
	
	    static createFrom(source: any = {}) {
	        return new Metrics(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.haConnections = source["haConnections"];
	        this.totalRequests = source["totalRequests"];
	        this.requestErrors = source["requestErrors"];
	        this.latestRtt = source["latestRtt"];
	        this.sentBytes = source["sentBytes"];
	        this.receivedBytes = source["receivedBytes"];
	        this.concurrentRequests = source["concurrentRequests"];
	    }
	}

}

