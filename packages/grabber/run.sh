# http://live.aismagilov.ru:3000/    name
echo '{ "signalingUrl": "'$1'", "peerName": "'$2'", "debug": false }'> config.json
./grabber > grabber.log &
