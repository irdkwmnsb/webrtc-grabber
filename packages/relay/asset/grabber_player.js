class GrabberPlayerClient {
    constructor(mode) {
        this.pc = null;
        this.peerConnectionConfig = null;
        this.peersStatus = null;
        this.participantsStatus = null;

        this.target = new EventTarget();

        this.ws = new GrabberSocket("/ws/player/" + (mode === "play" ? "play" : "admin"));
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

        _client.ws.on("init_peer", ({initPeer: {pcConfig}}) => {
            _client.peerConnectionConfig = pcConfig;
            console.log("Grabber connection initialized");
        });

        _client.ws.on("peers", ({peersStatus, participantsStatus}) => {
            _client.peersStatus = peersStatus ?? [];
            _client.participantsStatus = participantsStatus ?? [];
            _client.target.dispatchEvent(new CustomEvent("peers", {detail: [peersStatus, participantsStatus]}));
        });

        _client.ws.on("offer_answer", async ({offerAnswer: {peerId, answer}}) => {
            console.log(`got offer_answer from ${peerId}`);
            await _client?.pc.setRemoteDescription(answer);
        });

        _client.ws.on("grabber_ice", async ({ice: {peerId, candidate}}) => {
            console.log(`got grabber_ice from ${peerId}`);
            await _client?.pc.addIceCandidate(candidate);
        });
    }

    formatPeerInfo(peerInfo) {
        return peerInfo.peerId ?? (`{${peerInfo.peerName}}`);
    }

    authorize(credential) {
        this.ws.emit("auth", {playerAuth: {credential: credential}});
    }

    connect(peerInfo, streamType, onVideoTrack) {
        const _client = this;

        const pc = new RTCPeerConnection(_client.peerConnectionConfig);
        pc.addTransceiver("video");
        pc.addTransceiver("audio");

        pc.addEventListener("track", (e) => {
            console.log("got track");
            if (e.track.kind === "video" && e.streams.length > 0) {
                console.log('pc2 received remote stream', e.streams);
                onVideoTrack(e.streams[0], peerInfo, streamType);
            }
        });

        pc.addEventListener("icecandidate", (event) => {
            console.log(`sending ice to ${_client.formatPeerInfo(peerInfo)}`);
            _client.ws.emit("player_ice", {ice: {...peerInfo, candidate: event.candidate}});
        });

        _client.close();
        _client.pc = pc;

        pc.createOffer().then(offer => {
            pc.setLocalDescription(offer);
            _client.ws.emit("offer", {offer: {...peerInfo, offer, streamType}});
            console.log(`sending offer to ${_client.formatPeerInfo(peerInfo)} ${streamType} ...`);
        });
    }

    close() {
        if (["connecting", "connected", undefined].indexOf(this.pc?.connectionState) !== -1) {
            this.pc?.close();
        }
    }
}
