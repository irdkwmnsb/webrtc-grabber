const { ipcRenderer } = require('electron')
// const {Blob} = require("node:buffer");

let streams = {};
const pcs = new Map();
const activeRecoding = {
    recorders: [],
    recordId: null,
};

setInterval(() => {
    ipcRenderer.invoke('status:connections', { connectionsCount: pcs.size, streamTypes: Object.keys(streams), currentRecordId: activeRecoding.recordId });
}, 3000);

ipcRenderer.on('source:update', async (_, { screenSourceId, webcamConstraint, webcamAudioConstraint, desktopConstraint }) => {
    const detectedStreams = {};

    const webcamStream = await navigator.mediaDevices.getUserMedia({
        video: webcamConstraint ?? { width: 1280, height: 720 },
        audio: webcamAudioConstraint ?? true,
    }).catch(() => undefined);
    if (webcamStream) {
        detectedStreams["webcam"] = webcamStream;
    }

    if (screenSourceId) {
        const desktopStream = await navigator.mediaDevices.getUserMedia({
            video: {
                mandatory: {
                    chromeMediaSource: 'desktop',
                    chromeMediaSourceId: screenSourceId,
                    ...desktopConstraint,
                } ?? {
                    chromeMediaSource: 'desktop',
                    chromeMediaSourceId: screenSourceId,
                    minWidth: 1920,
                    minHeight: 1080,
                }
            }
        }).catch(() => undefined);
        if (desktopStream) {
            detectedStreams["desktop"] = desktopStream;
        }
    }

    streams = detectedStreams;
});

ipcRenderer.on("upload_record", async (_, data, fileName, signallingUrl, peerName) => {
    console.log(`Uploading reaction ${fileName} to server: (preload)`)
    const fileBlob = new Blob([data], {type: 'video/webm'})
    const formData = new FormData()
    formData.append('file', fileBlob, fileName)

    fetch(`${signallingUrl}/api/agent/${peerName}/record_upload`, {method: "POST", body: formData})
        .then(r => {
            r.text().then(text => {
                console.log(`Reaction upload ${fileName} to server: ${text}`)
            });
        }).catch(e => console.error(`Reaction uploading to server error: ${e}`));
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

const stopRecord = (recordId) => {
    if (activeRecoding.recordId === null || activeRecoding.recordId !== recordId) {
        return ;
    }
    activeRecoding.recorders.forEach(r => r.stop());
    activeRecoding.recorders = [];
    activeRecoding.recordId = null;
    console.info(`Stopped recording ${recordId}`);
}

ipcRenderer.on('record_start', (_, recordId, timeout) => {
    stopRecord(activeRecoding.recordId);
    activeRecoding.recordId = recordId;
    activeRecoding.recorders = [];

    Object.entries(streams).forEach(([streamKey, stream]) => {
        const mediaRecorder = new MediaRecorder(stream, {
            mimeType: 'video/webm',
        })

        mediaRecorder.addEventListener('dataavailable', event => {
            // TODO: collect all chanks
            console.log(`dataavailable [${streamKey}]`)
            const blob = new Blob([event.data]);
            let fr = new FileReader();
            fr.onload = async _ => {
                await ipcRenderer.invoke('record_save', recordId, streamKey, new Buffer(fr.result));
            }
            fr.readAsArrayBuffer(blob);
        })

        mediaRecorder.start();
        activeRecoding.recorders.push(mediaRecorder)
        console.info(`Start recording ${recordId} [${streamKey}]`);
    });

    setTimeout(() => {
        stopRecord(recordId);
    }, timeout);
});

ipcRenderer.on('record_stop', (_, recordId) => {
    stopRecord(recordId);
});

ipcRenderer.on('player_disconnect', (_) => {
    console.log("player_disconnect")
    pcs.forEach((connection, playerId) => {
        try {
            connection.close();
            console.log(`close connection for ${playerId}`);
        } catch (e) {
            console.error(`failed to close connection for ${playerId}`);
        }
    });
    pcs.clear();
});


console.log("Grabber version: 2024-12-14")
console.log(`Versions: ${JSON.stringify(process.versions)}`)
