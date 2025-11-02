@echo off
pushd agent 
call rmdir /S /Q build\webrtc_grabber_win_x64
call npm run dump_version
call npm run build_win32_x64
call move /Y build\grabber-win32-x64 build\webrtc_grabber_win_x64
call move /Y version.json build\webrtc_grabber_win_x64\version.json
call xcopy /Y scripts\*.bat build\webrtc_grabber_win_x64
REM Ensure that you did npm ci in grabber
popd
