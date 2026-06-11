package main

import (
	"bufio"
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	_ "github.com/mattn/go-sqlite3"
)

const (
	colorReset  = "\033[0m"
	colorCyan   = "\033[36m"
	colorYellow = "\033[33m"
	colorGreen  = "\033[32m"
	colorRed    = "\033[31m"
)

const banner = `                                                              
   ███████╗██╗  ██╗██╗   ██╗███████╗ ██████╗██████╗   █████╗ ██████╗ ███████╗██████╗ 
   ██╔════╝██║ ██╔╝╚██╗ ██╔╝██╔════╝██╔════╝██╔══██╗ ██╔══██╗██╔══██╗██╔════╝██╔══██╗
   ███████╗█████╔╝  ╚████╔╝ ███████╗██║     ██████╔╝ ███████║██████╔╝█████╗  ██████╔╝
   ╚════██║██╔═██╗   ╚██╔╝  ╚════██║██║     ██╔══██╗ ██╔══██║██╔═══╝ ██╔══╝  ██╔══██╗
   ███████║██║  ██╗   ██║   ███████║╚██████╗██║  ██║ ██║  ██║██║     ███████╗██║  ██║
   ╚══════╝╚═╝  ╚═╝   ╚═╝   ╚══════╝ ╚═════╝╚═╝  ╚═╝ ╚═╝  ╚═╝╚═╝     ╚══════╝╚═╝  ╚═╝                                  
                	 S K Y S C R A P E R   A N A L Y Z E R        
                     		      Version 1.0.0                          

`

func isTerminal() bool {
	if runtime.GOOS == "windows" {
		return isWindowsTerminal()
	}
	stat, _ := os.Stdout.Stat()
	return (stat.Mode() & os.ModeCharDevice) != 0
}

func isWindowsTerminal() bool {
	var mode uint32
	handle := syscall.Handle(os.Stdout.Fd())
	err := syscall.GetConsoleMode(handle, &mode)
	return err == nil
}

func enableVirtualTerminal() {
	if runtime.GOOS != "windows" {
		return
	}
	const ENABLE_VIRTUAL_TERMINAL_PROCESSING = 0x0004
	handle := syscall.Handle(os.Stdout.Fd())
	var mode uint32
	if err := syscall.GetConsoleMode(handle, &mode); err != nil {
		return
	}
	newMode := mode | ENABLE_VIRTUAL_TERMINAL_PROCESSING
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	procSetConsoleMode := kernel32.NewProc("SetConsoleMode")
	procSetConsoleMode.Call(uintptr(handle), uintptr(newMode))
}

func safeReadString(reader *bufio.Reader) (string, error) {
	text, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(text), nil
}

func prompt(reader *bufio.Reader, question string) string {
	fmt.Print(question)
	text, err := safeReadString(reader)
	if err == io.EOF {
		fmt.Println("\nВвод прерван пользователем.")
		os.Exit(0)
	} else if err != nil {
		log.Printf("Ошибка ввода: %v", err)
		os.Exit(1)
	}
	return text
}

func promptDefault(reader *bufio.Reader, question string, defaultVal string) string {
	text := prompt(reader, question)
	if text == "" {
		return defaultVal
	}
	return text
}

func promptYesNo(reader *bufio.Reader, question string) bool {
	for {
		answer := strings.ToLower(prompt(reader, question))
		switch answer {
		case "y", "yes", "д", "да":
			return true
		case "n", "no", "н", "нет":
			return false
		default:
			fmt.Printf("%sВведите y/yes/да или n/no/нет%s\n", colorRed, colorReset)
		}
	}
}

func waitForEnter() {
	fmt.Printf("\n%sНажмите Enter для продолжения...%s", colorYellow, colorReset)
	bufio.NewReader(os.Stdin).ReadBytes('\n')
}

func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	d := make([][]int, la+1)
	for i := range d {
		d[i] = make([]int, lb+1)
	}
	for i := 0; i <= la; i++ {
		d[i][0] = i
	}
	for j := 0; j <= lb; j++ {
		d[0][j] = j
	}
	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
		}
	}
	return d[la][lb]
}

func min(vals ...int) int {
	m := vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

func tokenize(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func containsFuzzyWord(text, query string) (matchedWord string, found bool) {
	textLower := strings.ToLower(text)
	queryLower := strings.ToLower(query)
	words := tokenize(textLower)

	maxDist := 2
	if len(queryLower) <= 3 {
		maxDist = 1
	}

	for _, w := range words {
		if len(w) < 3 {
			continue
		}
		dist := levenshtein(w, queryLower)
		if dist <= maxDist {
			origWords := tokenize(text)
			origLower := tokenize(textLower)
			for i, ow := range origLower {
				if ow == w {
					return origWords[i], true
				}
			}
			return w, true
		}
	}
	return "", false
}

var stopWords = map[string]bool{
	"это": true, "как": true, "для": true, "что": true, "все": true, "есть": true, "так": true,
	"and": true, "the": true, "you": true, "for": true, "are": true, "but": true, "not": true,
	"этот": true, "или": true, "быть": true, "они": true, "мы": true, "ещё": true, "уже": true,
	"был": true, "была": true, "было": true, "были": true, "очень": true, "весь": true, "там": true,
	"когда": true, "где": true, "почему": true, "зачем": true, "кто": true, "кому": true,
}

type commentRow struct {
	username, text, ts, postURL, postCode string
	isExact                               bool
	matchWord                             string
}

func main() {
	enableVirtualTerminal()

	fmt.Print(colorCyan + banner + colorReset)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	var currentDB *sql.DB
	go func() {
		<-sigCh
		fmt.Printf("\n%sСигнал завершения. Очистка ресурсов...%s\n", colorYellow, colorReset)
		if currentDB != nil {
			currentDB.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
			currentDB.Close()
		}
		os.Exit(0)
	}()

	reader := bufio.NewReader(os.Stdin)

	for {
		chosenDB, shouldExit := selectDatabase(reader)
		if shouldExit {
			waitForEnter()
			return
		}

		db, err := sql.Open("sqlite3", chosenDB)
		if err != nil {
			fmt.Printf("%sОшибка открытия базы: %v%s\n", colorRed, err, colorReset)
			waitForEnter()
			continue
		}
		currentDB = db

		if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			log.Printf("Предупреждение: не удалось выполнить wal_checkpoint при старте: %v", err)
		}

		fmt.Printf("\n%sБаза %s успешно открыта.%s\n", colorGreen, chosenDB, colorReset)

		switchDB := runWithDB(db, reader)

		if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			log.Printf("Ошибка при WAL checkpoint: %v", err)
		}
		db.Close()
		fmt.Printf("%sБаза данных закрыта, временные файлы удалены.%s\n", colorGreen, colorReset)

		if !switchDB {
			waitForEnter()
			return
		}
	}
}

func selectDatabase(reader *bufio.Reader) (string, bool) {
	dbFiles, err := filepath.Glob("*.db")
	if err != nil {
		fmt.Printf("%sОшибка поиска файлов: %v%s\n", colorRed, err, colorReset)
		return "", true
	}

	if len(dbFiles) == 0 {
		fmt.Printf("%sВ текущем каталоге нет файлов .db.%s\n", colorRed, colorReset)
		for {
			input := promptDefault(reader, fmt.Sprintf("%sВведите путь к файлу базы данных (или Enter для выхода): %s", colorCyan, colorReset), "")
			if input == "" {
				return "", true
			}
			if _, err := os.Stat(input); err != nil {
				fmt.Printf("%sФайл не найден. Попробуйте снова.%s\n", colorRed, colorReset)
				continue
			}
			return input, false
		}
	}

	fmt.Printf("%sНайдены файлы баз данных:%s\n", colorCyan, colorReset)
	for i, f := range dbFiles {
		fmt.Printf("  [%d] %s\n", i+1, f)
	}
	fmt.Println("  [0] Выход")
	for {
		input := prompt(reader, fmt.Sprintf("%sВведите номер файла или полный путь к базе: %s", colorCyan, colorReset))
		if num, err := strconv.Atoi(input); err == nil {
			if num == 0 {
				return "", true
			}
			if num >= 1 && num <= len(dbFiles) {
				return dbFiles[num-1], false
			}
		} else if _, err := os.Stat(input); err == nil {
			return input, false
		}
		fmt.Printf("%sНеверный ввод. Попробуйте снова.%s\n", colorRed, colorReset)
	}
}

func runWithDB(db *sql.DB, reader *bufio.Reader) bool {
	for {
		fmt.Println()
		fmt.Printf("%s=== Анализатор комментариев ===%s\n", colorGreen, colorReset)
		fmt.Println("[1] Поиск по тексту (точный + нечёткий)")
		fmt.Println("[2] Все комментарии от пользователя")
		fmt.Println("[3] Топ активных пользователей")
		fmt.Println("[4] Комментарии за период")
		fmt.Println("[5] Частотный словарь")
		fmt.Println("[6] Посты по уникальным пользователям")
		fmt.Println("[7] Сменить базу данных")
		fmt.Println("[8] Выход из программы")
		choice := prompt(reader, fmt.Sprintf("%sВыберите действие (1-8): %s", colorCyan, colorReset))

		switch choice {
		case "1":
			combinedSearch(db, reader)
		case "2":
			userComments(db, reader)
		case "3":
			topActiveUsers(db)
		case "4":
			commentsByPeriod(db, reader)
		case "5":
			wordFrequency(db, reader)
		case "6":
			postsByUniqueUsers(db, reader)
		case "7":
			return true
		case "8":
			return false
		default:
			fmt.Printf("%sНеверный выбор.%s\n", colorRed, colorReset)
		}
	}
}

func combinedSearch(db *sql.DB, reader *bufio.Reader) {
	keyword := prompt(reader, fmt.Sprintf("%sВведите поисковый запрос: %s", colorCyan, colorReset))
	if keyword == "" {
		return
	}

	exactResults := performExactSearch(db, keyword)
	excludeKeys := make(map[string]bool)
	for _, r := range exactResults {
		key := r.username + "|" + r.text + "|" + r.ts
		excludeKeys[key] = true
	}

	queries := strings.Fields(strings.ReplaceAll(keyword, ",", " "))
	fuzzyResults := performFuzzySearch(db, queries, excludeKeys)

	allResults := exactResults
	seen := make(map[string]bool)
	for _, r := range allResults {
		key := r.username + "|" + r.text + "|" + r.ts
		seen[key] = true
	}
	for _, r := range fuzzyResults {
		key := r.username + "|" + r.text + "|" + r.ts
		if !seen[key] {
			allResults = append(allResults, r)
			seen[key] = true
		}
	}

	sort.Slice(allResults, func(i, j int) bool {
		return allResults[i].ts > allResults[j].ts
	})

	printResults(allResults, keyword, "комбинированного поиска")

	if len(allResults) > 0 {
		offerSave(allResults, reader)
	}
}

func performExactSearch(db *sql.DB, keyword string) []commentRow {
	var results []commentRow
	rows, err := db.Query(
		"SELECT username, text, datetime(timestamp) as ts, post_url, post_code FROM comments WHERE LOWER(text) LIKE ? ORDER BY timestamp DESC",
		"%"+strings.ToLower(keyword)+"%",
	)
	if err != nil {
		return results
	}
	defer rows.Close()
	for rows.Next() {
		var r commentRow
		if err := rows.Scan(&r.username, &r.text, &r.ts, &r.postURL, &r.postCode); err != nil {
			continue
		}
		r.isExact = true
		r.matchWord = keyword
		results = append(results, r)
	}
	return results
}

func performFuzzySearch(db *sql.DB, queries []string, excludeKeys map[string]bool) []commentRow {
	var allComments []commentRow
	rows, err := db.Query("SELECT username, text, datetime(timestamp) as ts, post_url, post_code FROM comments ORDER BY timestamp DESC")
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var r commentRow
		rows.Scan(&r.username, &r.text, &r.ts, &r.postURL, &r.postCode)
		allComments = append(allComments, r)
	}

	var results []commentRow
	for _, c := range allComments {
		key := c.username + "|" + c.text + "|" + c.ts
		if excludeKeys != nil && excludeKeys[key] {
			continue
		}
		for _, q := range queries {
			if matchedWord, ok := containsFuzzyWord(c.text, q); ok {
				c.isExact = false
				c.matchWord = matchedWord
				results = append(results, c)
				break
			}
		}
	}
	return results
}

func printResults(results []commentRow, originalQuery, searchType string) {
	fmt.Printf("\n%sРезультаты %s для \"%s\" (%d):%s\n\n", colorGreen, searchType, originalQuery, len(results), colorReset)
	if len(results) == 0 {
		fmt.Printf("%sНичего не найдено.%s\n", colorYellow, colorReset)
		return
	}
	for _, r := range results {
		highlighted := r.text
		if r.isExact {
			lowerHighlighted := strings.ToLower(highlighted)
			lowerQuery := strings.ToLower(r.matchWord)
			start := strings.Index(lowerHighlighted, lowerQuery)
			if start >= 0 {
				highlighted = highlighted[:start] + "\033[7m" + highlighted[start:start+len(r.matchWord)] + "\033[0m" + highlighted[start+len(r.matchWord):]
			}
		} else {
			if r.matchWord != "" {
				highlighted = strings.ReplaceAll(highlighted, r.matchWord, "\033[7m"+r.matchWord+"\033[0m")
			}
		}
		fmt.Printf("%s[%s] %s%s\n", colorYellow, r.ts, r.username, colorReset)
		fmt.Printf("%s%s\n%s\n---\n", colorGreen, highlighted, r.postURL)
	}
}

func userComments(db *sql.DB, reader *bufio.Reader) {
	rows, err := db.Query("SELECT DISTINCT username FROM comments ORDER BY username")
	if err != nil {
		fmt.Printf("%sОшибка получения списка пользователей: %v%s\n", colorRed, err, colorReset)
		return
	}
	var users []string
	for rows.Next() {
		var u string
		rows.Scan(&u)
		users = append(users, u)
	}
	rows.Close()

	if len(users) == 0 {
		fmt.Printf("%sНет данных.%s\n", colorYellow, colorReset)
		return
	}

	fmt.Printf("\n%sУникальные пользователи (%d):%s\n", colorGreen, len(users), colorReset)
	for i, u := range users {
		fmt.Printf("  [%d] %s\n", i+1, u)
	}

	input := promptDefault(reader, fmt.Sprintf("%sВведите номер пользователя или имя (Enter – назад): %s", colorCyan, colorReset), "")
	if input == "" {
		return
	}

	var username string
	if num, err := strconv.Atoi(input); err == nil && num >= 1 && num <= len(users) {
		username = users[num-1]
	} else {
		username = input
	}

	var count int
	db.QueryRow("SELECT COUNT(*) FROM comments WHERE username = ?", username).Scan(&count)
	if count == 0 {
		fmt.Printf("%sПользователь '%s' не найден.%s\n", colorRed, username, colorReset)
		return
	}

	rows2, err := db.Query("SELECT username, text, datetime(timestamp) as ts, post_url, post_code FROM comments WHERE username = ? ORDER BY timestamp DESC", username)
	if err != nil {
		fmt.Printf("%sОшибка запроса: %v%s\n", colorRed, err, colorReset)
		return
	}
	defer rows2.Close()

	var results []commentRow
	for rows2.Next() {
		var r commentRow
		if err := rows2.Scan(&r.username, &r.text, &r.ts, &r.postURL, &r.postCode); err != nil {
			fmt.Printf("%sОшибка чтения: %v%s\n", colorRed, err, colorReset)
			continue
		}
		results = append(results, r)
	}

	fmt.Printf("\n%sКомментарии пользователя %s (%d):%s\n\n", colorGreen, username, len(results), colorReset)
	for _, r := range results {
		fmt.Printf("%s[%s]%s\n", colorYellow, r.ts, colorReset)
		fmt.Printf("%s%s\n%s\n---\n", colorGreen, r.text, r.postURL)
	}

	if len(results) > 0 {
		offerSave(results, reader)
	}
}

func topActiveUsers(db *sql.DB) {
	query := `
		SELECT username,
		       COUNT(*) AS cnt,
		       MIN(timestamp) AS first,
		       MAX(timestamp) AS last
		FROM comments
		GROUP BY username
		ORDER BY cnt DESC
	`
	rows, err := db.Query(query)
	if err != nil {
		fmt.Printf("%sОшибка запроса: %v%s\n", colorRed, err, colorReset)
		return
	}
	defer rows.Close()

	fmt.Printf("\n%sТоп активных пользователей:%s\n", colorGreen, colorReset)
	fmt.Printf("%-25s %8s  %-20s  %-20s\n", "Пользователь", "Коммент.", "Первый", "Последний")
	fmt.Println(strings.Repeat("─", 80))

	for rows.Next() {
		var username string
		var cnt int
		var first, last string
		if err := rows.Scan(&username, &cnt, &first, &last); err != nil {
			fmt.Printf("%sОшибка чтения строки: %v%s\n", colorRed, err, colorReset)
			continue
		}
		formatTS := func(ts string) string {
			t, err := time.Parse("2006-01-02T15:04:05Z", ts)
			if err != nil {
				t, err = time.Parse("2006-01-02 15:04:05", ts)
				if err != nil {
					return ts[:19]
				}
			}
			return t.Format("02.01.2006 15:04")
		}
		fmt.Printf("%-25s %8d  %-20s  %-20s\n", username, cnt, formatTS(first), formatTS(last))
	}
}

func commentsByPeriod(db *sql.DB, reader *bufio.Reader) {
	var minDate, maxDate string
	db.QueryRow("SELECT MIN(timestamp), MAX(timestamp) FROM comments").Scan(&minDate, &maxDate)

	fmt.Printf("\n%sДиапазон данных в базе: %s – %s%s\n", colorCyan, minDate[:10], maxDate[:10], colorReset)

	dateFrom := promptDefault(reader, fmt.Sprintf("%sНачальная дата (ГГГГ-ММ-ДД, Enter – %s): %s", colorCyan, minDate[:10], colorReset), minDate[:10])
	dateTo := promptDefault(reader, fmt.Sprintf("%sКонечная дата (ГГГГ-ММ-ДД, Enter – %s): %s", colorCyan, maxDate[:10], colorReset), maxDate[:10])

	if _, err := time.Parse("2006-01-02", dateFrom); err != nil {
		fmt.Printf("%sНеверный формат начальной даты. Использую %s.%s\n", colorRed, minDate[:10], colorReset)
		dateFrom = minDate[:10]
	}
	if _, err := time.Parse("2006-01-02", dateTo); err != nil {
		fmt.Printf("%sНеверный формат конечной даты. Использую %s.%s\n", colorRed, maxDate[:10], colorReset)
		dateTo = maxDate[:10]
	}

	query := `
		SELECT username, text, datetime(timestamp) as ts, post_url, post_code
		FROM comments
		WHERE timestamp >= ? AND timestamp <= ?
		ORDER BY timestamp DESC
	`
	rows, err := db.Query(query, dateFrom+" 00:00:00", dateTo+" 23:59:59")
	if err != nil {
		fmt.Printf("%sОшибка запроса: %v%s\n", colorRed, err, colorReset)
		return
	}
	defer rows.Close()

	var results []commentRow
	for rows.Next() {
		var r commentRow
		if err := rows.Scan(&r.username, &r.text, &r.ts, &r.postURL, &r.postCode); err != nil {
			fmt.Printf("%sОшибка чтения строки: %v%s\n", colorRed, err, colorReset)
			continue
		}
		results = append(results, r)
	}

	fmt.Printf("\n%sКомментарии за период %s – %s (%d):%s\n\n", colorGreen, dateFrom, dateTo, len(results), colorReset)
	if len(results) == 0 {
		fmt.Printf("%sНет комментариев за указанный период.%s\n", colorYellow, colorReset)
		return
	}

	for _, r := range results {
		fmt.Printf("%s[%s] %s%s\n", colorYellow, r.ts, r.username, colorReset)
		fmt.Printf("%s%s\n%s\n---\n", colorGreen, r.text, r.postURL)
	}

	offerSave(results, reader)
}

func wordFrequency(db *sql.DB, reader *bufio.Reader) {
	rows, err := db.Query("SELECT text FROM comments")
	if err != nil {
		fmt.Printf("%sОшибка запроса: %v%s\n", colorRed, err, colorReset)
		return
	}
	defer rows.Close()

	freq := make(map[string]int)
	totalWords := 0
	for rows.Next() {
		var text string
		rows.Scan(&text)
		words := tokenize(text)
		for _, w := range words {
			w = strings.ToLower(strings.TrimSpace(w))
			if len(w) < 3 || stopWords[w] {
				continue
			}
			freq[w]++
			totalWords++
		}
	}

	type wordCount struct {
		word string
		cnt  int
	}
	var list []wordCount
	for w, c := range freq {
		list = append(list, wordCount{w, c})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].cnt > list[j].cnt })

	fmt.Printf("\n%sЧастотный словарь (топ-50):%s\n", colorGreen, colorReset)
	fmt.Printf("%-25s %s\n", "Слово", "Частота")
	fmt.Println(strings.Repeat("─", 35))
	limit := 50
	if len(list) < limit {
		limit = len(list)
	}
	for i := 0; i < limit; i++ {
		fmt.Printf("%-25s %d\n", list[i].word, list[i].cnt)
	}
	fmt.Printf("\nВсего обработано слов: %d\n", totalWords)

	if promptYesNo(reader, fmt.Sprintf("%sСохранить словарь в CSV? (y/n): %s", colorCyan, colorReset)) {
		name := promptDefault(reader, fmt.Sprintf("%sИмя файла (без расширения) [dictionary]: %s", colorCyan, colorReset), "dictionary")
		filename := name + ".csv"
		f, err := os.Create(filename)
		if err != nil {
			fmt.Printf("%sОшибка создания файла: %v%s\n", colorRed, err, colorReset)
			return
		}
		defer f.Close()
		w := csv.NewWriter(f)
		defer w.Flush()
		w.Write([]string{"word", "count"})
		for _, wc := range list {
			w.Write([]string{wc.word, strconv.Itoa(wc.cnt)})
		}
		fmt.Printf("%sСловарь сохранён в %s%s\n", colorGreen, filename, colorReset)
	}
}

func postsByUniqueUsers(db *sql.DB, reader *bufio.Reader) {
	query := `
		SELECT post_code,
		       COUNT(*) AS total_comments,
		       COUNT(DISTINCT username) AS unique_users
		FROM comments
		WHERE post_code != ''
		GROUP BY post_code
		ORDER BY unique_users DESC
	`
	rows, err := db.Query(query)
	if err != nil {
		fmt.Printf("%sОшибка запроса: %v%s\n", colorRed, err, colorReset)
		return
	}
	defer rows.Close()

	type postStat struct {
		postCode string
		total    int
		unique   int
	}
	var stats []postStat
	for rows.Next() {
		var ps postStat
		if err := rows.Scan(&ps.postCode, &ps.total, &ps.unique); err != nil {
			fmt.Printf("%sОшибка чтения строки: %v%s\n", colorRed, err, colorReset)
			continue
		}
		stats = append(stats, ps)
	}

	fmt.Printf("\n%sПосты по количеству уникальных пользователей:%s\n", colorGreen, colorReset)
	if len(stats) == 0 {
		fmt.Printf("%sНет данных.%s\n", colorYellow, colorReset)
		return
	}
	fmt.Printf("%-20s %8s %12s\n", "Код поста", "Всего", "Уникальных")
	fmt.Println(strings.Repeat("─", 45))
	for _, s := range stats {
		fmt.Printf("%-20s %8d %12d\n", s.postCode, s.total, s.unique)
	}

	if len(stats) > 0 && promptYesNo(reader, fmt.Sprintf("%sСохранить результаты в CSV? (y/n): %s", colorCyan, colorReset)) {
		name := promptDefault(reader, fmt.Sprintf("%sИмя файла (без расширения) [posts_stats]: %s", colorCyan, colorReset), "posts_stats")
		filename := name + ".csv"
		f, err := os.Create(filename)
		if err != nil {
			fmt.Printf("%sОшибка создания файла: %v%s\n", colorRed, err, colorReset)
			return
		}
		defer f.Close()
		w := csv.NewWriter(f)
		defer w.Flush()
		w.Write([]string{"post_code", "total_comments", "unique_users"})
		for _, s := range stats {
			w.Write([]string{s.postCode, strconv.Itoa(s.total), strconv.Itoa(s.unique)})
		}
		fmt.Printf("%sСохранено в %s%s\n", colorGreen, filename, colorReset)
	}
}

func offerSave(results []commentRow, reader *bufio.Reader) {
	if !promptYesNo(reader, fmt.Sprintf("%sСохранить результаты? (y/n): %s", colorCyan, colorReset)) {
		return
	}

	format := strings.ToLower(promptDefault(reader, fmt.Sprintf("%sФормат сохранения (csv/db/both) [db]: %s", colorCyan, colorReset), "db"))
	switch format {
	case "csv":
		name := promptDefault(reader, fmt.Sprintf("%sИмя файла (без расширения) [export]: %s", colorCyan, colorReset), "export")
		filename := name + ".csv"
		if err := saveResultsToCSV(results, filename); err != nil {
			fmt.Printf("%sОшибка сохранения CSV: %v%s\n", colorRed, err, colorReset)
		} else {
			fmt.Printf("%sРезультаты сохранены в %s%s\n", colorGreen, filename, colorReset)
		}
	case "db":
		name := promptDefault(reader, fmt.Sprintf("%sИмя файла (без расширения) [export]: %s", colorCyan, colorReset), "export")
		filename := name + ".db"
		if err := saveResultsToDB(results, filename); err != nil {
			fmt.Printf("%sОшибка сохранения базы: %v%s\n", colorRed, err, colorReset)
		} else {
			fmt.Printf("%sРезультаты сохранены в %s%s\n", colorGreen, filename, colorReset)
		}
	case "both":
		name := promptDefault(reader, fmt.Sprintf("%sБазовое имя файлов (без расширения) [export]: %s", colorCyan, colorReset), "export")
		csvFile := name + ".csv"
		dbFile := name + ".db"
		if err := saveResultsToCSV(results, csvFile); err != nil {
			fmt.Printf("%sОшибка сохранения CSV: %v%s\n", colorRed, err, colorReset)
		} else {
			fmt.Printf("%sРезультаты сохранены в %s%s\n", colorGreen, csvFile, colorReset)
		}
		if err := saveResultsToDB(results, dbFile); err != nil {
			fmt.Printf("%sОшибка сохранения базы: %v%s\n", colorRed, err, colorReset)
		} else {
			fmt.Printf("%sРезультаты сохранены в %s%s\n", colorGreen, dbFile, colorReset)
		}
	default:
		fmt.Printf("%sНеизвестный формат. Сохранение отменено.%s\n", colorRed, colorReset)
	}
}

func saveResultsToCSV(results []commentRow, filename string) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	w.Write([]string{"username", "text", "timestamp", "post_url", "post_code"})
	for _, r := range results {
		w.Write([]string{r.username, r.text, r.ts, r.postURL, r.postCode})
	}
	return nil
}

func saveResultsToDB(results []commentRow, filename string) error {
	os.Remove(filename)
	db, err := sql.Open("sqlite3", filename)
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS comments (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			text TEXT,
			timestamp DATETIME NOT NULL,
			post_code TEXT NOT NULL,
			post_url TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR IGNORE INTO comments (id, username, text, timestamp, post_code, post_url)
		VALUES (?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range results {
		id := fmt.Sprintf("%x", r.username+r.text+r.ts)
		if _, err = stmt.Exec(id, r.username, r.text, r.ts, r.postCode, r.postURL); err != nil {
			return err
		}
	}
	return tx.Commit()
}
