import {GrabberSocket} from "./sockets";
import { TypedEmitter } from "tiny-typed-emitter"

type PeerInfo = any;
type GrabberPlayerClientEventTypes = "auth:request" | "auth:failed" | "initialized" | "peers"
export class GrabberPlayerClient {
    private ws: GrabberSocket<any>; // FIXME use more strict typing. Declare the protocol in typescript. Later.
    private pc?: RTCPeerConnection;
    private peerConnectionConfig?: RTCConfiguration
    private _peersStatus?: any[]; // Why is this stored in Client?
    private emitter: TypedEmitter;

    get peersStatus() { // FIXME: remove once proper types are implemented
        return this._peersStatus
    }
    private _participantsStatus?: any[]; // Why is this stored in Client?
    get participantsStatus() {
        return this._participantsStatus
    }
    // private target: EventTarget;
    constructor(mode: "play" | "admin" = "admin", url?: string) {
        // this.target = new EventTarget();
        this.emitter = new TypedEmitter();

        this.ws = new GrabberSocket((url ?? "") + "/ws/player/" + (mode === "play" ? "play" : "admin"));
        this._setupWS();
    }

    _setupWS() {
        this.ws.on("auth:request", () => {
            this.emitter.emit("auth:request", {});
            // this.target.dispatchEvent(new CustomEvent("auth:request", {}));
        });
        this.ws.on("auth:failed", () => {
            this.ws.close();
            this.emitter.emit("auth:failed", {});
            // this.target.dispatchEvent(new CustomEvent("auth:failed", {}));
        });

        this.ws.on("init_peer", ({ initPeer: { pcConfig } }) => {
            this.peerConnectionConfig = pcConfig;
            console.debug("WebRTCGrabber: connection initialized");
            this.emitter.emit("initialized", {});
            // this.target.dispatchEvent(new CustomEvent("initialized", {}));
        });

        this.ws.on("peers", ({ peersStatus, participantsStatus }) => {
            this._peersStatus = peersStatus ?? [];
            this._participantsStatus = participantsStatus ?? [];
            this.emitter.emit("peers", { detail: [ peersStatus, participantsStatus ]});
            // this.target.dispatchEvent(new CustomEvent("peers", { detail: [ peersStatus, participantsStatus ] }));
        });

        this.ws.on("offer_answer", async ({ offerAnswer: { peerId, answer } }) => {
            console.debug(`WebRTCGrabber: got offer_answer from ${peerId}`);
            await this?.pc?.setRemoteDescription(answer);
        });

        this.ws.on("grabber_ice", async ({ ice: { peerId, candidate } }) => {
            console.debug(`WebRTCGrabber: got grabber_ice from ${peerId}`);
            await this?.pc?.addIceCandidate(candidate);
        });
    }

    formatPeerInfo(peerInfo: PeerInfo) {
        return peerInfo.peerId ?? (`{${peerInfo.peerName}}`);
    }

    authorize(credential: string | null) {
        this.ws.emit("auth", { playerAuth: { credential: credential } });
    }

    connect(peerInfo: PeerInfo, streamType: any, onVideoTrack: (arg0: MediaStream, arg1: any, arg2: any) => void) {
        const pc = new RTCPeerConnection(this.peerConnectionConfig);
        pc.addTransceiver("video");
        pc.addTransceiver("audio");

        pc.addEventListener("track", (e) => {
            console.debug("WebRTCGrabber: got track");
            if (e.track.kind === "video" && e.streams.length > 0) {
                onVideoTrack(e.streams[0], peerInfo, streamType);
            }
        });

        pc.addEventListener("icecandidate", (event) => {
            console.debug(`WebRTCGrabber: sending ice to ${this.formatPeerInfo(peerInfo)}`);
            this.ws.emit("player_ice", { ice: { ...peerInfo, candidate: event.candidate } });
        });

        this.close();
        this.pc = pc;

        pc.createOffer().then(offer => {
            pc.setLocalDescription(offer);
            this.ws.emit("offer", { offer: { ...peerInfo, offer, streamType } });
            console.debug(`WebRTCGrabber: sending offer to ${this.formatPeerInfo(peerInfo)} ${streamType} ...`);
        });
    }

    on(eventName: GrabberPlayerClientEventTypes, callback: (arg0: any) => void) {
        this.emitter.on(eventName, ({detail}: any) => callback(detail));
        // this.target.addEventListener(eventName, ({detail}: any) => callback(detail));
    }

    close() {
        this.pc?.close();
        this.pc = undefined;
    }
}
