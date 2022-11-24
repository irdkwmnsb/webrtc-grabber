const { ipcRenderer } = require('electron')

let streams = {};
const pcs = new Map();

setInterval(() => {
    ipcRenderer.invoke('status:connections', { connectionsCount: pcs.size, streamTypes: Object.keys(streams) });
}, 3000);

function handleStream(stream) {
    // const video = document.querySelector('video#preview')
    // video.srcObject = stream
    // video.onloadedmetadata = () => video.play()
}

ipcRenderer.on('source:update', async (_, { screenSourceId }) => {
    const detectedStreams = {};

    detectedStreams["webcam"] = await navigator.mediaDevices.getUserMedia({
        video: { width: 1280, height: 720 },
    });

    if (screenSourceId) {
        detectedStreams["desktop"] = await navigator.mediaDevices.getUserMedia({
            audio: false,
            video: {
                mandatory: {
                    chromeMediaSource: 'desktop',
                    chromeMediaSourceId: screenSourceId,
                    minWidth: 1920,
                    minHeight: 1080,
                }
            }
        }) ?? undefined;
    }

    streams = detectedStreams;
})

ipcRenderer.on('offer', async (_, playerId, offer, streamType, configuration) => {
    console.log(`create new peer connection for ${playerId}`);
    pcs.set(playerId, new RTCPeerConnection(configuration));
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
