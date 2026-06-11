package scraper

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"inst/internal/browser"
	"inst/internal/logging"

	"github.com/chromedp/chromedp"
)

func GetPostsFromProfile(ctx context.Context, pool *browser.Pool, username string, maxPosts int) ([]string, error) {
	logging.InfoLogger.Printf("GetPostsFromProfile: начало сбора постов для %s (макс. %d)", username, maxPosts)
	maxRetries := 3
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		logging.InfoLogger.Printf("Попытка %d/%d для профиля %s", attempt, maxRetries, username)
		urls, err := tryGetPostsFromProfile(ctx, pool, username, maxPosts)
		if err == nil {
			logging.InfoLogger.Printf("Попытка %d успешна, получено %d постов", attempt, len(urls))
			return urls, nil
		}
		lastErr = err
		logging.WarnLogger.Printf("Попытка %d для %s не удалась: %v", attempt, username, err)
		if attempt < maxRetries {
			backoff := time.Duration(1<<uint(attempt)) * time.Second
			logging.DebugLogger.Printf("Ожидание %v перед повтором", backoff)
			time.Sleep(backoff)
		}
	}
	logging.ErrorLogger.Printf("GetPostsFromProfile: все попытки для %s исчерпаны: %v", username, lastErr)
	return nil, lastErr
}

func tryGetPostsFromProfile(ctx context.Context, pool *browser.Pool, username string, maxPosts int) ([]string, error) {
	logging.DebugLogger.Printf("tryGetPostsFromProfile: начало для %s (maxPosts=%d)", username, maxPosts)
	tabCtx, done := pool.GetTab()
	if tabCtx == nil {
		logging.ErrorLogger.Printf("tryGetPostsFromProfile: не удалось получить вкладку для %s", username)
		return nil, fmt.Errorf("не удалось получить вкладку")
	}
	defer func() {
		done()
		logging.DebugLogger.Printf("tryGetPostsFromProfile: вкладка возвращена для %s", username)
	}()

	profileCtx, cancel := context.WithTimeout(tabCtx, 3*time.Minute)
	defer cancel()

	profileURL := fmt.Sprintf("https://www.instagram.com/%s/", username)
	var posts []string
	postSet := make(map[string]bool)
	var pageHTML string

	saveHTML := func() {
		if pageHTML != "" {
			fname := fmt.Sprintf("debug_profile_%s_%d.html", username, time.Now().Unix())
			if err := os.WriteFile(fname, []byte(pageHTML), 0644); err != nil {
				logging.WarnLogger.Printf("Не удалось сохранить debug HTML профиля: %v", err)
			} else {
				logging.InfoLogger.Printf("Сохранён debug HTML профиля: %s", fname)
			}
		} else {
			logging.DebugLogger.Println("saveHTML: HTML страницы пуст, пропускаем сохранение")
		}
	}

	logging.InfoLogger.Printf("Загрузка профиля: %s", profileURL)
	err := chromedp.Run(profileCtx,
		chromedp.Navigate(profileURL),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var curURL string
			chromedp.Location(&curURL).Do(ctx)
			logging.DebugLogger.Printf("Текущий URL после навигации: %s", curURL)
			if strings.Contains(curURL, "accounts/login") {
				logging.WarnLogger.Println("Обнаружен редирект на логин – сессия истекла")
				return fmt.Errorf("session expired")
			}
			if strings.Contains(curURL, "challenge") {
				logging.WarnLogger.Println("Обнаружен challenge")
				return fmt.Errorf("challenge required")
			}
			return nil
		}),
		chromedp.WaitReady("body"),
		chromedp.Sleep(2*time.Second),
		chromedp.ActionFunc(func(ctx context.Context) error {
			logging.DebugLogger.Println("Закрытие всплывающих окон после загрузки профиля")
			selectors := []string{`div[role="button"]:contains("Not Now")`, `button:contains("Not Now")`}
			for _, sel := range selectors {
				var exists bool
				if err := chromedp.Evaluate(fmt.Sprintf(`!!document.querySelector('%s')`, sel), &exists).Do(ctx); err == nil && exists {
					logging.DebugLogger.Printf("Закрываем окно: %s", sel)
					chromedp.Click(sel, chromedp.NodeVisible).Do(ctx)
					time.Sleep(1 * time.Second)
				}
			}
			return nil
		}),
		chromedp.ActionFunc(func(ctx context.Context) error {
			logging.DebugLogger.Println("Начало сбора ссылок на посты")
			lastHeight := 0
			sameCount := 0
			const maxStableHeight = 2
			const scrollPause = 1 * time.Second
			for scrollIteration := 1; ; scrollIteration++ {
				var links []string
				_ = chromedp.Evaluate(`Array.from(document.querySelectorAll('a[href*="/p/"], a[href*="/reel/"]')).map(a => a.href).filter((v,i,s) => s.indexOf(v)===i)`, &links).Do(ctx)
				newFound := 0
				for _, l := range links {
					if !postSet[l] {
						postSet[l] = true
						posts = append(posts, l)
						newFound++
					}
				}
				logging.DebugLogger.Printf("Прокрутка %d: найдено %d новых ссылок, всего %d", scrollIteration, newFound, len(posts))
				if maxPosts > 0 && len(posts) >= maxPosts {
					logging.InfoLogger.Printf("Достигнуто ограничение maxPosts=%d", maxPosts)
					break
				}
				var height int
				chromedp.Evaluate(`document.documentElement.scrollHeight`, &height).Do(ctx)
				logging.DebugLogger.Printf("Высота страницы: %d (предыдущая: %d)", height, lastHeight)
				if height == lastHeight {
					sameCount++
					logging.DebugLogger.Printf("Высота не изменилась, счётчик стабильности: %d/%d", sameCount, maxStableHeight)
				} else {
					sameCount = 0
				}
				if sameCount >= maxStableHeight {
					logging.InfoLogger.Println("Высота страницы стабильна, прокрутка завершена")
					break
				}
				lastHeight = height
				chromedp.Evaluate(`window.scrollTo(0,document.documentElement.scrollHeight)`, nil).Do(ctx)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(scrollPause):
				}
			}
			return nil
		}),
		chromedp.ActionFunc(func(ctx context.Context) error {
			if err := chromedp.OuterHTML("html", &pageHTML).Do(ctx); err != nil {
				logging.WarnLogger.Printf("Не удалось получить HTML страницы: %v", err)
			} else {
				logging.DebugLogger.Println("HTML страницы успешно получен")
			}
			return nil
		}),
	)
	if err != nil {
		saveHTML()
		logging.ErrorLogger.Printf("Ошибка при загрузке профиля %s: %v", username, err)
		return nil, err
	}
	logging.InfoLogger.Printf("Собрано %d постов для %s", len(posts), username)
	return posts, nil
}
