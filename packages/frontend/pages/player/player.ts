import {GrabberPlayerClient} from "webrtc-grabber-sdk/lib/grabber_player";

const extractPlayerArguments = () => {
    const params = new URLSearchParams(window.location.search);
    if (!params.has("peerName")) {
        alert("Invalid parameters. No peerName argument");
    }
    return {
        peerName: params.get("peerName"),
        streamType: params.get("streamType"),
        credential: params.get("credential")
    };
}
const connectionArgs = extractPlayerArguments();

const client = new GrabberPlayerClient("play");
client.authorize(connectionArgs.credential);
client.on("initialized", () => {
    client.connect({peerName: connectionArgs.peerName}, connectionArgs.streamType, (track) => {
        console.log("got video track");
        const remoteVideo = document.getElementById("video") as HTMLVideoElement;
        remoteVideo.srcObject = track;
        remoteVideo.play();
        console.log(`pc2 received remote stream (${connectionArgs.peerName}) `, track);
    });
});

window.addEventListener("close", () => client.close());
