package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"roe-parser/models"

	"github.com/PuerkitoBio/goquery"
	ics "github.com/arran4/golang-ical"
)

const SourceURL = "https://www.roe.vsei.ua/disconnections"

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

	// 1. Ініціалізуємо список всіх груп
	groupIDs := []string{
		"1.1",
		"1.2",
		"2.1",
		"2.2",
		"3.1",
		"3.2",
		"4.1",
		"4.2",
		"5.1",
		"5.2",
		"6.1",
		"6.2"}
	groups := make(map[string]*models.WorkGroup)

	for _, id := range groupIDs {
		cal := ics.NewCalendar()
		cal.SetMethod(ics.MethodPublish)
		cal.SetProductId("-//ROE-Parser//UA")
		groups[id] = &models.WorkGroup{
			ID:          id,
			ColumnIndex: -1,
			Calendar:    cal,
		}
	}

	// 2. Знаходимо "Оновлено: ..."
	lastUpdate := "невідомо"
	reUpdate := regexp.MustCompile(`Оновлено:\s+\d{2}\.\d{2}\.\d{4}\s+\d{2}:\d{2}`)
	lastUpdate = reUpdate.FindString(doc.Find("body").Text())

	// 3. Знаходимо індекси колонок для кожної групи
	doc.Find("tr").Each(func(i int, s *goquery.Selection) {
		s.Find("td").Each(func(j int, cell *goquery.Selection) {
			txt := strings.TrimSpace(cell.Text())
			if g, ok := groups[txt]; ok {
				g.ColumnIndex = j
			}
		})
	})

	// 4. Проходимо по рядках таблиці
	reDate := regexp.MustCompile(`\d{2}\.\d{2}\.\d{4}`)
	doc.Find("tr").Each(func(i int, row *goquery.Selection) {
		cells := row.Find("td")
		dateMatch := reDate.FindString(strings.TrimSpace(cells.First().Text()))
		if dateMatch == "" {
			return
		}

		baseDate, err := time.ParseInLocation("02.01.2006", dateMatch, loc)
		if err != nil {
			return
		}

		// Для кожної знайденої групи витягуємо дані з її колонки
		for _, g := range groups {
			if g.ColumnIndex == -1 || cells.Length() <= g.ColumnIndex {
				continue
			}

			cells.Eq(g.ColumnIndex).Find("p").Each(func(k int, p *goquery.Selection) {
				interval := strings.TrimSpace(p.Text())
				if strings.Contains(interval, "-") {
					if addOutageEvent(g.Calendar, baseDate, interval, loc, lastUpdate, g.ID) {
						g.EventCount++
					}
				}
			})
		}
	})

	// 5. Зберігаємо всі 12 файлів
	for _, g := range groups {
		fileName := fmt.Sprintf("data/discos-%s.ics", g.ID)
		f, err := os.Create(fileName)
		if err != nil {
			fmt.Printf("Помилка створення файлу %s: %v\n", fileName, err)
			continue
		}
		f.WriteString(g.Calendar.Serialize())
		f.Close()
		fmt.Printf("Група %s: згенеровано %d подій -> %s\n", g.ID, g.EventCount, fileName)
	}
}

func addOutageEvent(cal *ics.Calendar, date time.Time, interval string, loc *time.Location, updateInfo string, groupID string) bool {
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

	uid := fmt.Sprintf("roe-%s-%d-%02d%02d", groupID, date.Unix(), st.Hour(), et.Hour())
	event := cal.AddEvent(uid)

	event.SetSummary("⚡ Відключення: " + groupID)
	event.SetDescription(fmt.Sprintf("%s. Джерело: %s", updateInfo, SourceURL))
	event.SetStartAt(start)
	event.SetEndAt(end)
	event.SetDtStampTime(time.Now())

	// Нагадування
	alarms := []models.Alarm{
		{Trigger: "-PT1H", Description: "1 годину"},
		{Trigger: "-PT30M", Description: "30 хвилин"},
	}

	for _, el := range alarms {
		a := event.AddAlarm()
		a.SetAction(ics.ActionDisplay)
		a.SetTrigger(el.Trigger)
		a.SetProperty(ics.ComponentPropertyDescription, "Світло вимкнуть через "+el.Description)
		a.SetProperty(ics.ComponentPropertySummary, "Відключення світла")
	}

	return true
}
