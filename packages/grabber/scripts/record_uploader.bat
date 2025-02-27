@echo off
chcp 65001 > nul

set "directory=%~dp0"
set peername=02

for %%a in ("%directory%*.webm") do (
  echo Upload %%a
  C:\Windows\System32\curl.exe "https://grabber.kbats.ru/api/agent/%peername%/record_upload" -X POST -F file=@"%%a" --fail && move "%%a" "%%a.backup"
)
