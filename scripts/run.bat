@echo off
rem clouds launcher for Windows — builds the binary automatically on first run.
cd /d "%~dp0.."
if not exist clouds.exe (
    where go >nul 2>nul
    if errorlevel 1 (
        echo [clouds] Go is required to build clouds.exe: https://go.dev/dl
        echo [clouds] install with:  winget install GoLang.Go
        pause
        exit /b 1
    )
    echo [clouds] first run: building clouds.exe...
    go build -trimpath -ldflags "-s -w" -o clouds.exe . || (pause & exit /b 1)
)
clouds.exe %*
