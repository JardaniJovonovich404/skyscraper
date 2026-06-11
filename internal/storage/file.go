package storage

import (
	"encoding/json"
	"os"
	"sync"

	"inst/internal/logging"
	"inst/internal/models"
)

type FileStorage struct {
	file    *os.File
	encoder *json.Encoder
	mu      sync.Mutex
}

func NewFileStorage(path string) (*FileStorage, error) {
	logging.InfoLogger.Printf("Создание файлового хранилища: %s", path)
	f, err := os.Create(path)
	if err != nil {
		logging.ErrorLogger.Printf("Ошибка создания файла %s: %v", path, err)
		return nil, err
	}
	logging.DebugLogger.Printf("Файл %s успешно создан", path)
	return &FileStorage{file: f, encoder: json.NewEncoder(f)}, nil
}

func (fs *FileStorage) SaveComment(c *models.Comment) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if logging.ShouldLog(logging.DEBUG) {
		logging.DebugLogger.Printf("Сохранение комментария %s в файл", c.ID)
	}
	err := fs.encoder.Encode(c)
	if err != nil {
		logging.ErrorLogger.Printf("Ошибка записи комментария %s в файл: %v", c.ID, err)
		return err
	}
	if syncErr := fs.file.Sync(); syncErr != nil {
		logging.WarnLogger.Printf("Ошибка fsync при сохранении %s: %v", c.ID, syncErr)
	}
	if logging.ShouldLog(logging.DEBUG) {
		logging.DebugLogger.Printf("Комментарий %s успешно записан", c.ID)
	}
	return nil
}

func (fs *FileStorage) Close() error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	logging.InfoLogger.Println("Закрытие файлового хранилища")
	if syncErr := fs.file.Sync(); syncErr != nil {
		logging.WarnLogger.Printf("Ошибка fsync при закрытии файла: %v", syncErr)
	}
	err := fs.file.Close()
	if err != nil {
		logging.ErrorLogger.Printf("Ошибка закрытия файла: %v", err)
	} else {
		logging.DebugLogger.Println("Файловое хранилище закрыто")
	}
	return err
}
