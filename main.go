package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/PuerkitoBio/goquery"
	ics "github.com/arran4/golang-ical"
)

const SourceURL = "https://www.roe.vsei.ua/disconnections"

// Допоміжна структура для збереження знайдених слотів відключень
type OutageSlot struct {
	Date     time.Time
	Interval string
}

func main() {
	loc, _ := time.LoadLocation("Europe/Kyiv")
	client := &http.Client{Timeout: 60 * time.Second}
	req, _ := http.NewRequest("GET", SourceURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://www.roe.vsei.ua/")

	res, err := client.Do(req)
	if err != nil {
		log.Fatalf("Помилка завантаження: %v", err)
	}
	defer res.Body.Close()

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		log.Fatalf("Помилка парсингу: %v", err)
	}

	// 1. Ініціалізуємо групи та слайси для зберігання знайдених інтервалів
	groupIDs := []string{"1.1", "1.2", "2.1", "2.2", "3.1", "3.2", "4.1", "4.2", "5.1", "5.2", "6.1", "6.2"}

	// Розширюємо вашу модель або використовуємо мапу для накопичення даних
	type GroupData struct {
		ID          string
		ColumnIndex int
		Slots       []OutageSlot
	}
	groups := make(map[string]*GroupData)

	for _, id := range groupIDs {
		groups[id] = &GroupData{
			ID:          id,
			ColumnIndex: -1,
			Slots:       []OutageSlot{},
		}
	}

	// 2. Знаходимо "Оновлено: ..."
	reUpdate := regexp.MustCompile(`Оновлено:\s+\d{2}\.\d{2}\.\d{4}\s+\d{2}:\d{2}`)
	lastUpdate := reUpdate.FindString(doc.Find("body").Text())
	if lastUpdate == "" {
		lastUpdate = "Оновлено: щойно"
	}

	// 3. Знаходимо індекси колонок
	doc.Find("tr").Each(func(i int, s *goquery.Selection) {
		s.Find("td").Each(func(j int, cell *goquery.Selection) {
			txt := strings.TrimSpace(cell.Text())
			if g, ok := groups[txt]; ok {
				g.ColumnIndex = j
			}
		})
	})

	// 4. Збираємо дані з таблиці в пам'ять
	reDate := regexp.MustCompile(`\d{2}\.\d{2}\.\d{4}`)
	doc.Find("tr").Each(func(i int, row *goquery.Selection) {
		cells := row.Find("td")
		if cells.Length() == 0 {
			return
		}

		dateMatch := reDate.FindString(strings.TrimSpace(cells.First().Text()))
		if dateMatch == "" {
			return
		}

		baseDate, err := time.ParseInLocation("02.01.2006", dateMatch, loc)
		if err != nil {
			return
		}

		for _, g := range groups {
			if g.ColumnIndex == -1 || cells.Length() <= g.ColumnIndex {
				continue
			}

			cells.Eq(g.ColumnIndex).Find("p").Each(func(k int, p *goquery.Selection) {
				interval := strings.TrimSpace(p.Text())
				if strings.Contains(interval, "-") {
					g.Slots = append(g.Slots, OutageSlot{Date: baseDate, Interval: interval})
				}
			})
		}
	})

	// 5. ГЕНЕРАЦІЯ ФАЙЛІВ
	// Визначаємо типи сповіщень
	alertConfigs := []struct {
		suffix   string
		triggers []string
	}{
		{suffix: "", triggers: []string{}},
		{suffix: "-30m", triggers: []string{"-PT30M"}},
		{suffix: "-1h", triggers: []string{"-PT1H"}},
		{suffix: "-30m-1h", triggers: []string{"-PT30M", "-PT1H"}},
	}

	for _, g := range groups {
		for _, cfg := range alertConfigs {
			cal := ics.NewCalendar()
			cal.SetProductId("-//ROE-Parser//UA")
			cal.SetXWRCalName(fmt.Sprintf("РОЕ. Черга: %s ", g.ID))
			cal.SetXWRTimezone("Europe/Kyiv")

			eventsCreated := 0
			for _, slot := range g.Slots {
				if addOutageEvent(cal, slot.Date, slot.Interval, loc, lastUpdate, g.ID, cfg.triggers) {
					eventsCreated++
				}
			}

			// Зберігаємо файл
			fileName := fmt.Sprintf("data/discos-%s%s.ics", g.ID, cfg.suffix)
			f, err := os.Create(fileName)
			if err != nil {
				fmt.Printf("Помилка створення %s: %v\n", fileName, err)
				continue
			}
			f.WriteString(cal.Serialize())
			f.Close()

			if cfg.suffix == "" { // Виводимо лог тільки для основного файлу, щоб не спамити
				fmt.Printf("Черга %s: %d подій згенеровано\n", g.ID, eventsCreated)
			}
		}
	}
}

func addOutageEvent(cal *ics.Calendar, date time.Time, interval string, loc *time.Location, updateInfo string, groupID string, triggers []string) bool {
	re := regexp.MustCompile(`\s*-\s*`)
	clean := re.ReplaceAllString(interval, "-")
	parts := strings.Split(clean, "-")
	if len(parts) != 2 {
		return false
	}

	layout := "15:04"
	st, errS := time.ParseInLocation(layout, parts[0], loc)
	et, errE := time.ParseInLocation(layout, parts[1], loc)
	if errS != nil || errE != nil {
		return false
	}

	start := time.Date(date.Year(), date.Month(), date.Day(), st.Hour(), st.Minute(), 0, 0, loc)
	end := time.Date(date.Year(), date.Month(), date.Day(), et.Hour(), et.Minute(), 0, 0, loc)

	if et.Hour() == 23 && et.Minute() == 59 {
		end = time.Date(date.Year(), date.Month(), date.Day()+1, 0, 0, 0, 0, loc)
	}

	// Додаємо суфікс тригерів до UID, щоб події були унікальними між різними календарями, якщо вони перетинаються
	triggerID := strings.Join(triggers, "")
	uid := fmt.Sprintf("roe-%s-%d-%02d%02d-%s", groupID, date.Unix(), st.Hour(), et.Hour(), triggerID)

	event := cal.AddEvent(uid)
	event.SetSummary("⚡ Відключення: " + groupID)
	event.SetDescription(fmt.Sprintf("%s.\nДжерело: %s", updateInfo, SourceURL))
	event.SetStartAt(start)
	event.SetEndAt(end)
	event.SetDtStampTime(time.Now())

	// Додаємо аларми згідно конфігурації
	for _, tr := range triggers {
		a := event.AddAlarm()
		a.SetAction(ics.ActionDisplay)
		a.SetTrigger(tr)

		label := "30 хвилин"
		if tr == "-PT1H" {
			label = "1 годину"
		}

		a.SetProperty(ics.ComponentPropertyDescription, "Ел. енергію вимкнуть через "+label)
		a.SetProperty(ics.ComponentPropertySummary, "Відключення світла")
	}

	return true
}
