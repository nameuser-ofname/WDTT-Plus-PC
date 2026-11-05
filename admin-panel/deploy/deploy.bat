@echo off
chcp 65001 >nul
title WDTT Admin - установка по SSH
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0deploy.ps1"
echo.
pause
