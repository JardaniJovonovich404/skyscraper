package app

import (
	"bufio"
	"context"
	"fmt"
	"inst/internal/browser"
	"inst/internal/config"
	"inst/internal/logging"
	"inst/internal/pipeline"
	"inst/internal/proxy"
	"inst/internal/scraper"
	"inst/internal/storage"
	"net/http"
	"os"
	"strings"

	"golang.org/x/term"
)

type Options struct {
	Username    string
	Password    string
	TargetUsers string
	Account     string
	MaxPosts    int
	PostFile    string
	NoCookies   bool
	StorageMode string
	Interactive bool
}

type App struct {
	cfg          *config.Config
	opts         Options
	proxyManager *proxy.Manager
	browserPool  *browser.Pool
	collector    scraper.Collector
	storages     []storage.Storage
	pipeline     *pipeline.Pipeline
	cookies      []*http.Cookie
	interactive  bool
}

func New(cfg *config.Config, opts Options) (*App, error) {
	logCfg := logging.Config{
		Level:     cfg.Logging.Level,
		InfoPath:  cfg.Logging.InfoPath,
		ErrorPath: cfg.Logging.ErrorPath,
	}
	if err := logging.Configure(logCfg); err != nil {
		return nil, fmt.Errorf("логирование: %w", err)
	}

	logging.InfoLogger.Println("Инициализация приложения...")

	proxyMgr := proxy.NewManager(cfg.Scraper.ProxyList)

	logging.InfoLogger.Println("Поиск сохранённой сессии...")
	jar, errLoad := scraper.LoadCookies(cfg.Scraper.CookiesFile)
	if errLoad != nil {
		logging.WarnLogger.Printf("Ошибка загрузки кук: %v", errLoad)
	}
	validSession := jar != nil && scraper.CheckSession(jar)

	if opts.NoCookies {
		validSession = false
	}

	var cookies []*http.Cookie
	if validSession {
		cookies = scraper.CookiesFromJar(jar, "https://www.instagram.com")
		logging.InfoLogger.Println("Найдена живая сессия, вход не требуется.")
	} else {
		username := opts.Username
		password := opts.Password
		if username == "" {
			fmt.Print("Логин: ")
			if _, err := fmt.Scanln(&username); err != nil {
				return nil, fmt.Errorf("ошибка чтения логина: %w", err)
			}
		}
		if password == "" {
			fmt.Print("Пароль: ")
			bytePass, err := term.ReadPassword(int(os.Stdin.Fd()))
			if err != nil {
				return nil, fmt.Errorf("ошибка чтения пароля: %w", err)
			}
			password = strings.TrimSpace(string(bytePass))
			fmt.Println()
		}

		logging.InfoLogger.Println("Требуется вход в Instagram, создаю временный браузер...")
		tempPool := browser.NewPool(1, proxyMgr, 0, cfg.Scraper.UserAgents, nil, 1)
		defer tempPool.Close()
		var err error
		cookies, err = scraper.Login(context.Background(), tempPool, username, password)
		if err != nil {
			logging.ErrorLogger.Printf("Аутентификация не удалась: %v", err)
			return nil, fmt.Errorf("аутентификация: %w", err)
		}
		if errSave := scraper.SaveCookies(cfg.Scraper.CookiesFile, cookies); errSave != nil {
			logging.WarnLogger.Printf("Не удалось сохранить куки: %v", errSave)
		} else {
			logging.InfoLogger.Println("Вход выполнен, куки сохранены.")
		}
	}

	poolSize := cfg.Scraper.BrowserPoolSize
	if poolSize < 1 {
		poolSize = 1
	}
	concurrency := cfg.Scraper.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}

	maxTabsPerBrowser := cfg.Scraper.MaxTabsPerBrowser
	if maxTabsPerBrowser <= 0 {
		maxTabsPerBrowser = 2
	}

	logging.InfoLogger.Printf("Параметры пула: макс. вкладок (size)=%d, воркеров=%d, вкладок на браузер=%d",
		poolSize, concurrency, maxTabsPerBrowser)

	if poolSize > concurrency {
		logging.WarnLogger.Printf("browser_pool_size (%d) больше concurrency (%d) – лишние вкладки не будут использованы",
			poolSize, concurrency)
	}

	pool := browser.NewPool(
		poolSize,
		proxyMgr,
		cfg.Scraper.MaxUsesPerBrowser,
		cfg.Scraper.UserAgents,
		cookies,
		maxTabsPerBrowser,
	)
	logging.InfoLogger.Println("Пул браузеров создан")

	targetUsers := parseTargets(opts.TargetUsers)
	collector := scraper.NewCollector(pool, targetUsers)

	storages, err := initStorages(cfg, opts.StorageMode)
	if err != nil {
		logging.ErrorLogger.Printf("Ошибка инициализации хранилищ: %v", err)
		pool.Close()
		return nil, err
	}

	pl := pipeline.New(collector, storages, concurrency)
	logging.InfoLogger.Println("Pipeline создан")

	app := &App{
		cfg:          cfg,
		opts:         opts,
		proxyManager: proxyMgr,
		browserPool:  pool,
		collector:    collector,
		storages:     storages,
		pipeline:     pl,
		cookies:      cookies,
		interactive:  opts.Interactive,
	}
	logging.InfoLogger.Println("Приложение успешно инициализировано")
	return app, nil
}

func (a *App) Run(ctx context.Context) error {
	urls, err := a.getPostURLs(ctx)
	if err != nil {
		return err
	}
	if len(urls) == 0 {
		logging.WarnLogger.Println("Нет постов для обработки")
		return nil
	}
	logging.InfoLogger.Printf("Получено %d URL постов", len(urls))
	return a.pipeline.Execute(ctx, urls)
}

func (a *App) RunWithProgress(ctx context.Context, progress chan<- pipeline.ProgressUpdate) error {
	urls, err := a.getPostURLs(ctx)
	if err != nil {
		return err
	}
	if len(urls) == 0 {
		logging.WarnLogger.Println("Нет постов для обработки")
		return nil
	}
	logging.InfoLogger.Printf("Получено %d URL постов", len(urls))
	return a.pipeline.ExecuteWithProgress(ctx, urls, progress)
}

func (a *App) Close() {
	logging.InfoLogger.Println("Закрытие приложения...")
	if a.browserPool != nil {
		a.browserPool.Close()
	}
	for _, s := range a.storages {
		s.Close()
	}
	logging.InfoLogger.Println("Все ресурсы освобождены")
}

func (a *App) getPostURLs(ctx context.Context) ([]string, error) {
	if a.opts.Account != "" {
		logging.InfoLogger.Printf("Сбор постов для профиля: %s (макс. %d)", a.opts.Account, a.opts.MaxPosts)
		profilePool := browser.NewPool(1, a.proxyManager, 0, a.cfg.Scraper.UserAgents, a.cookies, 1)
		defer profilePool.Close()
		urls, err := scraper.GetPostsFromProfile(ctx, profilePool, a.opts.Account, a.opts.MaxPosts)
		if err != nil {
			return nil, err
		}
		return urls, nil
	}

	if a.opts.PostFile != "" {
		logging.InfoLogger.Printf("Сбор постов из файла: %s", a.opts.PostFile)
		urls, err := loadPostURLs(a.opts.PostFile)
		if err != nil {
			return nil, err
		}
		return urls, nil
	}

	targetUsers := parseTargets(a.opts.TargetUsers)
	if len(targetUsers) > 0 {
		logging.InfoLogger.Printf("Сбор постов для целевых пользователей: %v", targetUsers)
		profilePool := browser.NewPool(1, a.proxyManager, 0, a.cfg.Scraper.UserAgents, a.cookies, 1)
		defer profilePool.Close()
		var allURLs []string
		for _, user := range targetUsers {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
			urls, err := scraper.GetPostsFromProfile(ctx, profilePool, user, a.opts.MaxPosts)
			if err != nil {
				logging.WarnLogger.Printf("Ошибка сбора постов для %s: %v", user, err)
				continue
			}
			allURLs = append(allURLs, urls...)
		}
		if len(allURLs) == 0 {
			return nil, fmt.Errorf("не удалось получить ни одного поста для указанных пользователей")
		}
		return allURLs, nil
	}

	return nil, fmt.Errorf("не задан ни аккаунт, ни файл с постами, ни целевые пользователи")
}

func parseTargets(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var res []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			res = append(res, p)
		}
	}
	return res
}

func initStorages(cfg *config.Config, mode string) ([]storage.Storage, error) {
	logging.InfoLogger.Printf("Инициализация хранилищ, режим: %s", mode)
	var storages []storage.Storage

	if mode == "db" || mode == "both" {
		driver := cfg.Database.Driver
		if driver == "" {
			driver = "sqlite3"
		}
		switch driver {
		case "sqlite3", "sqlite":
			sqliteStorage, err := storage.NewSQLiteStorage(&cfg.Database)
			if err != nil {
				return nil, err
			}
			storages = append(storages, sqliteStorage)
		default:
			return nil, fmt.Errorf("неподдерживаемый драйвер БД: %s", driver)
		}
	}
	if mode == "file" || mode == "both" {
		fs, err := storage.NewFileStorage(cfg.Scraper.OutputFile)
		if err != nil {
			for _, s := range storages {
				s.Close()
			}
			return nil, err
		}
		storages = append(storages, fs)
	}
	return storages, nil
}

func loadPostURLs(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var urls []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			urls = append(urls, line)
		}
	}
	return urls, scanner.Err()
}
