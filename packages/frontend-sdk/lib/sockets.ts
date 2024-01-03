export class GrabberSocket<T> {
    private readonly url: string;
    private target: EventTarget;
    private messageQueue: any[];
    private isClosed: boolean;
    private ws?: WebSocket;
    constructor(url: string) {
        if (!url.startsWith("ws")) {
            url = (window.location.protocol === "http:" ? "ws:" : "wss:") + window.location.host + url;
        }
        this.url = url;
        this.target = new EventTarget();
        this.messageQueue = [];
        this.isClosed = false;
        this.connect();
    }

    connect() {
        if (this.isClosed) {
            return;
        }
        const ws = new WebSocket(this.url);
        const _this = this;
        ws.onopen = function () {
            while (_this.messageQueue.length > 0) {
                ws.send(JSON.stringify(_this.messageQueue[0]));
                _this.messageQueue.splice(0, 1);
            }
        }
        ws.onmessage = function ({data}) {
            const payload = JSON.parse(data);
            _this.target.dispatchEvent(new CustomEvent(payload.event, {detail: payload}));
        }
        ws.onclose = function () {
            if (_this.isClosed) {
                return;
            }
            setTimeout(() => _this.connect(), 1000);
        }
        this.ws = ws;
    }

    emit(event: string, payload: T) {
        const data = {...payload, "event": event};
        if (this.ws && this.ws.readyState === this.ws.OPEN) {
            this.ws.send(JSON.stringify(data));
        } else {
            this.messageQueue.push(data);
        }
    }

    on(event: string, callback: (payload: T) => void) {
        this.target.addEventListener(event, ({detail}: any) => callback(detail));
    }

    close() {
        this.isClosed = true;
        this.ws?.close();
    }
}
