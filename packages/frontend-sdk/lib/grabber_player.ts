import {GrabberSocket} from "./sockets";

type PeerInfo = any;
type GrabberPlayerClientEventTypes = "auth:request" | "auth:failed" | "initialized" | "peers"
export class GrabberPlayerClient {
    private ws: GrabberSocket<any>; // FIXME use more strict typing. Declare the protocol in typescript. Later.
    private pc?: RTCPeerConnection;
    private peerConnectionConfig?: RTCConfiguration
    private _peersStatus?: any[]; // Why is this stored in Client?
    get peersStatus() { // FIXME: remove once proper types are implemented
        return this._peersStatus
    }
    private _participantsStatus?: any[]; // Why is this stored in Client?
    get participantsStatus() {
        return this._participantsStatus
    }
    private target: EventTarget;
    constructor(mode: "play" | "admin" = "admin", url?: string) {
        this.target = new EventTarget();

        this.ws = new GrabberSocket((url ?? "") + "/ws/player/" + (mode === "play" ? "play" : "admin"));
        this._setupWS();
    }

    _setupWS() {
        const _client = this;
        this.ws.on("auth:request", () => {
            _client.target.dispatchEvent(new CustomEvent("auth:request", {}));
        });
        _client.ws.on("auth:failed", () => {
            _client.ws.close();
            _client.target.dispatchEvent(new CustomEvent("auth:failed", {}));
        });

        _client.ws.on("init_peer", ({ initPeer: { pcConfig } }) => {
            _client.peerConnectionConfig = pcConfig;
            console.debug("WebRTCGrabber: connection initialized");
            _client.target.dispatchEvent(new CustomEvent("initialized", {}));
        });

        _client.ws.on("peers", ({ peersStatus, participantsStatus }) => {
            _client._peersStatus = peersStatus ?? [];
            _client._participantsStatus = participantsStatus ?? [];
            _client.target.dispatchEvent(new CustomEvent("peers", { detail: [ peersStatus, participantsStatus ] }));
        });

        _client.ws.on("offer_answer", async ({ offerAnswer: { peerId, answer } }) => {
            console.debug(`WebRTCGrabber: got offer_answer from ${peerId}`);
            await _client?.pc?.setRemoteDescription(answer);
        });

        _client.ws.on("grabber_ice", async ({ ice: { peerId, candidate } }) => {
            console.debug(`WebRTCGrabber: got grabber_ice from ${peerId}`);
            await _client?.pc?.addIceCandidate(candidate);
        });
    }

    formatPeerInfo(peerInfo: PeerInfo) {
        return peerInfo.peerId ?? (`{${peerInfo.peerName}}`);
    }

    authorize(credential: string | null) {
        this.ws.emit("auth", { playerAuth: { credential: credential } });
    }

    connect(peerInfo: PeerInfo, streamType: any, onVideoTrack: (arg0: MediaStream, arg1: any, arg2: any) => void) {
        const _client = this;

        const pc = new RTCPeerConnection(_client.peerConnectionConfig);
        pc.addTransceiver("video");
        pc.addTransceiver("audio");

        pc.addEventListener("track", (e) => {
            console.debug("WebRTCGrabber: got track");
            if (e.track.kind === "video" && e.streams.length > 0) {
                onVideoTrack(e.streams[0], peerInfo, streamType);
            }
        });

        pc.addEventListener("icecandidate", (event) => {
            console.debug(`WebRTCGrabber: sending ice to ${_client.formatPeerInfo(peerInfo)}`);
            _client.ws.emit("player_ice", { ice: { ...peerInfo, candidate: event.candidate } });
        });

        _client.close();
        _client.pc = pc;

        pc.createOffer().then(offer => {
            pc.setLocalDescription(offer);
            _client.ws.emit("offer", { offer: { ...peerInfo, offer, streamType } });
            console.debug(`WebRTCGrabber: sending offer to ${_client.formatPeerInfo(peerInfo)} ${streamType} ...`);
        });
    }

    on(eventName: GrabberPlayerClientEventTypes, callback: (arg0: any) => void) {
        this.target.addEventListener(eventName, ({detail}: any) => callback(detail));
    }

    close() {
        this.pc?.close();
        this.pc = undefined;
    }
}
