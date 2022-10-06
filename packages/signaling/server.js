import express from "express";
import http from "http";
import path from "path";
import {Server} from "socket.io";
import storage from "./storage.js";

const debug = (...x) => console.log(...x);

const app = express();
const server = http.createServer(app);
const io = new Server(server);


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

    const peer = storage.addPeer(name, socket.id);
    const grabberId = peer.id;
    debug(`new peer connection ${grabberId} [${name}]`);
    admin.emit("peers", storage.getAll());

    socket.on("ping", () => {
        storage.ping(grabberId);
    })
    socket.on("offer_answer", (playerId, answer) => {
        debug(`resend offer_answer from ${grabberId} to ${playerId}`);
        admin.to(playerId).emit("offer_answer", grabberId, answer);
    })
    socket.on("grabber_ice", (playerId, ice) => {
        admin.to(playerId).emit("grabber_ice", grabberId, ice);
    })
});


// admin connection
const admin = io.of("admin");
admin.on("connection", (socket) => {
    const refreshInterval = setInterval(() => {
        socket.emit("peers", storage.getAll());
    }, 1000);
    socket.on("disconnect", () => {
        clearInterval(refreshInterval);
    })
    const playerId = socket.id;
    socket.on("offer", (grabberId, offer) => {
        debug(`resend offer from ${playerId} to ${grabberId}`);
        peers.to(grabberId).emit("offer", playerId, offer);
    });
    socket.on("offer_name", (grabberName, offer) => {
        const peer = storage.getPeerByName(grabberName);
        if (!peer) {
            console.warn(`no grabber peer with name ${grabberName}`);
            return;
        }
        const grabberId = peer.id;
        debug(`resend offer from ${playerId} to ${grabberId}`);
        peers.to(grabberId).emit("offer", playerId, offer);
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

setInterval(() => storage.deleteOldPeers(), 60000);

server.listen(3000, () => {
    console.log('listening on *:3000');
});
