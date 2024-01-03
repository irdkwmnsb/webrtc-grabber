const path = require('path');
const fs = require('fs');
const {GrabberCaptureClient} = require('webrtc-grabber-sdk/lib/grabber_capture');
const {app, BrowserWindow, desktopCapturer, ipcMain} = require('electron')
const commandLineArgs = require('command-line-args')


app.commandLine.appendSwitch('enable-features', 'WebRTCPipeWireCapturer');

const configS = loadConfigS();
const config = parseArguments();
console.log("loaded config ", config, configS);

let screenSourceId = null;

function createWindow() {
    const window = new BrowserWindow({
        width: 1080,
        height: 660,
        webPreferences: {
            preload: path.join(__dirname, 'preload.js')
        },
        show: config.debug,
    })
    if (config.debug) {
        window.webContents.openDevTools();
    }

    window.loadFile('index.html');
    return window;
}

const sendSourceUpdate = (window) => {
    window.webContents.send('source:update', {
        screenSourceId: screenSourceId,
        webcamConstraint: configS.webcamConstraint,
        webcamAudioConstraint: configS.webcamAudioConstraint,
        desktopConstraint: configS.desktopConstraint,
    });
}

const sendAvailableStreams = (window) => {
    if (screenSourceId) {
        sendSourceUpdate(window);
        return
    }

    desktopCapturer.getSources({types: ['screen']})
        .then(async sources => {
            let id = null;
            for (const source of sources) {
                id = source.id ?? id;
            }
            screenSourceId = id;
            sendSourceUpdate(window);
        });
}

function runStreamsCapturing(window) {
    setInterval(() => sendAvailableStreams(window), 10000);
    sendAvailableStreams(window);

    if (config.debug) {
        setTimeout(() => {
            window.webContents.send("source:show_debug");
        }, 3000);
    }
}

function runGrabbing(window) {
    let peerConnectionConfig = undefined;

    runStreamsCapturing(window);

    let connectionsStatus = {connectionsCount: 0, streamTypes: []};
    ipcMain.handle('status:connections', (_, cs) => {
        connectionsStatus = cs;
    });

    const client = new GrabberCaptureClient(config.peerName, config.signalingUrl);

    let pingTimerId;
    client.target.addEventListener("init_peer", async ({detail: {pcConfig, pingInterval}}) => {
        peerConnectionConfig = pcConfig;
        pingInterval = pingInterval ?? 3000;
        if (pingTimerId) {
            clearInterval(pingTimerId);
        }
        pingTimerId = setInterval(() => {
            client.send_ping(connectionsStatus.connectionsCount, connectionsStatus.streamTypes);
        }, pingInterval);
        console.log(`init peer (pingInterval = ${pingInterval})`);
    });

    client.target.addEventListener("offer", async ({detail: {playerId, offer, streamType}}) => {
        console.log(`create new peer connection for ${playerId}`);
        window.webContents.send("offer", playerId, offer, streamType, peerConnectionConfig);
    });

    ipcMain.handle('offer_answer', async (_, playerId, offer) => {
        client.send_offer_answer(playerId, JSON.parse(offer));
    });

    client.target.addEventListener('player_ice', async ({detail: {peerId, candidate}}) => {
        window.webContents.send("player_ice", peerId, candidate);
    });

    ipcMain.handle("grabber_ice", (_, playerId, candidate) => {
        client.send_grabber_ice(playerId, JSON.parse(candidate));
    });
}

app.whenReady().then(() => {
    let window = createWindow();

    // app.on('activate', () => {
    //     if (BrowserWindow.getAllWindows().length === 0) {
    //         window = createWindow()
    //     }
    // })

    runGrabbing(window);

    app.on('before-quit', () => {
        window.removeAllListeners('close');
        window.close();
    });
})

app.on('window-all-closed', () => {
    if (process.platform !== 'darwin') {
        app.quit()
    }
})


function loadConfigS() {
    try {
        return JSON.parse(fs.readFileSync(path.join(__dirname, "config.json"), {encoding: "utf8"}));
    } catch (e) {
        console.warn("Failed to load config.json", e)
    }
    return {};
}

function parseArguments() {
    const argumentsDefinitions = [
        {name: "debugMode", type: Boolean, defaultValue: false},
        {name: "signalingUrl", alias: "s", type: String},
        {name: "peerName", alias: "n", type: String},
    ];
    const options = commandLineArgs(argumentsDefinitions);

    const config = {};
    config.debug = options.debugMode;
    config.signalingUrl = options.signalingUrl;
    config.peerName = options.peerName;

    return config;
}
