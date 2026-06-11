package scraper

import (
	"context"

	"inst/internal/browser"
	"inst/internal/logging"
	"inst/internal/models"
)

type Collector interface {
	CollectCommentsForPost(ctx context.Context, postURL string) ([]models.Comment, error)
}

func NewCollector(pool *browser.Pool, targetUsers []string) Collector {
	logging.InfoLogger.Printf("Создание коллектора комментариев для целевых пользователей: %v", targetUsers)
	collector := NewCommentsCollector(pool, targetUsers)
	logging.DebugLogger.Println("Коллектор комментариев успешно создан")
	return collector
}
