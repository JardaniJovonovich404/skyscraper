package scraper

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"inst/internal/logging"

	"github.com/chromedp/chromedp"
)

func dismissCookieBanners() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		logging.DebugLogger.Println("dismissCookieBanners: проверка наличия куки-баннеров")
		selectors := []string{
			`button:contains("Allow all cookies")`,
			`button:contains("Allow essential and optional cookies")`,
			`button:contains("Only allow essential cookies")`,
			`button:contains("Accept All")`,
			`button:contains("Разрешить все")`,
			`button:contains("Принять все")`,
		}
		for _, sel := range selectors {
			var exists bool
			_ = chromedp.Evaluate(fmt.Sprintf(`!!document.querySelector('%s')`, sel), &exists).Do(ctx)
			if exists {
				logging.DebugLogger.Printf("Закрываем куки‑баннер: %s", sel)
				if err := chromedp.Click(sel, chromedp.NodeVisible).Do(ctx); err != nil {
					logging.WarnLogger.Printf("Не удалось кликнуть по баннеру %s: %v", sel, err)
				}
				waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				_ = chromedp.Run(waitCtx, chromedp.WaitNotPresent(sel, chromedp.ByQuery))
				cancel()
				return nil
			}
		}
		logging.DebugLogger.Println("dismissCookieBanners: куки-баннеры не обнаружены")
		return nil
	})
}

func dismissPopups() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		logging.DebugLogger.Println("dismissPopups: поиск всплывающих окон")
		xpaths := []string{
			`//div[@role="button"][contains(text(),"Not Now")]`,
			`//button[contains(text(),"Not Now")]`,
			`//div[@role="button"][contains(text(),"Не сейчас")]`,
			`//button[contains(text(),"Не сейчас")]`,
		}
		closedAny := false
		for _, xpath := range xpaths {
			var exists bool
			if err := chromedp.Evaluate(fmt.Sprintf(`!!document.evaluate("%s", document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null).singleNodeValue`, xpath), &exists).Do(ctx); err == nil && exists {
				logging.DebugLogger.Printf("Закрываем окно: %s", xpath)
				if err := chromedp.Click(xpath, chromedp.BySearch).Do(ctx); err != nil {
					logging.WarnLogger.Printf("Не удалось кликнуть по окну %s: %v", xpath, err)
				} else {
					closedAny = true
				}
				waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				_ = chromedp.Run(waitCtx, chromedp.WaitNotPresent(xpath, chromedp.BySearch))
				cancel()
			}
		}
		if !closedAny {
			logging.DebugLogger.Println("dismissPopups: всплывающие окна не найдены")
		}
		return nil
	})
}

func checkLoginRedirect() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		var url string
		if err := chromedp.Location(&url).Do(ctx); err != nil {
			logging.ErrorLogger.Printf("checkLoginRedirect: не удалось получить URL: %v", err)
			return err
		}
		logging.DebugLogger.Printf("checkLoginRedirect: текущий URL = %s", url)
		if strings.Contains(url, "accounts/login") {
			logging.WarnLogger.Println("checkLoginRedirect: обнаружен редирект на логин – сессия истекла")
			return fmt.Errorf("редирект на логин – сессия истекла")
		}
		if strings.Contains(url, "challenge") {
			logging.WarnLogger.Println("checkLoginRedirect: обнаружен challenge")
			return fmt.Errorf("требуется challenge")
		}
		return nil
	})
}

func waitForPostContent() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		logging.DebugLogger.Println("waitForPostContent: ожидание загрузки контента поста")
		selectors := []string{
			`article`,
			`img[alt*="Instagram post"]`,
			`div[role="presentation"]`,
			`main`,
		}
		for _, sel := range selectors {
			var exists bool
			if err := chromedp.Evaluate(fmt.Sprintf(`!!document.querySelector('%s')`, sel), &exists).Do(ctx); err == nil && exists {
				logging.DebugLogger.Printf("Контент загружен, селектор: %s", sel)
				return nil
			}
		}
		logging.WarnLogger.Println("Специфичные селекторы не найдены, ждём body")
		return chromedp.WaitVisible(`body`, chromedp.ByQuery).Do(ctx)
	})
}

func clickLoadMoreComments() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		logging.DebugLogger.Println("clickLoadMoreComments: поиск кнопки загрузки комментариев")

		selectors := []string{
			`button:has-text("Load more comments")`,
			`button:has-text("Загрузить ещё комментарии")`,
			`div[role="button"]:has-text("Load more")`,
			`div[role="button"]:has-text("Загрузить ещё")`,
			`//button[contains(text(), "Load more")]`,
			`//div[@role="button"][contains(text(), "Load more")]`,
			`[aria-label*="Load more comments"]`,
			`[aria-label*="Загрузить ещё"]`,
		}

		for _, sel := range selectors {
			var exists bool
			checkJS := fmt.Sprintf(`!!(document.querySelector('%s') || document.evaluate("%s", document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null).singleNodeValue)`, sel, sel)
			if err := chromedp.Evaluate(checkJS, &exists).Do(ctx); err == nil && exists {
				logging.DebugLogger.Printf("clickLoadMoreComments: кликаем по %s", sel)
				if err := chromedp.Click(sel, chromedp.NodeVisible).Do(ctx); err != nil {
					_ = chromedp.Evaluate(fmt.Sprintf(`(function(){
                        let el = document.querySelector('%s');
                        if (!el) {
                            const xpath = "%s";
                            const res = document.evaluate(xpath, document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null);
                            el = res.singleNodeValue;
                        }
                        if (el) el.dispatchEvent(new MouseEvent('click', {bubbles: true}));
                    })()`, sel, sel), nil).Do(ctx)
				}
				time.Sleep(1 * time.Second)
				return nil
			}
		}
		logging.DebugLogger.Println("clickLoadMoreComments: кнопка не найдена")
		return nil
	})
}

func closeAllDialogs() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		logging.DebugLogger.Println("closeAllDialogs: закрытие всех диалогов")
		if err := chromedp.KeyEvent("Escape").Do(ctx); err != nil {
			logging.WarnLogger.Printf("closeAllDialogs: ошибка при нажатии Escape: %v", err)
		}
		time.Sleep(300 * time.Millisecond)
		xpaths := []string{
			`//div[@role="button"][contains(text(),"Close")]`,
			`//div[@role="button"][contains(text(),"Закрыть")]`,
			`//button[contains(text(),"Close")]`,
			`//button[contains(text(),"Закрыть")]`,
		}
		for _, xpath := range xpaths {
			var exists bool
			_ = chromedp.Evaluate(fmt.Sprintf(`!!document.evaluate("%s", document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null).singleNodeValue`, xpath), &exists).Do(ctx)
			if exists {
				logging.DebugLogger.Printf("Закрываем диалог: %s", xpath)
				_ = chromedp.Click(xpath, chromedp.BySearch).Do(ctx)
				time.Sleep(500 * time.Millisecond)
				break
			}
		}
		return nil
	})
}

func saveDebugHTML(ctx context.Context, filename string) {
	logging.InfoLogger.Printf("Сохранение HTML страницы в %s", filename)
	var html string
	if err := chromedp.Run(ctx, chromedp.OuterHTML("html", &html)); err != nil {
		logging.WarnLogger.Printf("Не удалось получить HTML: %v", err)
		return
	}
	if err := os.WriteFile(filename, []byte(html), 0644); err != nil {
		logging.WarnLogger.Printf("Не удалось сохранить HTML: %v", err)
	} else {
		logging.InfoLogger.Printf("HTML страницы сохранён в %s (%d байт)", filename, len(html))
	}
}

func waitForCommentThreadsReady(ctx context.Context) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		logging.DebugLogger.Println("waitForCommentThreadsReady: ожидание появления комментариев")
		if err := chromedp.WaitReady(`time[datetime]`, chromedp.ByQuery).Do(ctx); err != nil {
			return fmt.Errorf("не удалось обнаружить комментарии: %w", err)
		}
		time.Sleep(1 * time.Second)
		return nil
	})
}
