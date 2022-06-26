webrtc-grabber
==========
Main use case for this project:  
Streaming live screen video from contestants' screens on ICPC World Finals as a part of ICPC Live broadcast.  

Consists of two parts:  
- grabber
An electron application that is running in the background and listens for incoming calls from signaling.
- signaling
A socket.io server for signaling between the overlay and grabber.


<h1>THE PROJECT IS NOT CURRENTLY IN ITS STABLE STATE</h1>

## Developing
Start using
`npm ci
npm run grabber:start
npm run signaling:start:dev
`
Runs on localhost port 3000
