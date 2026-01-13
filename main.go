package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	ics "github.com/arran4/golang-ical"
)

const (
	TargetGroup    = "5.1"
	SourceURL      = "https://www.roe.vsei.ua/disconnections"
	OutputFileName = "discos-5.1.ics"
)

func main() {
	loc, _ := time.LoadLocation("Europe/Kyiv")
	client := &http.Client{Timeout: 60 * time.Second}
	req, _ := http.NewRequest("GET", SourceURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "uk-UA,uk;q=0.9,en-US;q=0.8,en;q=0.7")
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

	cal := ics.NewCalendar()
	cal.SetMethod(ics.MethodPublish)
	cal.SetProductId("-//ROE-Parser//UA")

	// 1. Шукаємо текст "Оновлено: ..." на сторінці
	lastUpdate := "невідомо"
	doc.Find("body").Each(func(i int, s *goquery.Selection) {
		fullText := s.Text()
		reUpdate := regexp.MustCompile(`Оновлено:\s+\d{2}\.\d{2}\.\d{4}\s+\d{2}:\d{2}`)
		match := reUpdate.FindString(fullText)
		if match != "" {
			lastUpdate = match
		}
	})

	// 2. Знаходимо індекс колонки для "5.1"
	columnIndex := -1
	doc.Find("tr").EachWithBreak(func(i int, s *goquery.Selection) bool {
		s.Find("td").Each(func(j int, cell *goquery.Selection) {
			if strings.TrimSpace(cell.Text()) == TargetGroup {
				columnIndex = j
			}
		})
		return columnIndex == -1
	})

	if columnIndex == -1 {
		log.Fatal("Групу не знайдено в таблиці")
	}

	eventCount := 0
	// 3. Проходимо по рядках з датами
	doc.Find("tr").Each(func(i int, row *goquery.Selection) {
		cells := row.Find("td")
		if cells.Length() <= columnIndex {
			return
		}

		// Спроба отримати дату з першої комірки
		dateText := strings.TrimSpace(cells.First().Text())
		// Регулярний вираз для дати (шукаємо дд.мм.рррр)
		reDate := regexp.MustCompile(`\d{2}\.\d{2}\.\d{4}`)
		match := reDate.FindString(dateText)
		if match == "" {
			return
		}

		baseDate, err := time.ParseInLocation("02.01.2006", match, loc)
		if err != nil {
			return
		}

		// Отримуємо інтервали з тегів <p>
		cells.Eq(columnIndex).Find("p").Each(func(k int, p *goquery.Selection) {
			interval := strings.TrimSpace(p.Text())
			if strings.Contains(interval, "-") {
				if addOutageEvent(cal, baseDate, interval, loc, lastUpdate) {
					eventCount++
				}
			}
		})
	})

	f, _ := os.Create(OutputFileName)
	defer f.Close()
	f.WriteString(cal.Serialize())

	fmt.Printf("Успішно! Згенеровано подій: %d у файл %s\n", eventCount, OutputFileName)
}

func addOutageEvent(cal *ics.Calendar, date time.Time, interval string, loc *time.Location, updateInfo string) bool {
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

	// Корекція для кінця доби
	if et.Hour() == 23 && et.Minute() == 59 {
		end = time.Date(date.Year(), date.Month(), date.Day()+1, 0, 0, 0, 0, loc)
	}

	uid := fmt.Sprintf("roe-%s-%d-%02d%02d", TargetGroup, date.Unix(), st.Hour(), et.Hour())
	event := cal.AddEvent(uid)

	// Назва та опис із посиланням
	event.SetSummary("⚡ Відключення: " + TargetGroup)
	event.SetDescription(fmt.Sprintf("%s. Джерело: %s", updateInfo, SourceURL))

	event.SetStartAt(start)
	event.SetEndAt(end)
	event.SetDtStampTime(time.Now())

	// Нагадування за 1 годину
	alarm1h := event.AddAlarm()
	alarm1h.SetAction(ics.ActionDisplay)
	alarm1h.SetTrigger("-PT1H")
	alarm1h.SetProperty(ics.ComponentPropertyDescription, "Світло вимкнуть через 1 годину")
	alarm1h.SetProperty(ics.ComponentPropertySummary, "Відключення світла")

	// Нагадування за 30 хвилин
	alarm30m := event.AddAlarm()
	alarm30m.SetAction(ics.ActionDisplay)
	alarm30m.SetTrigger("-PT30M")
	alarm30m.SetProperty(ics.ComponentPropertyDescription, "Світло вимкнуть через 30 хвилин")
	alarm30m.SetProperty(ics.ComponentPropertySummary, "Відключення світла")

	return true
}
