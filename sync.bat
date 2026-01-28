@echo off
:: Переходимо в папку, де лежить цей батнік
cd /d "%~dp0"

:: 1. Запуск парсера на Go
start /wait "" roe-parser.exe

:: 2. Перевірка чи з'явилися зміни (щоб не плодити пусті коміти)
git status --porcelain | findstr . > nul
if %errorlevel% equ 0 (
    git add data/*.ics
    git commit -m "Auto-update calendars %date% %time%"
    git push origin main
) else (
    echo No changes detected.
)

:: Явно виходимо з кодом 0
exit /b 0