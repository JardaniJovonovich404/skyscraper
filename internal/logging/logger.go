package logging

import (
	"fmt"
	"log"
	"os"
	"strings"
)

type Level int

const (
	DEBUG Level = iota
	INFO
	WARN
	ERROR
)

type Config struct {
	Level     string
	InfoPath  string
	ErrorPath string
}

var (
	InfoLogger  *log.Logger
	WarnLogger  *log.Logger
	ErrorLogger *log.Logger
	DebugLogger *log.Logger

	currentLevel Level = INFO
)

func Configure(cfg Config) error {
	if err := os.MkdirAll(getDir(cfg.InfoPath), 0755); err != nil {
		return fmt.Errorf("создание папки для info-лога: %w", err)
	}
	if err := os.MkdirAll(getDir(cfg.ErrorPath), 0755); err != nil {
		return fmt.Errorf("создание папки для error-лога: %w", err)
	}

	infoFile, err := os.OpenFile(cfg.InfoPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil {
		return err
	}
	errorFile, err := os.OpenFile(cfg.ErrorPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil {
		infoFile.Close()
		return err
	}

	InfoLogger = log.New(infoFile, "INFO: ", log.Ldate|log.Ltime|log.Lshortfile)
	WarnLogger = log.New(infoFile, "WARN: ", log.Ldate|log.Ltime|log.Lshortfile)
	ErrorLogger = log.New(errorFile, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
	DebugLogger = log.New(infoFile, "DEBUG: ", log.Ldate|log.Ltime|log.Lshortfile)

	levelStr := strings.ToUpper(cfg.Level)
	switch levelStr {
	case "DEBUG":
		currentLevel = DEBUG
	case "INFO":
		currentLevel = INFO
	case "WARN":
		currentLevel = WARN
	case "ERROR":
		currentLevel = ERROR
	default:
		currentLevel = INFO
	}
	return nil
}

func SetLevel(level Level)       { currentLevel = level }
func ShouldLog(level Level) bool { return level >= currentLevel }

func getDir(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return "."
}
