import {GrabberSocket} from "./sockets";

export class GrabberCaptureClient { // FIXME inherit from the other client
    private peerName: string;
    public target: EventTarget; // FIXME: make this private and make proper on function
    private socket: GrabberSocket<any>;
    constructor(peerName: string, signallingUrl?: string) {
        this.peerName = peerName;
        this.target = new EventTarget();
        this.target.dispatchEvent(new CustomEvent('hi', {}));

        const socketPath = (signallingUrl ?? "") + "/ws/peers/" + peerName;

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
