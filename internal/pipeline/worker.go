package pipeline

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	"inst/internal/logging"
	"inst/internal/models"
)

func (p *Pipeline) postWorker(ctx context.Context, posts <-chan string, results chan<- *models.Comment, progress chan<- ProgressUpdate) error {
	for {
		select {
		case u, ok := <-posts:
			if !ok {
				logging.DebugLogger.Println("postWorker: канал постов закрыт, выход")
				return nil
			}
			sent, err := p.processPost(ctx, u, results)
			if err != nil {
				logging.ErrorLogger.Printf("Ошибка обработки поста %s: %v", u, err)
				if progress != nil {
					select {
					case progress <- ProgressUpdate{ProcessedIncrement: 1, CommentsIncrement: 0, ErrorIncrement: 1}:
					default:
					}
				}
			} else {
				if progress != nil {
					select {
					case progress <- ProgressUpdate{ProcessedIncrement: 1, CommentsIncrement: sent, ErrorIncrement: 0}:
					default:
					}
				}
			}
		case <-ctx.Done():
			logging.WarnLogger.Println("postWorker: отмена контекста, выход")
			return ctx.Err()
		}
	}
}

func (p *Pipeline) processPost(ctx context.Context, postURL string, results chan<- *models.Comment) (sentCount int, err error) {
	postCode := models.ExtractPostCode(postURL)
	logging.DebugLogger.Printf("processPost: начало обработки %s (код: %s)", postURL, postCode)

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic при обработке %s: %v\n%s", postCode, r, debug.Stack())
			logging.ErrorLogger.Println(err)
		}
	}()

	comments, err := p.collector.CollectCommentsForPost(ctx, postURL)
	if err != nil {
		logging.ErrorLogger.Printf("processPost: ошибка сбора комментариев для %s: %v", postCode, err)
		return 0, err
	}

	logging.InfoLogger.Printf("Для поста %s собрано %d комментариев", postCode, len(comments))

	sent := 0
	for _, cmt := range comments {
		cmt.PostCode = postCode
		cmt.PostURL = postURL
		select {
		case results <- &cmt:
			sent++
		case <-ctx.Done():
			logging.WarnLogger.Printf("processPost: отмена контекста, отправлено %d из %d комментариев для %s", sent, len(comments), postCode)
			return sent, ctx.Err()
		}
	}
	logging.InfoLogger.Printf("Пост %s: отправлено %d комментариев в канал сохранения", postCode, sent)
	return sent, nil
}

func (p *Pipeline) saveWorker(ctx context.Context, comments <-chan *models.Comment) {
	const batchSize = 100
	batch := make([]*models.Comment, 0, batchSize)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	defer logging.InfoLogger.Println("saveWorker: работа завершена, сохранение остановлено")

	logging.DebugLogger.Println("saveWorker: запущен")

	for {
		select {
		case cmt, ok := <-comments:
			if !ok {
				logging.InfoLogger.Printf("saveWorker: канал комментариев закрыт, сохраняю оставшиеся %d записей", len(batch))
				if len(batch) > 0 {
					p.saveBatch(ctx, batch)
				}
				return
			}
			batch = append(batch, cmt)
			if len(batch) >= batchSize {
				logging.DebugLogger.Printf("saveWorker: достигнут лимит батча (%d), запуск сохранения", batchSize)
				p.saveBatch(ctx, batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				logging.DebugLogger.Printf("saveWorker: тикер (5с), сохранение %d накопленных записей", len(batch))
				p.saveBatch(ctx, batch)
				batch = batch[:0]
			}
		case <-ctx.Done():
			logging.WarnLogger.Printf("saveWorker: отмена контекста, финальное сохранение %d записей", len(batch))
			if len(batch) > 0 {
				p.saveBatch(ctx, batch)
			}
			go func() {
				for range comments {
				}
				logging.DebugLogger.Println("saveWorker: канал комментариев осушен")
			}()
			return
		}
	}
}

func (p *Pipeline) saveBatch(ctx context.Context, batch []*models.Comment) {
	if len(batch) == 0 {
		return
	}
	batchSize := len(batch)
	start := time.Now()
	logging.DebugLogger.Printf("saveBatch: сохранение %d комментариев", batchSize)

	for i, s := range p.storages {
		storageType := fmt.Sprintf("%T", s)
		if batcher, ok := s.(interface {
			SaveComments(context.Context, []*models.Comment) error
		}); ok {
			logging.DebugLogger.Printf("saveBatch: пакетная запись в хранилище #%d (%s)", i, storageType)
			if err := batcher.SaveComments(ctx, batch); err != nil {
				logging.ErrorLogger.Printf("saveBatch: ошибка пакетной записи в хранилище #%d (%s): %v", i, storageType, err)
			}
		} else {
			logging.DebugLogger.Printf("saveBatch: поштучная запись в хранилище #%d (%s)", i, storageType)
			for _, cmt := range batch {
				if err := s.SaveComment(cmt); err != nil {
					logging.ErrorLogger.Printf("saveBatch: ошибка сохранения комментария %s в хранилище #%d: %v", cmt.ID, i, err)
				}
			}
		}
	}

	elapsed := time.Since(start)
	if elapsed > time.Second {
		logging.InfoLogger.Printf("saveBatch: пачка из %d записей сохранена за %v", batchSize, elapsed)
	} else {
		logging.DebugLogger.Printf("saveBatch: пачка из %d записей сохранена за %v", batchSize, elapsed)
	}
}
