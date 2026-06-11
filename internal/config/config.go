package config

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration time.Duration

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("невалидная длительность '%s': %w", s, err)
	}
	*d = Duration(dur)
	return nil
}

func (d Duration) ToDuration() time.Duration { return time.Duration(d) }

type ScraperConfig struct {
	BaseURL           string   `yaml:"base_url"`
	Timeout           Duration `yaml:"timeout"`
	Retries           int      `yaml:"retries"`
	UserAgents        []string `yaml:"user_agents"`
	ProxyList         []string `yaml:"proxy_list"`
	BrowserPoolSize   int      `yaml:"browser_pool_size"`
	MaxUsesPerBrowser int      `yaml:"max_uses_per_browser"`
	MaxTabsPerBrowser int      `yaml:"max_tabs_per_browser"`
	Concurrency       int      `yaml:"concurrency"`
	OutputFile        string   `yaml:"output_file"`
	CookiesFile       string   `yaml:"cookies_file"`
}

type DatabaseConfig struct {
	Driver       string `yaml:"driver"`
	Host         string `yaml:"host"`
	Port         int    `yaml:"port"`
	User         string `yaml:"user"`
	Password     string `yaml:"password"`
	DBName       string `yaml:"dbname"`
	SSLMode      string `yaml:"sslmode"`
	MaxOpenConns int    `yaml:"max_open_conns"`
	MaxIdleConns int    `yaml:"max_idle_conns"`
}

type LoggingConfig struct {
	Level     string `yaml:"level"`
	InfoPath  string `yaml:"info_path"`
	ErrorPath string `yaml:"error_path"`
}

type Config struct {
	Scraper  ScraperConfig  `yaml:"scraper"`
	Database DatabaseConfig `yaml:"database"`
	Logging  LoggingConfig  `yaml:"logging"`
}

var cfgLogger = log.New(io.Discard, "[CONFIG] ", log.LstdFlags|log.Lmsgprefix)

func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("открытие конфига: %w", err)
	}
	defer f.Close()

	var cfg Config
	if err := yaml.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("декодирование YAML: %w", err)
	}

	if cfgLogger.Writer() == io.Discard {
		if f, err := os.OpenFile(cfg.Logging.InfoPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
			cfgLogger = log.New(f, "[CONFIG] ", log.LstdFlags|log.Lmsgprefix)
		}
	}

	cfgLogger.Printf("Загрузка конфигурации из %s", path)
	setDefaults(&cfg)
	normalizePaths(&cfg, filepath.Dir(path))

	cfgLogger.Println("Конфигурация успешно загружена и дополнена значениями по умолчанию")
	return &cfg, nil
}

func normalizePaths(cfg *Config, configDir string) {
	if cfg.Scraper.CookiesFile != "" && !filepath.IsAbs(cfg.Scraper.CookiesFile) {
		newPath := filepath.Join(configDir, cfg.Scraper.CookiesFile)
		cfgLogger.Printf("Нормализация CookiesFile: %s -> %s", cfg.Scraper.CookiesFile, newPath)
		cfg.Scraper.CookiesFile = newPath
	}
	if cfg.Scraper.OutputFile != "" && !filepath.IsAbs(cfg.Scraper.OutputFile) {
		newPath := filepath.Join(configDir, cfg.Scraper.OutputFile)
		cfgLogger.Printf("Нормализация OutputFile: %s -> %s", cfg.Scraper.OutputFile, newPath)
		cfg.Scraper.OutputFile = newPath
	}
}

func setDefaults(cfg *Config) {
	applyIfEmpty := func(field *string, name, value string) {
		if *field == "" {
			*field = value
			cfgLogger.Printf("%s не задан, установлено значение по умолчанию: %q", name, value)
		}
	}
	applyIntIfZero := func(field *int, name string, value int) {
		if *field <= 0 {
			*field = value
			cfgLogger.Printf("%s не задан или <=0, установлено значение по умолчанию: %d", name, value)
		}
	}
	applyDurationIfZero := func(field *Duration, name string, value time.Duration) {
		if field.ToDuration() == 0 {
			*field = Duration(value)
			cfgLogger.Printf("%s не задан, установлено значение по умолчанию: %s", name, value)
		}
	}

	applyIfEmpty(&cfg.Scraper.BaseURL, "BaseURL", "https://www.instagram.com")
	applyIntIfZero(&cfg.Scraper.Retries, "Retries", 3)
	applyIntIfZero(&cfg.Scraper.BrowserPoolSize, "BrowserPoolSize", 2)
	applyIntIfZero(&cfg.Scraper.MaxUsesPerBrowser, "MaxUsesPerBrowser", 10)
	applyIntIfZero(&cfg.Scraper.MaxTabsPerBrowser, "MaxTabsPerBrowser", 2)
	applyIntIfZero(&cfg.Scraper.Concurrency, "Concurrency", 3)
	applyIfEmpty(&cfg.Scraper.OutputFile, "OutputFile", "comments.jsonl")
	applyIfEmpty(&cfg.Scraper.CookiesFile, "CookiesFile", "cookies.json")
	applyDurationIfZero(&cfg.Scraper.Timeout, "Timeout", 30*time.Second)

	applyIfEmpty(&cfg.Database.Driver, "Database.Driver", "sqlite3")
	applyIfEmpty(&cfg.Database.DBName, "Database.DBName", "comments.db")
	applyIntIfZero(&cfg.Database.Port, "Database.Port", 5432)
	applyIfEmpty(&cfg.Database.SSLMode, "Database.SSLMode", "disable")
	applyIntIfZero(&cfg.Database.MaxOpenConns, "MaxOpenConns", 10)
	applyIntIfZero(&cfg.Database.MaxIdleConns, "MaxIdleConns", 5)

	applyIfEmpty(&cfg.Logging.Level, "Logging.Level", "info")
	applyIfEmpty(&cfg.Logging.InfoPath, "InfoPath", "logs/scraper.log")
	applyIfEmpty(&cfg.Logging.ErrorPath, "ErrorPath", "logs/errors.log")
}
