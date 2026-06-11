package pipeline

import (
	"context"
	"sync"

	"inst/internal/logging"
	"inst/internal/models"
	"inst/internal/scraper"
	"inst/internal/storage"

	"golang.org/x/sync/errgroup"
)

const commentBufferSize = 100000

type ProgressUpdate struct {
	ProcessedIncrement int
	CommentsIncrement  int
	ErrorIncrement     int
	Total              int
}

type Pipeline struct {
	collector   scraper.Collector
	storages    []storage.Storage
	concurrency int
}

func New(collector scraper.Collector, storages []storage.Storage, concurrency int) *Pipeline {
	if concurrency < 1 {
		concurrency = 1
	}
	logging.InfoLogger.Printf("Создание Pipeline: concurrency=%d, storages=%d", concurrency, len(storages))
	return &Pipeline{collector: collector, storages: storages, concurrency: concurrency}
}

func (p *Pipeline) Execute(ctx context.Context, postURLs []string) error {
	return p.execute(ctx, postURLs, nil)
}

func (p *Pipeline) ExecuteWithProgress(ctx context.Context, postURLs []string, progress chan<- ProgressUpdate) error {
	return p.execute(ctx, postURLs, progress)
}

func (p *Pipeline) execute(ctx context.Context, postURLs []string, progress chan<- ProgressUpdate) error {
	totalURLs := len(postURLs)
	logging.InfoLogger.Printf("Pipeline.Execute: начало обработки %d постов, concurrency=%d", totalURLs, p.concurrency)

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(p.concurrency)

	postChan := make(chan string, len(postURLs))
	for _, u := range postURLs {
		postChan <- u
	}
	close(postChan)

	if progress != nil {
		select {
		case progress <- ProgressUpdate{Total: totalURLs}:
		default:
		}
	}

	if logging.ShouldLog(logging.DEBUG) {
		logging.DebugLogger.Printf("Канал postURLs заполнен и закрыт, емкость=%d", cap(postChan))
	}

	commentChan := make(chan *models.Comment, commentBufferSize)

	for i := 0; i < p.concurrency; i++ {
		workerID := i + 1
		g.Go(func() error {
			logging.DebugLogger.Printf("postWorker #%d запущен", workerID)
			err := p.postWorker(ctx, postChan, commentChan, progress)
			if err != nil {
				logging.ErrorLogger.Printf("postWorker #%d завершился с ошибкой: %v", workerID, err)
			} else {
				logging.DebugLogger.Printf("postWorker #%d завершился успешно", workerID)
			}
			return err
		})
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		logging.DebugLogger.Println("saveWorker запущен")
		p.saveWorker(ctx, commentChan)
		logging.DebugLogger.Println("saveWorker завершён")
	}()

	logging.InfoLogger.Println("Все воркеры запущены, ожидание завершения обработки...")
	err := g.Wait()
	if err != nil {
		logging.ErrorLogger.Printf("Pipeline.Execute: ошибка в одном из postWorker: %v", err)
	}
	close(commentChan)
	logging.DebugLogger.Println("Канал комментариев закрыт, ожидание завершения saveWorker...")
	wg.Wait()
	if err == nil {
		logging.InfoLogger.Printf("Pipeline.Execute: успешно обработано %d постов", totalURLs)
	} else {
		logging.WarnLogger.Println("Pipeline.Execute: обработка завершена с ошибками")
	}
	return err
}
