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
    participants = [];
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

    getPeerByName(name) {
        return [ ...this.peers.values() ]
            .filter(p => p.name === name)
            .sort((a, b) => b.lastPing - a.lastPing)
            .find(() => true);
    }

    deletePeer(id) {
        const peer = this.peers.get(id);
        if (peer) {
            this.peers.delete(peer.id);
            // this.usedNames.delete(peer.name);
        }
    }

    deleteOldPeers() {
        const now = new Date();
        [...this.peers.values()].filter((p) => now - p.lastPing > 60000)
            .forEach(({id}) => this.deletePeer(id));
    }

    ping(id) {
        this.peers.get(id).lastPing = new Date();
    }

    getAll() {
        return [...this.peers.values()];
    }

    getParticipantsStatus() {
        return this.participants.map(partName => this.getPeerByName(partName) ?? new Peer(partName, null));
    }

    setParticipants(participants) {
        this.participants = participants;
    }
}

export const instance = new Storage();

export default instance;

