import { GrabberSocket } from "webrtc-grabber-sdk/lib/sockets";

const webcamConstraint = {aspectRatio: 16 / 9};
const webcamAudioConstraint = true;
const desktopConstraint = {
    video: {
        displaySurface: "window"
    }
};

class GrabberCaptureClient { // FIXME inherit from the other client
    private peerName: string;
    public target: EventTarget; // FIXME: make this private and make proper on function
    private socket: GrabberSocket<any>;
    constructor(peerName: string) {
        this.peerName = peerName;
        this.target = new EventTarget();

        const socketPath = "/ws/peers/" + peerName;

        this.socket = new GrabberSocket(socketPath);
        this.socket.on("connect", async () => {
            console.log("init socket", socketPath);
        });

        const init_peer_handle = ({initPeer: {pcConfig, pingInterval}}: any) => { // FIXME: remove once proper types are implemented
            this.target.dispatchEvent(new CustomEvent('init_peer', {detail: {pcConfig, pingInterval}}));
        };
        const offer_handle = async ({offer: {peerId, offer, streamType}}: any) => { // FIXME: remove once proper types are implemented
            this.target.dispatchEvent(new CustomEvent('offer', {detail: {playerId: peerId, offer, streamType}}));
        }
        const player_ice_handle = async ({ice: {peerId, candidate}}: any) => { // FIXME: remove once proper types are implemented
            this.target.dispatchEvent(new CustomEvent('player_ice', {detail: {peerId, candidate}}));
        }

        this.socket.on("init_peer", init_peer_handle);
        this.socket.on("offer", offer_handle);
        this.socket.on("player_ice", player_ice_handle);
    }

    send_ping(connectionsCount: number, streamTypes: string[]) {
        this.socket.emit("ping", {ping: {connectionsCount: connectionsCount, streamTypes: streamTypes}});
    }

    send_offer_answer(playerId: string, answer: string) {
        this.socket.emit("offer_answer", {offerAnswer: {peerId: playerId, answer}});
    }

    send_grabber_ice(peerId: string, candidate: string) {
        this.socket.emit("grabber_ice", {ice: {peerId, candidate}});
    }
}

const extractArguments = () => {
    const params = new URLSearchParams(window.location.search);
    if (!params.has("peerName")) {
        alert("Invalid parameters. No peerName argument");
        throw new Error("Invalid parameters. No peerName argument");
    }
    return {peerName: params.get("peerName")!};
}
const connectionArgs = extractArguments();

const detectStreams = async () => {
    const detectedStreams: Record<string, MediaStream> = {};

    const webcamStream = await navigator.mediaDevices.getUserMedia({
        video: webcamConstraint,
        audio: webcamAudioConstraint,
    }).catch(() => undefined);
    if (webcamStream) {
        detectedStreams["webcam"] = webcamStream;
    }

    if (desktopConstraint) {
        const desktopStream = await navigator.mediaDevices.getDisplayMedia(desktopConstraint).catch(() => undefined);
        if (desktopStream) {
            detectedStreams["desktop"] = desktopStream;
        }
    }

    return detectedStreams;
}

type State = "inactive" | "detecting" | "connecting" | "active"

let currentState: State = "inactive";
const updateState = (newState: State) => {
    currentState = newState;
    const captureButton = document.getElementById("captureButton")!;
    captureButton.className = newState;
    if (newState === "connecting") {
        captureButton.innerText = "connecting ...";
    } else if (newState === "active") {
        captureButton.innerText = "OK";
    }
}

const capture = async () => {
    if (currentState !== "inactive") return;
    updateState("detecting");

    const streams = await detectStreams();
    const pcs = new Map();
    let peerConnectionConfig: RTCConfiguration | undefined = undefined;
    let pingTimerId: number | undefined = undefined;

    updateState("connecting");
    const client = new GrabberCaptureClient(connectionArgs.peerName);
    client.target.addEventListener("init_peer", async ({detail: {pcConfig, pingInterval}}: any) => {
        peerConnectionConfig = pcConfig;
        pingInterval = (pingInterval ?? 3000);
        if (pingTimerId) {
            clearInterval(pingTimerId);
        }
        pingTimerId = setInterval((() => {
            console.log("ping");
            client.send_ping(pcs.size, Object.keys(streams));
        }) as TimerHandler, pingInterval);
        console.log(`init peer (pingInterval = ${pingInterval})`);
        updateState("active");
    });

    client.target.addEventListener("offer", async ({detail: {playerId, offer, streamType}}: any) => {
        console.log(`create new peer connection for ${playerId}`);
        pcs.set(playerId, new RTCPeerConnection(peerConnectionConfig));
        const pc = pcs.get(playerId);

        streamType = streamType ?? "desktop";
        const stream = streams[streamType];
        if (stream) {
            stream.getTracks().forEach(track => {
                console.log("added track: ", track);
                pc.addTrack(track, stream);
            });
        } else {
            console.warn(`No surch ${streamType} as captured stream`);
        }

        pc.addEventListener("icecandidate", (event: any) => { // FIXME: remove once proper types are implemented
            console.log(`send ice for player ${playerId}`);
            client.send_grabber_ice(playerId, event.candidate);
        })

        pc.addEventListener('connectionstatechange', ({target: connection}: any) => { // FIXME: remove once proper types are implemented
            console.log(`change player ${playerId} connection state ${connection.connectionState}`);
            if (connection.connectionState === "failed") {
                connection.close();
                pcs.delete(playerId);
                console.log(`close connection for ${playerId}`);
            }
        });

        await pc.setRemoteDescription(offer);
        const answer = await pc.createAnswer();
        await pc.setLocalDescription(answer);

        client.send_offer_answer(playerId, answer);
        console.log(`send offer_answer for ${playerId}`);
    });

    client.target.addEventListener('player_ice', async ({detail: {peerId, candidate}}: any) => { // FIXME: remove once proper types are implemented
        pcs.get(peerId).addIceCandidate(candidate)
            .then(() => console.log(`add player_ice from ${peerId}`));
    });
};

document.getElementById("captureButton")!.addEventListener("click", capture);
