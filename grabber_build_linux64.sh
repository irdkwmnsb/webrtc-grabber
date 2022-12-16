cd ./packages/grabber
rm -rf build/webrtc_grabber_linux64
npm run build_linux64
mv build/grabber-linux-x64 build/webrtc_grabber_linux64
cp -r scripts/*.sh build/webrtc_grabber_linux64
# Ensure that you did npm ci in packages/grabber
