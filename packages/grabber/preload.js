const {ipcRenderer} = require('electron')

let stream;
const pcs = new Map();

setInterval(() => {
    ipcRenderer.invoke('status:connections', { connectionsCount: pcs.size });
}, 3000);

function handleStream(stream) {
    // const video = document.querySelector('video#preview')
    // video.srcObject = stream
    // video.onloadedmetadata = () => video.play()
}

ipcRenderer.on('source:set', async (_, sourceId) => {
    console.log(sourceId);
    stream = await navigator.mediaDevices.getUserMedia({
        audio: false,
        video: {
            mandatory: {
                chromeMediaSource: 'desktop',
                chromeMediaSourceId: sourceId,
                minWidth: 1920,
                minHeight: 1080,
            }
        }
    })
    handleStream(stream);
})

ipcRenderer.on('offer', async (_, playerId, offer, configuration) => {
    console.log(`create new peer connection for ${playerId}`);
    pcs.set(playerId, new RTCPeerConnection(configuration));
    const pc = pcs.get(playerId);

    stream.getTracks().forEach(track => {
        console.log("added track: ", track);
        pc.addTrack(track, stream);
    });

    pc.addEventListener("icecandidate", (event) => {
        console.log(`send ice for player ${playerId}`);
        ipcRenderer.invoke('grabber_ice', playerId, JSON.stringify(event.candidate));
    })

    pc.addEventListener('connectionstatechange', ({target: connection}) => {
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

    await ipcRenderer.invoke('offer_answer', playerId, JSON.stringify(answer));
    console.log(`send offer_answer for ${playerId}`);
});

ipcRenderer.on('player_ice', (_, playerId, candidate) => {
    pcs.get(playerId).addIceCandidate(candidate)
        .then(() => console.log(`add player_ice from ${playerId}`));
});
