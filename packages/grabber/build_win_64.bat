@echo off
call npm run build_win_64
call xcopy runner.bat build\grabber-win32-x64
call xcopy stopper.bat build\grabber-win32-x64
call xcopy tester.bat build\grabber-win32-x64
call xcopy config.json build\grabber-win32-x64
