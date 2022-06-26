import { nanoid } from 'nanoid';

class Peer {
    constructor(name, id) {
        this.name = name
        this.id = id
        this.lastPing = undefined
    }
}

class Storage {
    peers = new Map();
    // usedNames = new Set();

    hasPeer(id) {
        return this.peers.has(id);
    }

    addPeer(name, id) {
        const newPeer = new Peer(name, id);
        this.peers.set(newPeer.id, newPeer);
        // this.usedNames.add(name);
        return newPeer;
    }

    deletePeer(id) {
        const peer = this.peers.get(id);
        if (peer) {
            this.peers.delete(peer.id);
            // this.usedNames.delete(peer.name);
        }
    }

    ping(id) {
        this.peers.get(id).lastPing = new Date();
    }

    getAll() {
        return [...this.peers.values()];
    }
}

export const instance = new Storage();

export default instance;

