package storage

import (
	"context"
	"database/sql"

	"inst/internal/config"
	"inst/internal/logging"
	"inst/internal/models"

	_ "github.com/mattn/go-sqlite3"
)

type SQLiteStorage struct {
	db *sql.DB
}

func NewSQLiteStorage(cfg *config.DatabaseConfig) (*SQLiteStorage, error) {
	logging.InfoLogger.Println("Инициализация SQLite хранилища...")
	dbPath := cfg.DBName
	if dbPath == "" {
		dbPath = "comments.db"
	}
	db, err := sql.Open("sqlite3", dbPath+"?_journal=WAL&_fk=1")
	if err != nil {
		logging.ErrorLogger.Printf("Ошибка открытия SQLite: %v", err)
		return nil, err
	}
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)

	if err := db.Ping(); err != nil {
		logging.ErrorLogger.Printf("SQLite не отвечает: %v", err)
		db.Close()
		return nil, err
	}
	logging.InfoLogger.Printf("SQLite подключена, файл: %s", dbPath)

	if err := createSQLiteTable(db); err != nil {
		logging.ErrorLogger.Printf("Ошибка создания таблиц: %v", err)
		db.Close()
		return nil, err
	}

	logging.InfoLogger.Println("Хранилище SQLite успешно инициализировано")
	return &SQLiteStorage{db: db}, nil
}

func createSQLiteTable(db *sql.DB) error {
	logging.InfoLogger.Println("Проверка/создание таблицы comments (SQLite)...")
	query := `
	CREATE TABLE IF NOT EXISTS comments (
		id TEXT PRIMARY KEY,
		username TEXT NOT NULL,
		text TEXT,
		timestamp DATETIME NOT NULL,
		post_code TEXT NOT NULL,
		post_url TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_comments_username ON comments(username);
	CREATE INDEX IF NOT EXISTS idx_comments_post_code ON comments(post_code);
	CREATE INDEX IF NOT EXISTS idx_comments_timestamp ON comments(timestamp DESC);
	`
	_, err := db.Exec(query)
	if err != nil {
		return err
	}
	logging.InfoLogger.Println("Таблица comments и индексы готовы")
	return nil
}

func (s *SQLiteStorage) SaveComment(c *models.Comment) error {
	return s.SaveComments(context.Background(), []*models.Comment{c})
}

func (s *SQLiteStorage) SaveComments(ctx context.Context, comments []*models.Comment) error {
	if len(comments) == 0 {
		logging.DebugLogger.Println("SaveComments: пустой батч, выход")
		return nil
	}

	if err := ctx.Err(); err != nil {
		logging.WarnLogger.Println("SaveComments: контекст отменён до начала транзакции")
		return err
	}

	logging.DebugLogger.Printf("SaveComments: начало транзакции для %d комментариев", len(comments))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		logging.ErrorLogger.Printf("SaveComments: ошибка начала транзакции: %v", err)
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT OR IGNORE INTO comments (id, username, text, timestamp, post_code, post_url)
		VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		logging.ErrorLogger.Printf("SaveComments: ошибка подготовки запроса: %v", err)
		return err
	}
	defer stmt.Close()

	inserted := 0
	for _, c := range comments {
		if err := ctx.Err(); err != nil {
			logging.WarnLogger.Println("SaveComments: контекст отменён во время вставки")
			return err
		}
		if _, err := stmt.ExecContext(ctx, c.ID, c.Username, c.Text, c.Timestamp, c.PostCode, c.PostURL); err != nil {
			logging.ErrorLogger.Printf("SaveComments: ошибка вставки комментария %s: %v", c.ID, err)
			return err
		}
		inserted++
	}

	if err := tx.Commit(); err != nil {
		logging.ErrorLogger.Printf("SaveComments: ошибка фиксации транзакции: %v", err)
		return err
	}

	logging.DebugLogger.Printf("SaveComments: транзакция успешно завершена, вставлено %d записей", inserted)
	return nil
}

func (s *SQLiteStorage) Close() error {
	logging.InfoLogger.Println("Закрытие соединения с SQLite")
	err := s.db.Close()
	if err != nil {
		logging.ErrorLogger.Printf("Ошибка закрытия SQLite: %v", err)
	} else {
		logging.InfoLogger.Println("Соединение с SQLite закрыто")
	}
	return err
}
