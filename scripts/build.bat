@echo off
rem Explicit build for Windows.
cd /d "%~dp0.."
go build -trimpath -ldflags "-s -w" -o clouds.exe . && echo built clouds.exe
