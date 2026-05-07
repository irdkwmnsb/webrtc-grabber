const {GrabberSocket, CustomEvent} = require("./sockets.js");


function uploadRecord(fileBlob, fileName, signallingUrl, peerName, uploadToken) {
    const formData = new FormData()
    formData.append('file', fileBlob, fileName)

    const headers = uploadToken ? {'X-Upload-Token': uploadToken} : {};
    return fetch(`${signallingUrl}/api/agent/${peerName}/record_upload`,
        {
            method: "POST",
            body: formData,
            headers,
        });
}

class GrabberCaptureClient {
    constructor(peerName, signallingUrl) {
        this.peerName = peerName;
        this.target = new EventTarget();
        this.target.dispatchEvent(new CustomEvent('hi', {}));
        this.signallingUrl = (signallingUrl ?? "");

        const socketPath = this.signallingUrl + "/ws/peers/" + peerName;

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
        const record_start_handle = async function ({recordStart: {recordId, timeout, uploadToken}}) {
            this.target.dispatchEvent(new CustomEvent('record_start', {detail: {recordId, timeout, uploadToken}}));
        }
        const record_stop_handle = async function ({recordStop: {recordId}}) {
            this.target.dispatchEvent(new CustomEvent('record_stop', {detail: {recordId}}));
        }
        const record_upload_handle = async function ({recordUpload: {recordId}}) {
            this.target.dispatchEvent(new CustomEvent('record_upload', {detail: {recordId}}));
        }
        const players_disconnect_handle = async function ({event}) {
            this.target.dispatchEvent(new CustomEvent("players_disconnect", {
                detail: {
                    recordId: "recordId",
                    timeout: 10
                }
            }));
        }
        const proctoring_start_handle = function ({proctoringStart}) {
            this.target.dispatchEvent(new CustomEvent('proctoring_start', {detail: proctoringStart}));
        };
        const proctoring_pause_handle = function () {
            this.target.dispatchEvent(new CustomEvent('proctoring_pause'));
        };
        const proctoring_resume_handle = function ({proctoringResume}) {
            this.target.dispatchEvent(new CustomEvent('proctoring_resume', {detail: proctoringResume}));
        };
        const proctoring_stop_handle = function () {
            this.target.dispatchEvent(new CustomEvent('proctoring_stop'));
        };

        this.socket.on("init_peer", init_peer_handle.bind(this));
        this.socket.on("offer", offer_handle.bind(this));
        this.socket.on("player_ice", player_ice_handle.bind(this));
        this.socket.on("record_start", record_start_handle.bind(this));
        this.socket.on("record_stop", record_stop_handle.bind(this));
        this.socket.on("record_upload", record_upload_handle.bind(this));
        this.socket.on("players_disconnect", players_disconnect_handle.bind(this));
        this.socket.on("proctoring_start",  proctoring_start_handle.bind(this));
        this.socket.on("proctoring_pause",  proctoring_pause_handle.bind(this));
        this.socket.on("proctoring_resume", proctoring_resume_handle.bind(this));
        this.socket.on("proctoring_stop",   proctoring_stop_handle.bind(this));
    }

    send_ping(connectionsCount, streamTypes, currentRecordId, proctoringActiveStreams) {
        this.socket.emit("ping", {
            ping: {
                connectionsCount: connectionsCount,
                streamTypes: streamTypes,
                currentRecordId: currentRecordId,
                proctoringActiveStreams: proctoringActiveStreams || [],
            }
        });
    }

    send_offer_answer(playerId, answer) {
        this.socket.emit("offer_answer", {offerAnswer: {peerId: playerId, answer}});
    }

    send_grabber_ice(peerId, candidate) {
        this.socket.emit("grabber_ice", {ice: {peerId, candidate}});
    }

    record_upload(fileName, fileBlob, uploadToken) {
        return uploadRecord(fileBlob, fileName, this.signallingUrl, this.peerName, uploadToken);
    }
}

module.exports.GrabberCaptureClient = GrabberCaptureClient;
