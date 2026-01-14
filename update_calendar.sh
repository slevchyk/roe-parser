#!/bin/bash
# Переходимо в папку з проектом
cd /root/roe-parser || exit

echo "[$(date +'%d.%m %H:%M')] --- Запуск сесії ---"

# Запускаємо парсер
./roe-parser

# Додаємо файли, якщо є зміни
if [[ -n $(git status -s data/) ]]; then
    echo "[$(date +'%H:%M')] Виявлено зміни. Відправка на GitHub..."    
    git add data/discos-*.ics
    git commit -m "Auto-update: $(date +'%d.%m.%Y %H:%M')"
    git push origin main
    echo "[$(date +'%H:%M')] GitHub оновлено успішно."
else
    echo "[$(date +'%H:%M')] Змін у теці data немає."
fi