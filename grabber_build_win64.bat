@echo off
pushd packages\grabber
call rmdir /S /Q build\webrtc_grabber_win_x64
call npm run build_win32_x64
call move /Y build\grabber-win32-x64 build\webrtc_grabber_win_x64
call xcopy /Y scripts\*.bat build\webrtc_grabber_win_x64
REM Ensure that you did npm ci in packages/grabber
popd
