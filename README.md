webrtc-grabber
==========
Main use case for this project:  
Streaming live screen video from contestants' screens on ICPC World Finals as a part of ICPC Live broadcast.  

## Signalling
A socket.io node js server for signaling between the players and grabbers.

Runs on localhost port 3000.

Start using (in `packages/signaling` directory)
```
npm ci
npm run start:dev
```
or as docker image
```
sudo docker build -t grabber .
sudo docker run -d -p 3000:3000 --name grabber grabber
```

### signalling config.json
See example in [config.json.example](packages/signaling/config.json.example).
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
An electron application that is running in the background and listens for incoming calls from signaling.

Run in windows `.\run_runner.bat "{signallingUrl}" "{peerName}"`

Run in linux `./run_runner.sh "{signallingUrl}" "{peerName}"`

### grabber config.json
```json
{
  "debug": false,
  "signalingUrl": "signaling url, you can set when using run_runner",
  "peerName": "name of peer (participant number or etc), you can set when using run_runner"
}
```
