package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"inst/internal/app"
	"inst/internal/browser"
	"inst/internal/config"
	"inst/internal/logging"
	"inst/internal/pipeline"
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
                        I N S T A G R A M   S C R A P E R     
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

func colorize(text, color string) string {
	if !isTerminal() {
		return text
	}
	return color + text + colorReset
}

func coloredPrint(color, text string) {
	if isTerminal() {
		fmt.Print(color, text, colorReset)
	} else {
		fmt.Print(text)
	}
}

func showProgress(ctx context.Context, progress <-chan pipeline.ProgressUpdate, out io.Writer, useColor bool) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var total int
	var processed int
	var comments int
	var errors int

	for {
		select {
		case <-ctx.Done():
			return
		case p, ok := <-progress:
			if !ok {
				return
			}
			if p.Total > 0 {
				total = p.Total
			}
			processed += p.ProcessedIncrement
			comments += p.CommentsIncrement
			errors += p.ErrorIncrement
		case <-ticker.C:
		}
		if total == 0 {
			continue
		}

		greenCount := processed - errors
		if greenCount < 0 {
			greenCount = 0
		}

		var barBuilder strings.Builder
		for i := 0; i < processed; i++ {
			if i < greenCount {
				if useColor {
					barBuilder.WriteString(colorGreen)
				}
				barBuilder.WriteByte('=')
			} else {
				if useColor {
					barBuilder.WriteString(colorRed)
				}
				barBuilder.WriteByte('=')
			}
		}
		if processed < total {
			barBuilder.WriteByte('>')
		}
		if useColor {
			barBuilder.WriteString(colorReset)
		}
		barStr := barBuilder.String()

		percent := processed * 100 / total
		line := fmt.Sprintf("\r[%s] %3d%% | Постов: %d/%d | Комментариев: %d | Ошибок: %d",
			barStr, percent, processed, total, comments, errors)
		fmt.Fprint(out, line)
	}
}

func safeReadString(reader *bufio.Reader) (string, error) {
	text, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(text), nil
}

func promptRequired(reader *bufio.Reader, question string) string {
	for {
		coloredPrint(colorCyan, question)
		text, err := safeReadString(reader)
		if err != nil {
			fmt.Printf("\nОшибка ввода: %v\n", err)
			os.Exit(1)
		}
		if text != "" {
			return text
		}
		coloredPrint(colorRed, "Ошибка: значение не может быть пустым. Повторите ввод.\n")
	}
}

func promptWithDefault(reader *bufio.Reader, question string, defaultVal string) string {
	coloredPrint(colorCyan, question)
	text, err := safeReadString(reader)
	if err != nil {
		fmt.Printf("\nОшибка ввода: %v\n", err)
		os.Exit(1)
	}
	if text == "" {
		return defaultVal
	}
	return text
}

func promptWithValidation(reader *bufio.Reader, question string, validate func(string) error) string {
	for {
		coloredPrint(colorCyan, question)
		text, err := safeReadString(reader)
		if err != nil {
			fmt.Printf("\nОшибка ввода: %v\n", err)
			os.Exit(1)
		}
		if err := validate(text); err != nil {
			coloredPrint(colorRed, fmt.Sprintf("Ошибка: %v. Повторите ввод.\n", err))
			continue
		}
		return text
	}
}

func promptInt(reader *bufio.Reader, question string, defaultVal int) int {
	for {
		coloredPrint(colorCyan, question)
		text, err := safeReadString(reader)
		if err != nil {
			fmt.Printf("\nОшибка ввода: %v\n", err)
			os.Exit(1)
		}
		if text == "" {
			return defaultVal
		}
		val, err := strconv.Atoi(text)
		if err != nil {
			coloredPrint(colorRed, "Ошибка: введите целое число.\n")
			continue
		}
		return val
	}
}

func promptBool(reader *bufio.Reader, question string) bool {
	for {
		coloredPrint(colorCyan, question)
		text, err := safeReadString(reader)
		if err != nil {
			fmt.Printf("\nОшибка ввода: %v\n", err)
			os.Exit(1)
		}
		text = strings.ToLower(text)
		if text == "" || text == "n" || text == "no" {
			return false
		}
		if text == "y" || text == "yes" {
			return true
		}
		coloredPrint(colorRed, "Ошибка: введите 'y' (да) или 'n' (нет).\n")
	}
}

func main() {
	enableVirtualTerminal()

	var (
		username    = flag.String("u", "", "Instagram username")
		password    = flag.String("p", "", "Instagram password (env: INSTA_PASSWORD)")
		targets     = flag.String("t", "", "Target usernames (comma separated)")
		account     = flag.String("a", "", "Target account username")
		maxPosts    = flag.Int("max-posts", 0, "Maximum posts from profile")
		postFile    = flag.String("f", "", "File with post URLs")
		output      = flag.String("o", "", "Output file")
		concurrency = flag.Int("c", 0, "Concurrency")
		noCookies   = flag.Bool("no-cookies", false, "Ignore existing cookies")
		storageMode = flag.String("storage", "file", "Storage mode: file, db, both")
		configPath  = flag.String("config", "", "Path to config file (auto-detected if empty)")
		help        = flag.Bool("help", false, "Show help")
	)
	flag.Parse()

	if *password == "" {
		*password = os.Getenv("INSTA_PASSWORD")
	}

	if len(os.Args) == 1 {
		runInteractive()
		return
	}

	if *help {
		flag.Usage()
		return
	}
	if *account == "" && (*targets == "" && *postFile == "") {
		fmt.Fprintln(os.Stderr, "Необходимо указать --account или --targets и/или --file")
		flag.Usage()
		os.Exit(1)
	}

	cfgPath, err := resolveConfigPath(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Не удалось найти конфигурационный файл: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Используется конфиг: %s\n", cfgPath)

	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка загрузки конфигурации: %v\n", err)
		os.Exit(1)
	}

	if *output != "" {
		cfg.Scraper.OutputFile = *output
	}
	if *concurrency > 0 {
		cfg.Scraper.Concurrency = *concurrency
	}

	opts := app.Options{
		Username:    *username,
		Password:    *password,
		TargetUsers: *targets,
		Account:     *account,
		MaxPosts:    *maxPosts,
		PostFile:    *postFile,
		NoCookies:   *noCookies,
		StorageMode: *storageMode,
		Interactive: false,
	}

	if err := runApp(cfg, opts, false, nil, false); err != nil {
		os.Exit(1)
	}
}

func runInteractive() {
	useColor := isTerminal()

	if useColor {
		fmt.Print("\033[H\033[2J")
	}

	coloredPrint(colorCyan, banner)
	fmt.Println()
	coloredPrint(colorCyan, "                  	        Добро пожаловать!\n")
	fmt.Println("   	    SkyScraper — сборщик данных Instagram (интерактивный режим)")
	fmt.Println("   		     Для выхода в любой момент нажмите Ctrl+C\n")
	fmt.Println(strings.Repeat("─", 82))

	reader := bufio.NewReader(os.Stdin)

	cfgPath := promptWithValidation(reader, "Путь к конфигурационному файлу (Enter — автоопределение): ", func(input string) error {
		if input == "" {
			return nil
		}
		if _, err := os.Stat(input); os.IsNotExist(err) {
			return fmt.Errorf("файл не существует: %s", input)
		}
		return nil
	})
	if cfgPath == "" {
		var err error
		cfgPath, err = resolveConfigPath("")
		if err != nil {
			fmt.Println(colorize("Ошибка автоопределения конфига: "+err.Error(), colorRed))
			waitForExit()
			return
		}
	}
	fmt.Println(colorize("Используется конфиг: "+cfgPath, colorGreen) + "\n")

	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Println(colorize("Ошибка загрузки конфигурации: "+err.Error(), colorRed))
		waitForExit()
		return
	}

	fmt.Println(colorize("── Учётные данные ──", colorCyan))
	username := promptRequired(reader, "Логин Instagram: ")
	password := promptRequired(reader, "Пароль: ")

	fmt.Println("\n" + colorize("── Выбор цели ──", colorCyan))
	fmt.Println("Можно указать аккаунт для сбора (режим 'account'),")
	fmt.Println("или список пользователей и/или файл с постами (режим 'targets').")
	mode := promptWithValidation(reader, "Режим (account/targets): ", func(input string) error {
		input = strings.ToLower(strings.TrimSpace(input))
		if input != "account" && input != "targets" {
			return fmt.Errorf("введите 'account' или 'targets'")
		}
		return nil
	})

	var account, targets, postFile string
	if mode == "account" {
		account = promptRequired(reader, "Введите имя аккаунта для сбора: ")
	} else {
		targets = promptWithDefault(reader, "Целевые пользователи (через запятую) [Enter — пропустить]: ", "")
		postFile = promptWithDefault(reader, "Файл со ссылками на посты [Enter — пропустить]: ", "")
		if targets == "" && postFile == "" {
			fmt.Println(colorize("Ошибка: нужно указать хотя бы одно: цели или файл с постами.", colorRed))
			waitForExit()
			return
		}
	}

	maxPosts := promptInt(reader, fmt.Sprintf("Максимум постов (0 — все) [по умолчанию: %d]: ", 0), 0)

	defaultOutput := cfg.Scraper.OutputFile
	output := promptWithDefault(reader, fmt.Sprintf("Выходной файл [по умолчанию: %s]: ", defaultOutput), defaultOutput)
	cfg.Scraper.OutputFile = output

	defaultConcurrency := cfg.Scraper.Concurrency
	concurrency := promptInt(reader, fmt.Sprintf("Число параллельных потоков (1-...) [по умолчанию: %d]: ", defaultConcurrency), defaultConcurrency)
	cfg.Scraper.Concurrency = concurrency

	noCookies := promptBool(reader, "Игнорировать cookies? (y/n) [по умолчанию: n]: ")

	storageMode := promptWithValidation(reader, "Режим хранения (file/db/both) [по умолчанию: file]: ", func(input string) error {
		if input == "" {
			return nil
		}
		input = strings.ToLower(strings.TrimSpace(input))
		if input != "file" && input != "db" && input != "both" {
			return fmt.Errorf("допустимые значения: file, db, both")
		}
		return nil
	})
	if storageMode == "" {
		storageMode = "file"
	}

	if username == "" || password == "" {
		fmt.Println(colorize("Ошибка: логин и пароль обязательны.", colorRed))
		waitForExit()
		return
	}

	opts := app.Options{
		Username:    username,
		Password:    password,
		TargetUsers: targets,
		Account:     account,
		MaxPosts:    maxPosts,
		PostFile:    postFile,
		NoCookies:   noCookies,
		StorageMode: storageMode,
		Interactive: true,
	}

	realStdout := os.Stdout
	realStderr := os.Stderr
	devNull, _ := os.Open(os.DevNull)
	os.Stdout = devNull
	os.Stderr = devNull
	defer func() {
		os.Stdout = realStdout
		os.Stderr = realStderr
		devNull.Close()
	}()

	fmt.Fprintln(realStdout, "\n"+colorize("Запуск сбора данных...", colorGreen))
	if err := runApp(cfg, opts, true, realStdout, useColor); err != nil {
		os.Exit(1)
	}
	waitForExit()
}

func runApp(cfg *config.Config, opts app.Options, interactive bool, progressOut io.Writer, useColor bool) (runErr error) {
	defer func() {
		if r := recover(); r != nil {
			logging.ErrorLogger.Printf("Критическая паника при параметрах: account=%q targets=%q maxPosts=%d postFile=%q storage=%q interactive=%v\n%s",
				opts.Account, opts.TargetUsers, opts.MaxPosts, opts.PostFile, opts.StorageMode, interactive, debug.Stack())
			runErr = fmt.Errorf("критическая паника: %v", r)
		}
	}()

	if !opts.Interactive && (opts.Username == "" || opts.Password == "") {
		return fmt.Errorf("не заданы учётные данные (используйте -u и -p или переменную окружения INSTA_PASSWORD)")
	}

	application, err := app.New(cfg, opts)
	if err != nil {
		logging.ErrorLogger.Printf("Ошибка создания приложения: %v", err)
		return fmt.Errorf("ошибка создания приложения: %w", err)
	}
	defer application.Close()

	if interactive && progressOut != nil {
		printRunInfo(cfg, opts, progressOut)
	} else {
		printRunInfo(cfg, opts, os.Stderr)
	}

	if interactive && progressOut != nil {
		progressCh := make(chan pipeline.ProgressUpdate, 10)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go showProgress(ctx, progressCh, progressOut, useColor)

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			sig := <-sigCh
			logging.InfoLogger.Printf("Получен сигнал завершения: %v", sig)
			cancel()

			time.AfterFunc(2*time.Second, func() {
				logging.WarnLogger.Println("Принудительное завершение браузеров по таймауту")
				browser.KillAllChromeProcesses()
				os.Exit(1)
			})
		}()

		err = application.RunWithProgress(ctx, progressCh)
		if err != nil {
			fmt.Fprintf(progressOut, "\n%sОшибка выполнения: %s%s\n",
				colorize("", colorRed), formatError(err), colorize("", colorReset))
			return err
		}
		fmt.Fprintf(progressOut, "\n%sГотово!%s\n",
			colorize(colorGreen, ""), colorize(colorReset, ""))
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logging.InfoLogger.Printf("Получен сигнал завершения: %v", sig)
		cancel()
		time.AfterFunc(2*time.Second, func() {
			logging.WarnLogger.Println("Принудительное завершение браузеров по таймауту")
			browser.KillAllChromeProcesses()
			os.Exit(1)
		})
	}()

	if err := application.Run(ctx); err != nil {
		logging.ErrorLogger.Printf("Ошибка выполнения: %v", err)
		return fmt.Errorf("ошибка выполнения: %w", err)
	}
	return nil
}

func printRunInfo(cfg *config.Config, opts app.Options, out io.Writer) {
	fmt.Fprintln(out, "══════════════════════════════════════════════════════")
	if opts.Interactive {
		fmt.Fprintln(out, "  Режим: интерактивный")
	} else {
		fmt.Fprintln(out, "  Режим: командная строка")
	}
	if opts.Account != "" {
		fmt.Fprintf(out, "  Цель (аккаунт): %s\n", opts.Account)
	} else if opts.TargetUsers != "" {
		fmt.Fprintf(out, "  Цель (пользователи): %s\n", opts.TargetUsers)
	} else if opts.PostFile != "" {
		fmt.Fprintf(out, "  Цель (файл с постами): %s\n", opts.PostFile)
	}
	fmt.Fprintf(out, "  Максимум постов: %d (0 – без ограничений)\n", opts.MaxPosts)
	fmt.Fprintf(out, "  Выходной файл: %s\n", cfg.Scraper.OutputFile)
	fmt.Fprintf(out, "  Параллелизм: %d\n", cfg.Scraper.Concurrency)
	fmt.Fprintf(out, "  Хранилище: %s\n", opts.StorageMode)
	fmt.Fprintf(out, "  Куки: %s\n", map[bool]string{true: "игнорировать", false: "использовать"}[opts.NoCookies])
	fmt.Fprintln(out, "══════════════════════════════════════════════════════")
}

func formatError(err error) string {
	var parts []string
	for e := err; e != nil; e = errors.Unwrap(e) {
		parts = append(parts, e.Error())
	}
	return strings.Join(parts, " → ")
}

func waitForExit() {
	fmt.Fprintln(os.Stdout, colorize("Данные сохранены. Нажмите Enter или Ctrl+C для выхода.", colorYellow))

	enterCh := make(chan struct{})
	go func() {
		bufio.NewReader(os.Stdin).ReadBytes('\n')
		close(enterCh)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case <-enterCh:
	case <-sigCh:
	}
}

func resolveConfigPath(flagPath string) (string, error) {
	if flagPath != "" {
		if _, err := os.Stat(flagPath); err == nil {
			return flagPath, nil
		}
		return "", fmt.Errorf("файл по флагу -config не найден: %s", flagPath)
	}

	exeDir, err := executableDir()
	if err == nil {
		candidates := []string{
			filepath.Join(exeDir, "config.yaml"),
			filepath.Join(exeDir, "configs", "config.yaml"),
		}
		for _, p := range candidates {
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}

	wd, _ := os.Getwd()
	candidates := []string{
		filepath.Join(wd, "config.yaml"),
		filepath.Join(wd, "configs", "config.yaml"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("не найден config.yaml ни рядом с exe, ни в рабочей директории")
}

func executableDir() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exePath), nil
}
