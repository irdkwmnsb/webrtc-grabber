class GrabberAdminClient {
    constructor(url, credential) {
        this._url = url;
        this._credential = credential;
    }

    setCredential(credential) {
        this._credential = credential;
    }

    recordStart(peerName, recordId, timeout) {
        return fetch(this._url + "/api/admin/record_start", {
            method: "POST",
            headers: {
                "Content-Type": "application/json",
                "Authorization": "Basic " + btoa("admin:" + this._credential),
            },
            body: JSON.stringify({ peerName, recordId, timeout }),
        })
    }

    recordStop(peerName, recordId) {
        return fetch(this._url + "/api/admin/record_stop", {
            method: "POST",
            headers: {
                "Content-Type": "application/json",
                "Authorization": "Basic " + btoa("admin:" + this._credential),
            },
            body: JSON.stringify({ peerName, recordId }),
        })
    }

    recordUpload(peerName, recordId) {
        return fetch(this._url + "/api/admin/record_upload", {
            method: "POST",
            headers: {
                "Content-Type": "application/json",
                "Authorization": "Basic " + btoa("admin:" + this._credential),
            },
            body: JSON.stringify({ peerName, recordId }),
        })
    }

    playersDisconnect(peerName) {
        return fetch(this._url + `/api/admin/players_disconnect/${peerName}`, {
            method: "POST",
            headers: {
                "Content-Type": "application/json",
                "Authorization": "Basic " + btoa("admin:" + this._credential),
            },
        })
    }

    _proctoring(path, body) {
        return fetch(this._url + "/api/admin/proctoring" + path, {
            method: "POST",
            headers: {
                "Content-Type": "application/json",
                "Authorization": "Basic " + btoa("admin:" + this._credential),
            },
            body: body ? JSON.stringify(body) : undefined,
        });
    }

    proctoringStart({ chunkDurationMs, fps, videoBitrate, endsAt }) {
        return this._proctoring("/start", { chunkDurationMs, fps, videoBitrate, endsAt: endsAt ?? null });
    }

    proctoringPause()  { return this._proctoring("/pause"); }
    proctoringResume() { return this._proctoring("/resume"); }
    proctoringStop()   { return this._proctoring("/stop"); }

    proctoringGet() {
        return fetch(this._url + "/api/admin/proctoring", {
            method: "GET",
            headers: {
                "Authorization": "Basic " + btoa("admin:" + this._credential),
            },
        }).then(r => r.ok ? r.json() : null);
    }

    proctoringFinalize(sessionId) {
        return fetch(this._url + "/api/admin/proctoring/finalize/" + encodeURIComponent(sessionId), {
            method: "POST",
            headers: {
                "Authorization": "Basic " + btoa("admin:" + this._credential),
            },
        });
    }

    async proctoringDownload(sessionId, peerName, streamKey, file) {
        const url = this._url + "/api/admin/proctoring/file/"
            + encodeURIComponent(sessionId) + "/"
            + encodeURIComponent(peerName) + "/"
            + encodeURIComponent(streamKey) + "/"
            + encodeURIComponent(file);
        const res = await fetch(url, {
            headers: { "Authorization": "Basic " + btoa("admin:" + this._credential) },
        });
        if (!res.ok) throw new Error(`download failed: ${res.status} ${await res.text().catch(() => "")}`);
        const blob = await res.blob();
        const a = document.createElement("a");
        const blobUrl = URL.createObjectURL(blob);
        a.href = blobUrl;
        a.download = `${sessionId}_${peerName}_${streamKey}_${file}`;
        document.body.appendChild(a);
        a.click();
        a.remove();
        setTimeout(() => URL.revokeObjectURL(blobUrl), 60_000);
    }
}
