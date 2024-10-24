"use strict";

import path from 'path';
import fs from 'fs/promises';
import { fileURLToPath } from 'url';
import {GrabberCaptureClient} from 'webrtc-grabber-sdk';
import {app, BrowserWindow, desktopCapturer, ipcMain} from 'electron';
import commandLineArgs from 'command-line-args';


app.commandLine.appendSwitch('enable-features', 'WebRTCPipeWireCapturer');

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const config = parseArguments();
console.log(process.versions);

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

async function runStreamsCapturing(window) {
    const sendAvailableStreams = async () => {
        const sources = await desktopCapturer.getSources({types: ['screen']});
        const configS = await loadConfigS();
        console.log("loaded config ", config, configS);
        window.webContents.send('source:update', {
            screenSourceId: sources[0]?.id,
            webcamConstraint: configS.webcamConstraint,
            webcamAudioConstraint: configS.webcamAudioConstraint,
            desktopConstraint: configS.desktopConstraint,
        });
    }
    setInterval(() => sendAvailableStreams(window), 10000);
    await sendAvailableStreams(window);
}

async function runGrabber(window) {
    let peerConnectionConfig = undefined;

    await runStreamsCapturing(window);

    if (config.debug) {
        setTimeout(() => {
            window.webContents.send("source:show_debug");
        }, 3000);
    }

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

app.whenReady().then(async () => {
    let window = createWindow();

    // app.on('activate', () => {
    //     if (BrowserWindow.getAllWindows().length === 0) {
    //         window = createWindow()
    //     }
    // })

    await runGrabber(window);

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


async function loadConfigS() {
    try {
        const rawContent = await fs.readFile(path.join(__dirname, "config.json"), {encoding: "utf8"})
        return JSON.parse(rawContent);
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
