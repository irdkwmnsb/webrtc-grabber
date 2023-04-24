# WebRTC-grabber
---

**Main use case for this project:**  
Streaming live screen video from contestants' screens on ICPC World Finals 
as a part of ICPC Live broadcast.



## Signalling
---

A socket.io node js server for signaling between the players and grabbers.

Runs on `localhost` port `3000`.

Start using (in `packages/relay` directory)

```
npm ci
npm run start:dev
```

or as docker image

```
sudo docker build -t grabber .
sudo docker run -d -p 3000:3000 --name grabber grabber
```


### Configure the Signalling

See example in [config.json](packages/relay/conf/config.json):

```json
{
  "participants": [
    "list","of","expected", "participants", "streams"
  ],
  "peerConnectionConfig": {
    "iceServers": [
      {
        "urls": "stun:{hostname of stun server or skip}:{port}"
      },
      {
        "urls": "turn:{ip address of turn server or skip}:{port}",
        "username": "admin",
        "credential": "credential"
      }
    ]
  },
  "grabberPingInterval": "{ping interval in millisecond (number)}"
}
```



## Grabber
---

Grabber is an electron application that is running in the background and listens 
for incoming calls from signaling.


### Run

On the contestants' PC you need to extract files from the
`webrtc_grabber_grabber_<os>_<arch>.zip` archive, which you can find
on the `Release` page.

After that, you can run the grabber:

#### Windows

- Launch in background (see `runner.bat`):
  ```
  %~dp0grabber.exe . --peerName={number of computer} --signalingUrl="{signalling url}"
  ```

- For testing use the script `tester.bat`:
  ```
  %~dp0grabber.exe . --debugMode --peerName={number of computer} --signalingUrl="{signalling url}"
  ```

- Stop the grabber with the `stopper.bat` script.


#### Linux

- Launch in background:
  ```shell
  $ bash grabber-linux.sh run {computer number} {signalling url}
  ```

- For testing use
  ```shell
  $ bash grabber-linux.sh test {computer number} {signalling url}
  ```

- Stop the grabber with
  ```shell
  $ bash grabber-linux.sh stop {computer number} {signalling url}
  ```


### Configure the Grabber

Grabber `config.json`:

```json
{
  "webcamConstraint": {
    "aspectRatio": 1.7777777778
  },
  "webcamAudioConstraint": true,
  "desktopConstraint": {
    "width": 1280,
    "height": 720
  }
}
```
