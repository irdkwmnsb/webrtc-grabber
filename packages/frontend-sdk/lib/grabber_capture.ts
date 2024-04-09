import {GrabberSocket} from "./sockets";
import {IOfferReceiveResponder, StreamType} from "./api";
import { TypedEmitter } from "tiny-typed-emitter";

export class GrabberCaptureClient { // FIXME inherit from the other client
    private peerName: string;
    public target: EventTarget; // FIXME: make this private and make proper on function
    private socket: GrabberSocket<any>;
    private offerReceiveResponder?: IOfferReceiveResponder;
    private emitter: TypedEmitter;

    constructor(peerName: string, signallingUrl?: string) {
        this.emitter = new TypedEmitter();
        this.peerName = peerName;
        this.target = new EventTarget();
        this.emitter.emit("hi", {});
        // this.target.dispatchEvent(new CustomEvent('hi', {}));

        const socketPath = (signallingUrl ?? "") + "/ws/peers/" + peerName;

        this.socket = new GrabberSocket(socketPath);
        this.socket.on("connect", async () => {
            console.log("init socket", socketPath);
        });

        const init_peer_handle = ({initPeer: {pcConfig, pingInterval}}: any) => { // FIXME: remove once proper types are implemented
            this.emitter.emit("init_peer", {detail: {pcConfig, pingInterval}});
            // this.target.dispatchEvent(new CustomEvent('init_peer', {detail: {pcConfig, pingInterval}}));
        };
        const offer_handle = async ({offer: {peerId, offer, streamType}}: any) => { // FIXME: remove once proper types are implemented
            // if (this.offerReceiveResponder === undefined) {
            //     console.warn("Got an incomming call, even though onOfferReceived is not called.");
            //     return;
            // }
            // await this.offerReceiveResponder(peerId, offer, streamType);
            this.emitter.emit("offer", {detail: {playerId: peerId, offer, streamType}});
            // this.target.dispatchEvent(new CustomEvent('offer', {detail: {playerId: peerId, offer, streamType}}));
        }
        const player_ice_handle = async ({ice: {peerId, candidate}}: any) => { // FIXME: remove once proper types are implemented
            this.emitter.emit("player_ice", {detail: {peerId, candidate}});
            // this.target.dispatchEvent(new CustomEvent('player_ice', {detail: {peerId, candidate}}));
        }

        this.socket.on("init_peer", init_peer_handle);
        this.socket.on("offer", offer_handle);
        this.socket.on("player_ice", player_ice_handle);
    }

    onOfferReceived(getAnswer: IOfferReceiveResponder) {
        this.offerReceiveResponder = getAnswer;
    }

    sendPing(connectionsCount: number, streamTypes: string[]) {
        this.socket.emit("ping", {ping: {connectionsCount: connectionsCount, streamTypes: streamTypes}});
    }

    sendOfferAnswer(playerId: string, answer: string) {
        this.socket.emit("offer_answer", {offerAnswer: {peerId: playerId, answer}});
    }

    sendGrabberICE(peerId: string, candidate: string) {
        this.socket.emit("grabber_ice", {ice: {peerId, candidate}});
    }
}
