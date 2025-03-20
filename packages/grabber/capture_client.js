const {GrabberSocket, CustomEvent} = require("./sockets.js");


class GrabberCaptureClient {
    constructor(peerName, signallingUrl) {
        this.peerName = peerName;
        this.target = new EventTarget();
        this.target.dispatchEvent(new CustomEvent('hi', {}));

        const socketPath = (signallingUrl ?? "") + "/ws/peers/" + peerName;

        this.socket = new GrabberSocket(socketPath);
        this.socket.on("connect", async () => {
            console.log("init socket", socketPath);
        });

        const init_peer_handle = function ({initPeer: {pcConfig, pingInterval}}) {
            this.target.dispatchEvent(new CustomEvent('init_peer', {detail: {pcConfig, pingInterval}}));
        };
        const offer_handle = async function ({offer: {peerId, offer, streamType}}) {
            this.target.dispatchEvent(new CustomEvent('offer', {detail: {playerId: peerId, offer, streamType}}));
        }
        const player_ice_handle = async function ({ice: {peerId, candidate, streamType}}) {
            this.target.dispatchEvent(new CustomEvent('player_ice', {detail: {peerId, candidate, streamType}}));
        }

        this.socket.on("init_peer", init_peer_handle.bind(this));
        this.socket.on("offer", offer_handle.bind(this));
        this.socket.on("player_ice", player_ice_handle.bind(this));
    }

    send_ping(connectionsCount, streamTypes) {
        this.socket.emit("ping", {ping: {connectionsCount: connectionsCount, streamTypes: streamTypes}});
    }

    send_offer_answer(playerId, answer, streamType) {
        this.socket.emit("offer_answer", {offerAnswer: {peerId: playerId, streamType: streamType, answer}});
    }

    send_grabber_ice(peerId, candidate, streamType) {
        this.socket.emit("grabber_ice", {ice: {peerId, streamType: streamType, candidate}});
    }
}

module.exports.GrabberCaptureClient = GrabberCaptureClient;
