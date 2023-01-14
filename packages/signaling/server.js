import express from "express";
import http from "http";
import path from "path";
import {Server} from "socket.io";
import storage from "./storage.js";
import fs from "fs";

const debug = (...x) => console.log(...x);

const config = JSON.parse(fs.readFileSync("conf/config.json", {encoding: "utf8"}));
config.peerConnectionConfig = config.peerConnectionConfig ?? undefined;
config.participants = config.participants ?? [];
config.grabberPingInterval = config.grabberPingInterval ?? 3000;
storage.setParticipants(config.participants);

const app = express();
const server = http.createServer(app);
const io = new Server(server, {cors: {origin: '*'}});


const sendPeersStatus = () => {
    admin.emit("peers", storage.getAll(), storage.getParticipantsStatus());
}

// peer connection

const peers = io.of("peers");

peers.on("connection", (socket) => {
    const name = socket.handshake.query.name;
    if (name === undefined) {
        console.warn("failed connection, name=", name);
        socket.emit("error", "No name");
        socket.disconnect(true);
        return;
    }

    socket.emit("init_peer", config.peerConnectionConfig, config.grabberPingInterval);

    const peer = storage.addPeer(name, socket.id);
    const grabberId = peer.id;
    debug(`new peer connection ${grabberId} [${name}]`);
    sendPeersStatus();

    socket.on("ping", (status) => {
        storage.ping(grabberId, status);
    })
    socket.on("offer_answer", (playerId, answer) => {
        debug(`resend offer_answer from ${grabberId} to ${playerId}`);
        admin.to(playerId).emit("offer_answer", grabberId, answer);
    })
    socket.on("grabber_ice", (playerId, ice) => {
        admin.to(playerId).emit("grabber_ice", grabberId, ice);
    })
});

const checkAdminCredential = (socket) => {
    if (!config.adminCredential) {
        return true;
    }
    return socket.handshake?.auth && socket.handshake?.auth["token"] === config.adminCredential;
}

// admin connection
const admin = io.of("admin");
admin.on("connection", (socket) => {
    if (!checkAdminCredential(socket)){
        socket.emit("auth", "forbidden");
        socket.disconnect(true);
        return;
    }
    const refreshInterval = setInterval(() => {
        sendPeersStatus();
    }, 5000);
    socket.on("disconnect", () => {
        clearInterval(refreshInterval);
    });
    socket.emit("init_peer", config.peerConnectionConfig);

    const playerId = socket.id;
    socket.on("offer", (grabberId, offer, streamType) => {
        debug(`resend offer from ${playerId} to ${grabberId}`);
        peers.to(grabberId).emit("offer", playerId, offer, streamType);
    });
    socket.on("offer_name", (grabberName, offer, streamType) => {
        const peer = storage.getPeerByName(grabberName);
        if (!peer) {
            console.warn(`no grabber peer with name ${grabberName}`);
            return;
        }
        const grabberId = peer.id;
        debug(`resend offer from ${playerId} to ${grabberId}`);
        peers.to(grabberId).emit("offer", playerId, offer, streamType);
    });
    socket.on("player_ice", (grabberId, ice) => {
        peers.to(grabberId).emit("player_ice", playerId, ice);
    });
    socket.on("player_ice_name", (grabberName, ice) => {
        const peer = storage.getPeerByName(grabberName);
        if (!peer) {
            console.warn(`no grabber peer with name ${grabberName}`);
            return;
        }
        peers.to(peer.id).emit("player_ice", playerId, ice);
    });
})

// Serve index.html file
app.get('/', (_, res) => {
    res.sendFile('index.html', {root: path.resolve()});
});
app.get('/player', (_, res) => {
    res.sendFile('player.html', {root: path.resolve()});
});
app.get('/capture', (_, res) => {
    res.sendFile('capture.html', {root: path.resolve()});
});

setInterval(() => storage.deleteOldPeers(), 60000);

server.listen(3000, () => {
    console.log('listening on *:3000');
});
