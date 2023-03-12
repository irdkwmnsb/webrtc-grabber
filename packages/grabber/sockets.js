const WebSocket = require("ws");

class CustomEvent extends Event {
    constructor(message, data) {
        super(message, data)
        this.detail = data.detail
    }
}

class GrabberSocket {
    constructor(url) {
        this.url = url.startsWith("http") ? "ws" + url.substring(4) : url;
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
        ws.onerror = function (event) {
            console.error(`WebSocket ${_this.url} ${event.type}: ${event.message}`);
        }
        ws.onclose = function () {
            if (_this.isClosed) {
                return;
            }
            setTimeout(() => _this.connect(), 3000);
        }
        this.ws = ws;
    }

    emit(event, payload) {
        const data = {...payload, "event": event};
        if (this.ws.readyState === this.ws.OPEN) {
            this.ws.send(JSON.stringify(data));
        } else {
            this.messageQueue.push(data);
        }
    }

    on(event, callback) {
        this.target.addEventListener(event, e => callback(e.detail));
    }

    close() {
        this.isClosed = true;
        this.ws?.close();
    }
}

module.exports.GrabberSocket = GrabberSocket;
module.exports.CustomEvent = CustomEvent;
