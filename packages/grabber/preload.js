const {ipcRenderer} = require('electron')
const io = require('socket.io-client');

let stream;

ipcRenderer.on("START", async (event, baseUrl, name) => {
    const url = new URL(baseUrl);
    console.log(url);
    const socketUrl = new URL("a://e");
    socketUrl.protocol = url.protocol;
    socketUrl.host = url.host;
    socketUrl.pathname = "peers";
    socketUrl.searchParams.append("name", name);
    const socketPath = socketUrl.toString();
    console.log("init socket", socketPath);
    const socket = io(socketPath);
    const pcs = new Map();
    socket.on("connect", async () => {
        console.log("connect");
    });
    const interval = setInterval(() => {
        socket.emit("ping");
    }, 1000);
    socket.on("call", async (callerId) => {
        console.log("Got call");
        const configuration = {};
        console.log('RTCPeerConnection configuration:', configuration);
        pcs.set(callerId, new RTCPeerConnection(configuration));

        console.log("stream:", stream);
        stream.getTracks().forEach(track => {
            console.log("added track");
            console.log(track);
            pcs.get(callerId).addTrack(track, stream);
        });

        pcs.get(callerId).addEventListener("icecandidate", (event) => {
            console.log("send ice");
            socket.emit("ice", event.candidate, callerId);
        })

        pcs.get(callerId).addEventListener('iceconnectionstatechange', console.log);


        const offerOptions = {
            offerToReceiveAudio: 0,
            offerToReceiveVideo: 0,
            offerToSendVideo: 1
        };

        const offer = await pcs.get(callerId).createOffer(offerOptions);
        socket.emit("offer", offer, callerId);
        console.log("send offer");

        await pcs.get(callerId).setLocalDescription(offer);
    });
    socket.on("ice", async (candidate, callerId) => {
        console.log("got ice");
        await pcs.get(callerId).addIceCandidate(candidate);
    })
    socket.on("answer", async (answer, callerId) => {
        console.log("got answer");
        await pcs.get(callerId).setRemoteDescription(answer);
    })
})

ipcRenderer.on('SET_SOURCE', async (event, sourceId) => {
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
    handleStream(stream)
})

function handleStream(stream) {
    const video = document.querySelector('video')
    video.srcObject = stream
    video.onloadedmetadata = (e) => video.play()
}
