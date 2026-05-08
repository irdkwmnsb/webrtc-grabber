const { ipcRenderer } = require('electron')

let streams = {};
const pcs = new Map();
const activeRecoding = {
    recorders: [],
    recordId: null,
    uploadToken: null,
};

const liveStreamTypes = () => Object.entries(streams)
    .filter(([_, s]) => s.getVideoTracks().some(t => t.readyState === 'live'))
    .map(([k]) => k);

setInterval(() => {
    ipcRenderer.invoke('status:connections', {
        connectionsCount: pcs.size,
        streamTypes: liveStreamTypes(),
        currentRecordId: activeRecoding.recordId,
        proctoringActiveStreams: proctoringSession ? proctoringSession.getActiveStreams() : [],
    });
}, 3000);

const streamHasLiveTracks = (stream) =>
    stream && stream.getTracks().some(t => t.readyState === 'live');

ipcRenderer.on('source:update', async (_, { screenSourceId, webcamConstraint, webcamAudioConstraint, desktopConstraint }) => {
    // Don't re-acquire if we already have live streams; otherwise proctoring/PCs
    // would be left holding stale MediaStream references.
    const haveLive = Object.values(streams).some(streamHasLiveTracks);
    if (haveLive) return;

    const detectedStreams = {};

    const webcamStream = await navigator.mediaDevices.getUserMedia({
        video: webcamConstraint ?? { width: 1280, height: 720 },
        audio: webcamAudioConstraint ?? true,
    }).catch(() => undefined);
    if (webcamStream) {
        detectedStreams["webcam"] = webcamStream;
    }

    if (screenSourceId) {
        const desktopStream = await navigator.mediaDevices.getUserMedia({
            video: {
                mandatory: {
                    chromeMediaSource: 'desktop',
                    chromeMediaSourceId: screenSourceId,
                    ...desktopConstraint,
                } ?? {
                    chromeMediaSource: 'desktop',
                    chromeMediaSourceId: screenSourceId,
                    minWidth: 1920,
                    minHeight: 1080,
                }
            }
        }).catch(() => undefined);
        if (desktopStream) {
            detectedStreams["desktop"] = desktopStream;
        }
    }

    streams = detectedStreams;
    for (const [key, stream] of Object.entries(streams)) {
        stream.getTracks().forEach(track => {
            track.addEventListener('ended', () => {
                console.warn(`stream "${key}" ${track.kind} track ended`);
            });
        });
    }
});

ipcRenderer.on("upload_record", async (_, data, fileName, signallingUrl, peerName, uploadToken) => {
    console.log(`Uploading reaction ${fileName} to server: (preload)`)
    const fileBlob = new Blob([data], {type: 'video/webm'})
    const formData = new FormData()
    formData.append('file', fileBlob, fileName)

    const headers = uploadToken ? {'X-Upload-Token': uploadToken} : {};
    fetch(`${signallingUrl}/api/agent/${peerName}/record_upload`, {method: "POST", body: formData, headers})
        .then(r => {
            r.text().then(text => {
                console.log(`Reaction upload ${fileName} to server: ${text}`)
            });
        }).catch(e => console.error(`Reaction uploading to server error: ${e}`));
});

ipcRenderer.on('source:show_debug', async () => {
    for (const streamType of ["desktop", "webcam"]) {
        const video = document.querySelector("video#" + streamType);
        if (streams[streamType]) {
            video.srcObject = streams[streamType];
            video.onloadedmetadata = () => video.play()
        }
    }
    navigator.mediaDevices.enumerateDevices().then((devices) => {
        console.log("Webcam devices:")
        for (const device of devices.filter(d => d.kind === "videoinput")) {
            console.log(`${device.label}: ${device.deviceId}`)
        }
    });
});

ipcRenderer.on('offer', async (_, playerId, offer, streamType, configuration) => {
    console.log(`create new peer connection for ${playerId}`);
    pcs.set(playerId, new RTCPeerConnection(configuration));
    const pc = pcs.get(playerId);

    streamType = streamType ?? "desktop";
    const stream = streams[streamType];
    if (stream) {
        stream.getTracks().forEach(track => {
            console.log("added track: ", track);
            pc.addTrack(track, stream);
        });
    } else {
        console.warn(`No such ${streamType} as captured stream`);
    }

    pc.addEventListener("icecandidate", (event) => {
        console.log(`send ice for player ${playerId}`);
        ipcRenderer.invoke('grabber_ice', playerId, JSON.stringify(event.candidate));
    })

    pc.addEventListener('connectionstatechange', ({target: connection}) => {
        console.log(`change player ${playerId} connection state ${connection.connectionState}`);
        if (connection.connectionState === "failed") {
            connection.close();
            pcs.delete(playerId);
            console.log(`close connection for ${playerId}`);
        }
    });

    await pc.setRemoteDescription(offer);
    const answer = await pc.createAnswer();
    await pc.setLocalDescription(answer);

    await ipcRenderer.invoke('offer_answer', playerId, JSON.stringify(answer));
    console.log(`send offer_answer for ${playerId}`);
});

ipcRenderer.on('player_ice', (_, playerId, candidate) => {
    pcs.get(playerId).addIceCandidate(candidate)
        .then(() => console.log(`add player_ice from ${playerId}`));
});

const stopRecord = (recordId) => {
    if (activeRecoding.recordId === null || activeRecoding.recordId !== recordId) {
        return ;
    }
    activeRecoding.recorders.forEach(r => r.stop());
    activeRecoding.recorders = [];
    activeRecoding.recordId = null;
    activeRecoding.uploadToken = null;
    console.info(`Stopped recording ${recordId}`);
}

ipcRenderer.on('record_start', (_, recordId, timeout, uploadToken) => {
    stopRecord(activeRecoding.recordId);
    activeRecoding.recordId = recordId;
    activeRecoding.uploadToken = uploadToken || null;
    activeRecoding.recorders = [];

    Object.entries(streams).forEach(([streamKey, stream]) => {
        const mediaRecorder = new MediaRecorder(stream, {
            mimeType: 'video/webm',
        })

        mediaRecorder.addEventListener('dataavailable', event => {
            console.log(`dataavailable [${streamKey}]`)
            const blob = new Blob([event.data]);
            let fr = new FileReader();
            fr.onload = async _ => {
                await ipcRenderer.invoke('record_save', recordId, streamKey, Buffer.from(fr.result));
            }
            fr.readAsArrayBuffer(blob);
        })

        mediaRecorder.start();
        activeRecoding.recorders.push(mediaRecorder)
        console.info(`Start recording ${recordId} [${streamKey}]`);
    });

    setTimeout(() => {
        stopRecord(recordId);
    }, timeout);
});

ipcRenderer.on('record_stop', (_, recordId) => {
    stopRecord(recordId);
});

ipcRenderer.on('player_disconnect', (_) => {
    console.log("player_disconnect")
    pcs.forEach((connection, playerId) => {
        try {
            connection.close();
            console.log(`close connection for ${playerId}`);
        } catch (e) {
            console.error(`failed to close connection for ${playerId}`);
        }
    });
    pcs.clear();
});

// ===== Proctoring =====

const signallingOrigin = () => {
    // signallingUrl can be empty/relative; for absolute upload URLs we prefer http(s)
    // form. main.js passes raw URL via record_upload; for proctoring we derive from
    // the same source via IPC at startup.
    return proctoringSignallingUrl || '';
};

let proctoringSignallingUrl = '';
let proctoringPeerName = '';
ipcRenderer.invoke('proctoring:config').then(({signallingUrl, peerName}) => {
    proctoringSignallingUrl = signallingUrl || '';
    proctoringPeerName = peerName || '';
}).catch(() => {});

class ProctoringQueue {
    constructor(dbName) {
        this.dbName = dbName;
        this.dbPromise = this._open();
    }
    _open() {
        return new Promise((resolve, reject) => {
            const req = indexedDB.open(this.dbName, 1);
            req.onupgradeneeded = () => {
                const db = req.result;
                if (!db.objectStoreNames.contains('chunks')) {
                    db.createObjectStore('chunks', { keyPath: 'id', autoIncrement: true });
                }
            };
            req.onsuccess = () => resolve(req.result);
            req.onerror = () => reject(req.error);
        });
    }
    async _store(mode) {
        const db = await this.dbPromise;
        return db.transaction('chunks', mode).objectStore('chunks');
    }
    async enqueue(item) {
        const store = await this._store('readwrite');
        return new Promise((resolve, reject) => {
            const req = store.add(item);
            req.onsuccess = () => resolve(req.result);
            req.onerror = () => reject(req.error);
        });
    }
    async peek() {
        const store = await this._store('readonly');
        return new Promise((resolve, reject) => {
            const req = store.openCursor();
            req.onsuccess = () => resolve(req.result ? req.result.value : null);
            req.onerror = () => reject(req.error);
        });
    }
    async remove(id) {
        const store = await this._store('readwrite');
        return new Promise((resolve, reject) => {
            const req = store.delete(id);
            req.onsuccess = () => resolve();
            req.onerror = () => reject(req.error);
        });
    }
}

class ProctoringUploader {
    constructor({ signallingUrl, peerName, streamKey, queue }) {
        this.signallingUrl = signallingUrl;
        this.peerName = peerName;
        this.streamKey = streamKey;
        this.queue = queue;
        this.running = false;
        this.attempt = 0;
        this.uploadToken = null;
    }
    setUploadToken(token) { this.uploadToken = token || null; }
    _authHeaders() { return this.uploadToken ? { 'X-Upload-Token': this.uploadToken } : {}; }
    async _backoff(label) {
        this.attempt++;
        const base = Math.min(Math.pow(2, this.attempt) * 1000, 5 * 60 * 1000);
        const delay = base / 2 + Math.random() * (base / 2);
        console.warn(`proctoring[${this.streamKey}] ${label} (attempt ${this.attempt}), retry in ${Math.round(delay)}ms`);
        await new Promise(r => setTimeout(r, delay));
    }
    kick() {
        if (this.running) return;
        this.running = true;
        (async () => {
            try {
                while (true) {
                    const item = await this.queue.peek();
                    if (!item) break;
                    let outcome;
                    try {
                        outcome = await this._upload(item);
                    } catch (e) {
                        await this._backoff(`upload failed: ${e}`);
                        continue;
                    }
                    if (outcome === 'ok' || outcome === 'drop') {
                        await this.queue.remove(item.id);
                        this.attempt = 0;
                        continue;
                    }
                    let progressed = false;
                    try { progressed = await this._reconcile(item.sessionId); }
                    catch (e) { console.warn(`proctoring[${this.streamKey}] reconcile failed`, e); }
                    if (progressed) this.attempt = 0;
                    else await this._backoff('conflict not resolved');
                }
            } finally { this.running = false; }
        })();
    }
    async _upload({ sessionId, seq, blob }) {
        const fd = new FormData();
        fd.append('file', blob, `${seq}.webm`);
        const url = `${this.signallingUrl}/api/agent/${this.peerName}/proctoring_upload`
            + `?sessionId=${encodeURIComponent(sessionId)}`
            + `&streamKey=${encodeURIComponent(this.streamKey)}`
            + `&seq=${encodeURIComponent(seq)}`;
        const res = await fetch(url, { method: 'POST', body: fd, headers: this._authHeaders() });
        if (res.ok) return 'ok';
        if (res.status === 409) return 'conflict';
        if (res.status === 400 || res.status === 413 || res.status === 415) {
            const body = await res.text().catch(() => '');
            console.error(`proctoring[${this.streamKey}] permanent ${res.status} seq=${seq}, dropping: ${body}`);
            return 'drop';
        }
        throw new Error(`status ${res.status}: ${await res.text().catch(() => '')}`);
    }
    async _reconcile(sessionId) {
        const url = `${this.signallingUrl}/api/agent/${this.peerName}/proctoring_state`
            + `?sessionId=${encodeURIComponent(sessionId)}`
            + `&streamKey=${encodeURIComponent(this.streamKey)}`;
        const res = await fetch(url, { headers: this._authHeaders() });
        if (!res.ok) throw new Error(`state fetch: status ${res.status}`);
        const state = await res.json();
        const committed = state.committedSeq;
        let dropped = 0;
        while (true) {
            const head = await this.queue.peek();
            if (!head || head.sessionId !== sessionId || head.seq > committed) break;
            await this.queue.remove(head.id);
            dropped++;
        }
        console.log(`proctoring[${this.streamKey}] reconcile: committedSeq=${committed}, dropped ${dropped}`);
        return dropped > 0;
    }
}

class ProctoringManager {
    constructor({ signallingUrl, peerName, streamKey }) {
        this.peerName = peerName;
        this.streamKey = streamKey;
        this.queue = new ProctoringQueue(`webrtc-grabber-proctoring-${peerName}-${streamKey}`);
        this.uploader = new ProctoringUploader({ signallingUrl, peerName, streamKey, queue: this.queue });
        this.cfg = null;
        this.video = null;
        this.canvas = null;
        this.canvasCtx = null;
        this.canvasStream = null;
        this.recordedStream = null;
        this.recorder = null;
        this.drawTimer = null;
        this.seq = 0;
        this.seqStorageKey = null;
        this.streamingActive = false;
        this.trackListeners = [];
        this.uploader.kick();
    }
    isStreaming() { return this.streamingActive; }
    _seqKey(sessionId) { return `proctoring_next_seq:${this.peerName}:${this.streamKey}:${sessionId}`; }
    _trackEndedSubscribe(track, handler) {
        track.addEventListener('ended', handler);
        this.trackListeners.push({ track, handler });
    }
    _clearTrackListeners() {
        for (const { track, handler } of this.trackListeners) {
            try { track.removeEventListener('ended', handler); } catch (e) {}
        }
        this.trackListeners = [];
    }
    async start(cfg, sourceStream) {
        // If the previous session was a different one, clear its persisted seq.
        // For the same sessionId (e.g. a fallback from resume after track end),
        // we must NOT clear so the seq counter survives the pipeline rebuild —
        // otherwise the relay drops every chunk until seq passes committedSeq.
        const sameSession = this.cfg && this.cfg.sessionId === cfg.sessionId;
        if (!sameSession) await this.stop();
        else this._destroyPipeline();

        this.cfg = cfg;
        this.uploader.setUploadToken(cfg.uploadTokens && cfg.uploadTokens[this.streamKey]);
        this.seqStorageKey = this._seqKey(cfg.sessionId);
        const saved = parseInt(localStorage.getItem(this.seqStorageKey), 10);
        this.seq = Number.isFinite(saved) && saved >= 0 ? saved : 0;
        console.log(`proctoring[${this.streamKey}] starting session=${cfg.sessionId} from seq=${this.seq}`);
        await this._initPipeline(sourceStream);
    }
    async _initPipeline(sourceStream) {
        const videoTrack = sourceStream.getVideoTracks().find(t => t.readyState === 'live');
        const audioTrack = sourceStream.getAudioTracks().find(t => t.readyState === 'live');
        if (!videoTrack) {
            console.warn(`proctoring[${this.streamKey}] no live video track`);
            return;
        }
        this.recordedStream = new MediaStream();

        const videoOnlyStream = new MediaStream([videoTrack]);
        const video = document.createElement('video');
        video.muted = true;
        video.srcObject = videoOnlyStream;
        await video.play().catch(e => console.warn(`proctoring[${this.streamKey}] video play failed`, e));
        this.video = video;

        const settings = videoTrack.getSettings();
        const w = settings.width  || video.videoWidth  || 1280;
        const h = settings.height || video.videoHeight || 720;
        const canvas = document.createElement('canvas');
        canvas.width = w;
        canvas.height = h;
        this.canvas = canvas;
        this.canvasCtx = canvas.getContext('2d');

        const drawIntervalMs = Math.max(1, Math.floor(1000 / this.cfg.fps));
        this.drawTimer = setInterval(() => {
            try { this.canvasCtx.drawImage(this.video, 0, 0, canvas.width, canvas.height); }
            catch (e) {}
        }, drawIntervalMs);
        this.canvasStream = canvas.captureStream(this.cfg.fps);
        this.canvasStream.getVideoTracks().forEach(t => this.recordedStream.addTrack(t));

        this._trackEndedSubscribe(videoTrack, () => {
            console.warn(`proctoring[${this.streamKey}] video track ended; tearing down recorder`);
            this._teardownRecorder();
        });
        if (audioTrack) {
            this.recordedStream.addTrack(audioTrack);
            this._trackEndedSubscribe(audioTrack, () => {
                console.warn(`proctoring[${this.streamKey}] audio track ended`);
            });
        }
        this._startRecorder();
    }
    _startRecorder() {
        const hasAudio = this.recordedStream && this.recordedStream.getAudioTracks().length > 0;
        const candidates = hasAudio
            ? ['video/webm;codecs=vp8,opus', 'video/webm;codecs=vp9,opus', 'video/webm']
            : ['video/webm;codecs=vp8', 'video/webm;codecs=vp9', 'video/webm'];
        const mimeType = candidates.find(t => MediaRecorder.isTypeSupported(t)) || '';
        let recorder;
        try {
            const opts = { videoBitsPerSecond: this.cfg.videoBitrate };
            if (mimeType) opts.mimeType = mimeType;
            if (hasAudio) opts.audioBitsPerSecond = 64000;
            recorder = new MediaRecorder(this.recordedStream, opts);
        } catch (e) {
            console.error(`proctoring[${this.streamKey}] failed to create MediaRecorder`, e);
            return;
        }
        this.recorder = recorder;
        this.streamingActive = true;
        const sessionId = this.cfg.sessionId;
        recorder.addEventListener('dataavailable', async (e) => {
            if (!e.data || !e.data.size) return;
            const seq = this.seq++;
            try {
                await this.queue.enqueue({ sessionId, seq, blob: e.data, createdAt: Date.now() });
                if (this.seqStorageKey) localStorage.setItem(this.seqStorageKey, String(this.seq));
                this.uploader.kick();
            } catch (err) { console.error(`proctoring[${this.streamKey}] enqueue failed`, err); }
        });
        recorder.addEventListener('error', e => console.error(`proctoring[${this.streamKey}] recorder error`, e.error));
        try {
            recorder.start(this.cfg.chunkDurationMs);
            console.log(`proctoring[${this.streamKey}] started: session=${sessionId} fps=${this.cfg.fps} chunk=${this.cfg.chunkDurationMs}ms mime=${mimeType}`);
        } catch (e) { console.error(`proctoring[${this.streamKey}] failed to start recorder`, e); }
    }
    // Pause without tearing down the recorder so resume can reuse the same
    // WebM byte stream (and the same seq counter). A torn-down recorder would
    // emit a fresh WebM header on the next chunk, corrupting the appended file
    // and the seq sequence on the relay would reset to 0 (silently dropped).
    pause() {
        if (this.recorder && this.recorder.state === 'recording') {
            try { this.recorder.pause(); } catch (e) {}
        }
        this.streamingActive = false;
        console.log(`proctoring[${this.streamKey}] paused`);
    }
    resume() {
        if (this.recorder && this.recorder.state === 'paused') {
            try { this.recorder.resume(); } catch (e) {
                console.warn(`proctoring[${this.streamKey}] resume failed`, e);
                return false;
            }
            this.streamingActive = true;
            console.log(`proctoring[${this.streamKey}] resumed`);
            return true;
        }
        return false;
    }
    _teardownRecorder() {
        if (this.recorder && this.recorder.state !== 'inactive') {
            try { this.recorder.requestData(); } catch (e) {}
            try { this.recorder.stop(); } catch (e) {}
        }
        this.recorder = null;
        this.streamingActive = false;
    }
    _destroyPipeline() {
        this._teardownRecorder();
        this._clearTrackListeners();
        if (this.drawTimer) { clearInterval(this.drawTimer); this.drawTimer = null; }
        if (this.canvasStream) {
            try { this.canvasStream.getTracks().forEach(t => t.stop()); } catch (e) {}
            this.canvasStream = null;
        }
        this.recordedStream = null;
        if (this.video) {
            try { this.video.srcObject = null; } catch (e) {}
            this.video = null;
        }
        this.canvas = null;
        this.canvasCtx = null;
    }
    async stop() {
        this._destroyPipeline();
        if (this.seqStorageKey) {
            localStorage.removeItem(this.seqStorageKey);
            this.seqStorageKey = null;
        }
        this.cfg = null;
    }
}

class ProctoringSession {
    constructor() { this.managers = new Map(); }
    _ensure(streamKey) {
        let m = this.managers.get(streamKey);
        if (!m) {
            m = new ProctoringManager({
                signallingUrl: signallingOrigin(),
                peerName: proctoringPeerName,
                streamKey,
            });
            this.managers.set(streamKey, m);
        }
        return m;
    }
    getActiveStreams() {
        const out = [];
        for (const [key, m] of this.managers) if (m.isStreaming()) out.push(key);
        return out;
    }
    async start(cfg) {
        const ps = [];
        for (const [key, stream] of Object.entries(streams)) {
            if (!stream.getVideoTracks().some(t => t.readyState === 'live')) continue;
            ps.push(this._ensure(key).start(cfg, stream).catch(e => console.error(`proctoring[${key}] start failed`, e)));
        }
        await Promise.all(ps);
    }
    pause() { for (const m of this.managers.values()) m.pause(); }
    async resume(cfg) {
        // For each manager: try MediaRecorder.resume() (cheap, keeps WebM stream).
        // Fall back to a full start if the recorder was torn down (e.g. track
        // ended during the pause, or this session is new).
        const ps = [];
        for (const [key, stream] of Object.entries(streams)) {
            if (!stream.getVideoTracks().some(t => t.readyState === 'live')) continue;
            const m = this._ensure(key);
            const sameSession = m.cfg && cfg && m.cfg.sessionId === cfg.sessionId;
            if (sameSession && m.resume()) continue;
            ps.push(m.start(cfg, stream).catch(e => console.error(`proctoring[${key}] resume failed`, e)));
        }
        await Promise.all(ps);
    }
    async stop() {
        const ps = [];
        for (const m of this.managers.values()) ps.push(m.stop());
        await Promise.all(ps);
    }
}

const proctoringSession = new ProctoringSession();

ipcRenderer.on('proctoring_start',  async (_, cfg) => { try { await proctoringSession.start(cfg); } catch (e) { console.error('proctoring start failed', e); } });
ipcRenderer.on('proctoring_pause',  ()        => { proctoringSession.pause(); });
ipcRenderer.on('proctoring_resume', async (_, cfg) => { try { await proctoringSession.resume(cfg); } catch (e) { console.error('proctoring resume failed', e); } });
ipcRenderer.on('proctoring_stop',   async ()   => { try { await proctoringSession.stop();  } catch (e) { console.error('proctoring stop failed', e); } });

console.log("Grabber version: 2024-12-14")
console.log(`Versions: ${JSON.stringify(process.versions)}`)
