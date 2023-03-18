import {GrabberPlayerClient} from "../../lib/grabber_player";
import {requireAuth} from "../../lib/util";

let curGrabberId = null;
let selectedParticipantName: string | null = null;

const peerState = (peer: any) => // FIXME: remove once proper types are implemented
    peer.lastPing ? ((new Date().getTime() - new Date(peer.lastPing).getTime()) < 15000 ? "active" : "disconnected") : "offline";

const client = new GrabberPlayerClient();
client.on("auth:request", () => {
    client.authorize(requireAuth());
});
client.on("auth:failed", () => {
    localStorage.removeItem("adminCredentials");
    alert("Forbidden. Authorization failed");
});

const renderParticipants = (peers?: any[], participantsStatus?: any[]) => { // FIXME: remove once proper types are implemented
    const allPeersAnchor = document.getElementById("peers")!;
    allPeersAnchor.innerHTML = "";
    peers && peers.forEach((peer) => {
        allPeersAnchor.innerHTML += "";
        allPeersAnchor.appendChild(renderPeerInfo(peer));
    });

    const participantsStatusAnchor = document.getElementById("participants")!;
    participantsStatusAnchor.innerHTML = "";
    participantsStatus && participantsStatus.forEach((peer) => {
        const pStatus = document.createElement('div');
        pStatus.className = "participantStatus " + peerState(peer);
        pStatus.innerText = peer.name;
        if (peer.connectionsCount > 0) {
            pStatus.className += " viewed";
            pStatus.innerText += ` [${peer.connectionsCount}]`
        }
        if (peer.streamTypes) {
            pStatus.innerText += " " + peer.streamTypes.map((t: string) => t.slice(0, 1).toUpperCase()).join(""); // FIXME: remove once proper types are implemented
        }
        if (peer.name === selectedParticipantName) {
            renderSelectedPeer(peer);
            pStatus.classList.add("selected");
        }
        pStatus.onclick = () => {
            if (selectedParticipantName === peer.name) {
                selectedParticipantName = null;
                pStatus.classList.remove("selected");
                renderSelectedPeer(null);
            } else {
                selectedParticipantName = peer.name;
                pStatus.classList.add("selected");
                renderSelectedPeer(peer);
            }
            renderParticipants(client.peersStatus, client.participantsStatus);
        };
        pStatus.style.cursor = "pointer";
        participantsStatusAnchor.appendChild(pStatus);
    });
}
const renderSelectedPeer = (peer: any) => { // FIXME: remove once proper types are implemented
    const anchor = document.getElementById("selectedParticipant")!;
    anchor.innerText = "";
    if (!peer) {
        return;
    }
    anchor.appendChild(renderPeerInfo(peer, true));
}
client.on("peers", ({detail: [peersStatus, participantsStatus]}) => {
    renderParticipants(peersStatus, participantsStatus);
});

const onPeerTrack = (track: any, peerId: any, streamType: any) => {
    console.log("got video track");
    const remoteVideo = document.getElementById("video")! as HTMLVideoElement;
    if (remoteVideo.srcObject !== track) {
        remoteVideo.srcObject = null;
        remoteVideo.srcObject = track;
        remoteVideo.play();
        console.log(`pc2 received remote stream (peerId ${peerId}) `, track);
    }
};

function renderPeerInfo(peer: any, isSelected = false) {
    const lastPingStr = peer.lastPing ? (new Date(peer.lastPing)).toLocaleString() : "never";
    const pStatus = document.createElement('div');
    if (isSelected) {
        pStatus.className += "peerInfo participantStatus " + peerState(peer);
    } else {
        pStatus.className += "peerInfo " + peerState(peer);
    }
    pStatus.innerText = `${peer.id ?? "???"} [${peer.name}] last ping: ${lastPingStr}`;

    const connectStreamButton = (streamType: any, isSpirit = false) => {
        const connectButton = document.createElement('button');
        connectButton.innerText = (isSpirit && "?" || "") + streamType;
        connectButton.onclick = () => client.connect({peerId: peer.id}, streamType, onPeerTrack);
        pStatus.appendChild(connectButton);
        const playerLink = document.createElement('a');
        playerLink.innerText = "" + (isSpirit && "?" || "") + streamType;
        playerLink.href = `${window.location}player?` +
            `peerName=${peer.name}&streamType=${streamType}&credential=${localStorage.getItem("adminCredentials")}`
        pStatus.appendChild(playerLink);
    }
    if (peer.streamTypes && peer.streamTypes.length > 0) {
        for (const streamType of peer.streamTypes) {
            connectStreamButton(streamType);
        }
    } else {
        connectStreamButton("desktop", true);
        connectStreamButton("webcam", true);
    }
    return pStatus;
}

window.addEventListener("close", () => client.close());
