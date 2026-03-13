package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "time/tzdata"

	"github.com/PuerkitoBio/goquery"
	ics "github.com/arran4/golang-ical"
	"golang.org/x/sys/windows/svc"
)

const (
	SourceURL   = "https://www.roe.vsei.ua/disconnections"
	ServiceName = "ROEParsingService"
)

type OutageSlot struct {
	Date     time.Time
	Interval string
}

// Налаштування логування та робочої директорії
func setupEnvironment() {
	exePath, _ := os.Executable()
	workingDir := filepath.Dir(exePath)
	os.Chdir(workingDir)

	logFile, err := os.OpenFile(filepath.Join(workingDir, "service.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err == nil {
		log.SetOutput(logFile)
		// Налаштовуємо прапорці логування: Дата, Час, Мікросекунди (опціонально)
		log.SetFlags(log.Ldate | log.Ltime)
	}
}

// Функція для автоматичного коміту та пушу в Git
func gitCommitCalendars() {
	// 1. Виводимо поточну папку для контролю
	cwd, err := os.Getwd()
	if err != nil {
		log.Printf("[GIT-ERROR] Не вдалося визначити робочу директорію: %v\n", err)
		return
	}
	log.Printf("[GIT] Підготовка до оновлення репозиторію. Робоча директорія: %s\n", cwd)

	// 2.Вказуємо Git, що ця директорія безпечна (для служби)
	exec.Command("git", "config", "--global", "--add", "safe.directory", cwd).Run()

	// 3 Перед комітом підтягуємо зміни, щоб уникнути конфліктів
	exec.Command("git", "pull", "--rebase").Run()

	// 3. Налаштовуємо git user (на випадок, якщо це чистий контейнер без глобальних налаштувань)
	exec.Command("git", "config", "user.email", "s.levchyk@gmail.com").Run()
	exec.Command("git", "config", "user.name", "Serhii Levchyk").Run()

	// 4. Перевіряємо статус (що бачить git)
	statusCmd := exec.Command("git", "status")
	statusOut, _ := statusCmd.CombinedOutput()
	log.Printf("[GIT] Поточний статус перед add:\n%s", string(statusOut))

	if err != nil && strings.Contains(string(statusOut), "nothing to commit") {
		log.Println("[GIT] Нових змін у календарях не виявлено (вміст ідентичний).")
		return
	}

	// 3. Додаємо папку
	addCmd := exec.Command("git", "add", "data")
	addOut, err := addCmd.CombinedOutput()
	if err != nil {
		log.Printf("[GIT-ERROR] Помилка git add: %v. Вивід: %s\n", err, string(addOut))
		return
	}

	// 4. Робимо коміт
	commitMsg := fmt.Sprintf("auto: update calendars %s", time.Now().Format("02.01 15:04"))
	commitCmd := exec.Command("git", "commit", "-a", "-m", commitMsg)
	commitOut, err := commitCmd.CombinedOutput()

	// Якщо Git каже, що змін немає - ми не йдемо на Push
	if err != nil && strings.Contains(string(commitOut), "nothing to commit") {
		log.Println("[GIT] Нових змін у календарях не виявлено (вміст ідентичний).")
		return
	} else if err != nil {
		log.Printf("[GIT-ERROR] Помилка коміту: %v. Вивід: %s\n", err, string(commitOut))
		return
	}

	log.Printf("[GIT] Коміт створено: %s", string(commitOut))

	// 5. Пуш
	log.Println("[GIT] Відправка на GitHub...")
	pushOut, err := exec.Command("git", "push").CombinedOutput()
	if err != nil {
		log.Printf("[GIT-ERROR] Помилка пушу: %s\n", string(pushOut))
		return
	}

	log.Println("[GIT-SUCCESS] Календарі синхронізовано з репозиторієм.")
}

func runParser() {
	// Візуальний розділювач для нового циклу
	log.Println("")
	log.Println("================================================================================")
	log.Println("[INFO] ПОЧАТОК ПАРСИНГУ")
	log.Println("--------------------------------------------------------------------------------")

	os.MkdirAll("data", 0755)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	loc, _ := time.LoadLocation("Europe/Kyiv")
	client := &http.Client{Timeout: 30 * time.Second}

	req, err := http.NewRequestWithContext(ctx, "GET", SourceURL, nil)
	if err != nil {
		log.Printf("[ERROR] Помилка створення запиту: %v\n", err)
		return
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://www.roe.vsei.ua/")

	log.Println("[INFO] Запит до сайту РОЕ...")
	res, err := client.Do(req)
	if err != nil {
		log.Printf("[ERROR] Сайт РОЕ не відповідає: %v\n", err)
		return
	}
	defer res.Body.Close()

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		log.Printf("[ERROR] Помилка обробки HTML: %v\n", err)
		return
	}

	groupIDs := []string{"1.1", "1.2", "2.1", "2.2", "3.1", "3.2", "4.1", "4.2", "5.1", "5.2", "6.1", "6.2"}

	type GroupData struct {
		ID          string
		ColumnIndex int
		Slots       []OutageSlot
	}
	groups := make(map[string]*GroupData)

	for _, id := range groupIDs {
		groups[id] = &GroupData{ID: id, ColumnIndex: -1, Slots: []OutageSlot{}}
	}

	reUpdate := regexp.MustCompile(`Оновлено:\s+\d{2}\.\d{2}\.\d{4}\s+\d{2}:\d{2}`)
	lastUpdate := reUpdate.FindString(doc.Find("body").Text())
	if lastUpdate == "" {
		lastUpdate = "Оновлено: щойно"
	}
	log.Printf("[INFO] Статус на сайті: %s\n", lastUpdate)

	// Пошук індексів колонок
	doc.Find("tr").Each(func(i int, s *goquery.Selection) {
		s.Find("td").Each(func(j int, cell *goquery.Selection) {
			txt := strings.TrimSpace(cell.Text())
			if g, ok := groups[txt]; ok {
				g.ColumnIndex = j
			}
		})
	})

	// Парсинг рядків з датами
	reDate := regexp.MustCompile(`\d{2}\.\d{2}\.\d{4}`)
	parsedCount := 0
	doc.Find("tr").Each(func(i int, row *goquery.Selection) {
		cells := row.Find("td")
		if cells.Length() == 0 {
			return
		}
		dateMatch := reDate.FindString(strings.TrimSpace(cells.First().Text()))
		if dateMatch == "" {
			return
		}
		baseDate, _ := time.ParseInLocation("02.01.2006", dateMatch, loc)

		for _, g := range groups {
			if g.ColumnIndex == -1 || cells.Length() <= g.ColumnIndex {
				continue
			}
			cells.Eq(g.ColumnIndex).Find("p").Each(func(k int, p *goquery.Selection) {
				interval := strings.TrimSpace(p.Text())
				if strings.Contains(interval, "-") {
					g.Slots = append(g.Slots, OutageSlot{Date: baseDate, Interval: interval})
					parsedCount++
				}
			})
		}
	})

	log.Printf("[INFO] Оброблено черг: %d, знайдено слотів: %d\n", len(groups), parsedCount)

	alertConfigs := []struct {
		suffix   string
		triggers []string
	}{
		{"", []string{}},
		{"-30m", []string{"-PT30M"}},
		{"-1h", []string{"-PT1H"}},
		{"-30m-1h", []string{"-PT30M", "-PT1H"}},
	}

	for _, g := range groups {
		for _, cfg := range alertConfigs {
			cal := ics.NewCalendar()
			cal.SetProductId("-//ROE-Parser//UA")
			cal.SetXWRCalName(fmt.Sprintf("РОЕ. Черга: %s ", g.ID))
			cal.SetXWRTimezone("Europe/Kyiv")

			for _, slot := range g.Slots {
				addOutageEvent(cal, slot.Date, slot.Interval, loc, lastUpdate, g.ID, cfg.triggers)
			}

			fileName := filepath.Join("data", fmt.Sprintf("discos-%s%s.ics", g.ID, cfg.suffix))
			f, err := os.Create(fileName)
			if err != nil {
				log.Printf("[ERROR] Не вдалося створити файл %s: %v\n", fileName, err)
				continue
			}
			f.WriteString(cal.Serialize())
			f.Close()
		}
	}

	log.Println("[SUCCESS] Парсинг завершено успішно.")

	gitCommitCalendars()
}

type myService struct{}

func (m *myService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	stopChan := make(chan struct{})

	go func() {
		runParser()

		for {
			now := time.Now()
			nextRun := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 35, 0, 0, now.Location())

			if !now.Before(nextRun) {
				nextRun = nextRun.Add(time.Hour)
			}

			waitDist := time.Until(nextRun)
			log.Printf("[SERVICE] Наступний запуск о %s (через %s)\n",
				nextRun.Format("15:04:05"),
				waitDist.Round(time.Second))

			timer := time.NewTimer(waitDist)

			select {
			case <-timer.C:
				runParser()
			case <-stopChan:
				timer.Stop()
				return
			}
		}
	}()

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

loop:
	for {
		c := <-r
		switch c.Cmd {
		case svc.Interrogate:
			changes <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			log.Println("[SERVICE] Отримано сигнал зупинки служби.")
			close(stopChan)
			break loop
		}
	}
	changes <- svc.Status{State: svc.StopPending}
	return
}

func main() {
	setupEnvironment()

	inService, err := svc.IsWindowsService()
	if err != nil {
		log.Fatalf("[FATAL] Помилка визначення контексту: %v\n", err)
	}

	if !inService {
		log.Println("[INTERACTIVE] Запуск у вікні консолі...")
		runParser()
		fmt.Println("\nГотово. Натисніть Enter для виходу.")
		fmt.Scanln()
		return
	}

	log.Println("[SERVICE] Запуск служби...")
	err = svc.Run(ServiceName, &myService{})
	if err != nil {
		log.Fatalf("[FATAL] Служба зупинилася з помилкою: %v\n", err)
	}
}

func addOutageEvent(cal *ics.Calendar, date time.Time, interval string, loc *time.Location, updateInfo string, groupID string, triggers []string) bool {
	re := regexp.MustCompile(`\s*-\s*`)
	clean := re.ReplaceAllString(interval, "-")
	parts := strings.Split(clean, "-")
	if len(parts) != 2 {
		return false
	}

	st, errS := time.ParseInLocation("15:04", parts[0], loc)
	et, errE := time.ParseInLocation("15:04", parts[1], loc)
	if errS != nil || errE != nil {
		return false
	}

	start := time.Date(date.Year(), date.Month(), date.Day(), st.Hour(), st.Minute(), 0, 0, loc)
	end := time.Date(date.Year(), date.Month(), date.Day(), et.Hour(), et.Minute(), 0, 0, loc)

	if et.Hour() == 23 && et.Minute() == 59 {
		end = time.Date(date.Year(), date.Month(), date.Day()+1, 0, 0, 0, 0, loc)
	}

	uid := fmt.Sprintf("roe-%s-%d-%02d%02d-%s", groupID, date.Unix(), st.Hour(), et.Hour(), strings.Join(triggers, ""))
	event := cal.AddEvent(uid)
	event.SetSummary("⚡ Відключення: " + groupID)
	event.SetDescription(fmt.Sprintf("%s.\nДжерело: %s", updateInfo, SourceURL))
	event.SetStartAt(start)
	event.SetEndAt(end)
	event.SetDtStampTime(time.Now())

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
