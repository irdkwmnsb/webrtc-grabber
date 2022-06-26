import express from "express";
import http from "http";
import path from "path";
import {Server} from "socket.io";
import storage from "./storage.js";

const app = express();
const server = http.createServer(app);
const io = new Server(server);


// peer connection

const peers = io.of("peers");


peers.on("connection", (socket) => {
    const name = socket.handshake.query.name;
    if(name === undefined) {
        console.log("failed connection, name=", name);
        socket.emit("error", "No name");
        socket.disconnect(true);
        return;
    }
    const peer = storage.addPeer(name, socket.id);
    admin.emit("peers", storage.getAll());
    console.log("New peer:", name, peer);
    socket.on("ping", () => {
        storage.ping(peer.id);
    })
    socket.on("offer", (offer, callerId) => {
        admin.to(callerId).emit("offer", offer);
    })
    socket.on("ice", (ice, callerId) => {
        admin.to(callerId).emit("ice", ice);
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
    const callerId = socket.id;
    socket.on("call", (id) => {
        console.log("Calling", id);
        peers.to(id).emit("call", callerId);
    })
    socket.on("answer", (id, offer) => {
        console.log("got answer, retransmitting");
        peers.to(id).emit("answer", offer, callerId);
    })
    socket.on("ice", (id, ice) => {
        console.log("got ice, retransmitting");
        peers.to(id).emit("ice", ice, callerId);
    })
})

// Serve index.html file
app.get('/', (_, res) => {
    res.sendFile('index.html', {root: path.resolve()});
});

server.listen(3000, () => {
    console.log('listening on *:3000');
});
