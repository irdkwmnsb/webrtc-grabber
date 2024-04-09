import {WebSocket} from "ws";
import EventEmitter from "events";
import TypedEmitter from "typed-emitter";

type GrabberSocketEvents<T> = {
    "auth:request": (payload: T) => void;
    "auth:failed": (payload: T) => void;
    "auth:black_listed": (payload: T) => void;
    "init_peer": (payload: T) => void;
    "peers": (payload: T) => void;
    "offer_answer": (payload: T) => Promise<void>;
    "grabber_ice": (payload: T) => Promise<void>;
    "connect": (payload: T) => Promise<void>;
    "offer_handle": (payload: T) => Promise<void>;
    "player_ice": (payload: T) => Promise<void>; 
};

export class GrabberSocket<T> {
    private readonly url: string;
    private messageQueue: any[];
    private isClosed: boolean;
    private ws?: WebSocket;
    private messageEmitter: TypedEmitter<GrabberSocketEvents<T>>;
    // private target: EventTarget;


    constructor(url: string) {
        if (!url.startsWith("ws")) { 
            if (typeof window !== "undefined") {
                url = (window.location.protocol === "http:" ? "ws:" : "wss:") + window.location.host + url;
            } else {
                console.error("Please use ws or wss in signalling url!");
                throw DOMException;
            }
        }
        // this.target = new EventTarget();
        this.url = url;
        this.messageEmitter = new EventEmitter();
        this.messageQueue = [];
        this.isClosed = false;
        this.connect();
    }

    connect() {
        console.log("connecting....");
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
            console.log(payload);
            _this.messageEmitter.emit(payload.event, {detail: payload});
            // _this.target.dispatchEvent(new CustomEvent(payload.event, {detail: payload}));
        }
        ws.onclose = function () {
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

    on(event, callback: (payload: T) => void) {
        this.messageEmitter.on(event, callback);
        // this.target.addEventListener(event, ({detail}: any) => callback(detail));
    }

    close() {
        this.isClosed = true;
        this.ws?.close();
    }
}
