const path = require('path');
const fs = require('fs');
const io = require('socket.io-client');
const {app, BrowserWindow, desktopCapturer, ipcMain} = require('electron')

const config = JSON.parse(fs.readFileSync("config.json", {encoding: "utf8"}));
const signalingUrl = config?.signalingUrl ?? "http://localhost:3000";
const peerName = config?.peerName ?? "Test";
console.log("loaded config ", config);

function createWindow() {
    const window = new BrowserWindow({
        width: 800,
        height: 600,
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

function runGrabbing(window) {
    let peerConnectionConfig = undefined;
    desktopCapturer.getSources({types: ['screen']}).then(async sources => {
        for (const source of sources) {
            window.webContents.send('source:set', source.id)
        }
    });

    const url = new URL(signalingUrl);
    url.pathname = "peers";
    url.searchParams.append("name", peerName);
    const socketPath = url.toString();

    console.log("init socket", socketPath);
    const socket = io(socketPath);
    socket.on("connect", async () => {
        console.log("connect");
    });
    socket.on("init_peer", (pcConfig) => {
        peerConnectionConfig = pcConfig;
        console.log("set peerConnectionConfig");
    });
    setInterval(() => {
        socket.emit("ping");
    }, 1000);
    socket.on("offer", async (playerId, offer) => {
        window.webContents.send("offer", playerId, offer, peerConnectionConfig);
    });
    ipcMain.handle('offer_answer', async (_, playerId, offer) => {
        socket.emit("offer_answer", playerId, JSON.parse(offer));
        console.log(`send offer_answer for ${playerId}`);
    });
    socket.on("player_ice", (playerId, candidate) => {
        window.webContents.send("player_ice", playerId, candidate);
    })
    ipcMain.handle("grabber_ice", (_, playerId, candidate) => {
        socket.emit("grabber_ice", playerId, JSON.parse(candidate));
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
