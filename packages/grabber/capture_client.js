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
        const player_ice_handle = async function ({ice: {peerId, candidate}}) {
            this.target.dispatchEvent(new CustomEvent('player_ice', {detail: {peerId, candidate}}));
        }
        const record_start_handle = async function ({recordStart: {recordId, timeout}}) {
            this.target.dispatchEvent(new CustomEvent('record_start', {detail: {recordId, timeout}}));
        }
        const record_stop_handle = async function ({recordStop: {recordId}}) {
            this.target.dispatchEvent(new CustomEvent('record_stop', {detail: {recordId}}));
        }
        const players_disconnect_handle = async function ({event}) {
            this.target.dispatchEvent(new CustomEvent("players_disconnect", {detail: {recordId: "recordId", timeout: 10}}));
        }

        this.socket.on("init_peer", init_peer_handle.bind(this));
        this.socket.on("offer", offer_handle.bind(this));
        this.socket.on("player_ice", player_ice_handle.bind(this));
        this.socket.on("record_start", record_start_handle.bind(this));
        this.socket.on("record_stop", record_stop_handle.bind(this));
        this.socket.on("players_disconnect", players_disconnect_handle.bind(this));
    }

    send_ping(connectionsCount, streamTypes, currentRecordId) {
        this.socket.emit("ping", {ping: {connectionsCount: connectionsCount, streamTypes: streamTypes, currentRecordId: currentRecordId}});
    }

    send_offer_answer(playerId, answer) {
        this.socket.emit("offer_answer", {offerAnswer: {peerId: playerId, answer}});
    }

    send_grabber_ice(peerId, candidate) {
        this.socket.emit("grabber_ice", {ice: {peerId, candidate}});
    }
}

module.exports.GrabberCaptureClient = GrabberCaptureClient;
