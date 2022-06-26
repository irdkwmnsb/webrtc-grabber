const {ipcRenderer} = require('electron')
const io = require('socket.io-client');

let stream;

ipcRenderer.on("START", async (event, baseUrl, name) => {
    const url = new URL(baseUrl);
    const socketUrl = new URL("a://e");
    socketUrl.protocol = url.protocol;
    socketUrl.host = url.host;
    socketUrl.pathname = "peers";
    socketUrl.searchParams.append("name", name)
    const socketPath = socketUrl.toString();
    console.log("init socket", socketPath);
    const socket = io(socketPath);
    let pc = undefined;
    socket.on("connect", async () => {
        console.log("connect");
    });
    const interval = setInterval(() => {
        socket.emit("ping");
    }, 1000);
    socket.on("call", async () => {
        console.log("Got call");
        const configuration = {};
        console.log('RTCPeerConnection configuration:', configuration);
        pc = new RTCPeerConnection(configuration);

        console.log("stream:", stream);
        stream.getTracks().forEach(track => {
            console.log("added track");
            console.log(track);
            pc.addTrack(track, stream);
        });

        pc.addEventListener("icecandidate", (event) => {
            console.log("send ice");
            socket.emit("ice", event.candidate);
        })

        pc.addEventListener('iceconnectionstatechange', console.log);

        const offerOptions = {
            offerToReceiveAudio: 0,
            offerToReceiveVideo: 0,
            offerToSendVideo: 1
        };

        const offer = await pc.createOffer(offerOptions);
        socket.emit("offer", offer);
        console.log("send offer");

        await pc.setLocalDescription(offer);
    });
    socket.on("ice", async (candidate) => {
        console.log("got ice");
        await pc.addIceCandidate(candidate);
    })
    socket.on("answer", async (answer) => {
        console.log("got answer");
        await pc.setRemoteDescription(answer);
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
