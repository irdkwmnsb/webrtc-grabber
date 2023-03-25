const { ipcRenderer } = require('electron')

let streams = {};
const pcs = new Map();

setInterval(() => {
    ipcRenderer.invoke('status:connections', { connectionsCount: pcs.size, streamTypes: Object.keys(streams) });
}, 3000);

ipcRenderer.on('source:update', async (_, { webcamConstraint, webcamAudioConstraint, desktopConstraint }) => {
    const detectedStreams = {};

    const webcamStream = await navigator.mediaDevices.getUserMedia({
        video: webcamConstraint ?? { width: 1280, height: 720 },
        audio: webcamAudioConstraint ?? true,
    }).catch(() => undefined);
    if (webcamStream) {
        detectedStreams["webcam"] = webcamStream;
    }

    const desktopStream = await navigator.mediaDevices.getUserMedia({
        video: {
            mandatory: {
                chromeMediaSource: 'desktop',
                ...desktopConstraint,
            } ?? {
                chromeMediaSource: 'desktop',
                minWidth: 1920,
                minHeight: 1080,
            }
        }
    }).catch(() => undefined);
    if (desktopStream) {
        detectedStreams["desktop"] = desktopStream;
    }

    streams = detectedStreams;
});

ipcRenderer.on('source:show_debug', async () => {
    for (const streamType of ["desktop", "webcam"]) {
        const video = document.querySelector("video#" + streamType);
        if (streams[streamType]) {
            video.srcObject = streams[streamType];
            video.onloadedmetadata = () => video.play()
        }
    }
    navigator.mediaDevices.enumerateDevices().then((devices) => {
        console.log("Webcam devices:")
        for (const device of devices.filter(d => d.kind === "videoinput")) {
            console.log(`${device.label}: ${device.deviceId}`)
        }
    });
});

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
