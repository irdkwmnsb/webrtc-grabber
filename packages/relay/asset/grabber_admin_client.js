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

    playersDisconnect(peerName) {
        return fetch(this._url + `/api/admin/players_disconnect/${peerName}`, {
            method: "POST",
            headers: {
                "Content-Type": "application/json",
                "Authorization": "Basic " + btoa("admin:" + this._credential),
            },
        })
    }
}
