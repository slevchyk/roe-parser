#!/bin/bash
# Переходимо в папку з проектом
cd /root/roe-parser

# Запускаємо парсер
./roe-parser

#Додаємо файлів з теки data
if [[ -n $(git status -s data/) ]]; then
    echo "Оновлення графіків у теці data..."
    git add data/discos-*.ics
    git commit -m "Auto-update: $(date +'%d.%m.%Y %H:%M')"
    git push origin main
    echo "Всі файли відправлено на GitHub!"
else
    echo "Змін у теці data не знайдено."
fi
