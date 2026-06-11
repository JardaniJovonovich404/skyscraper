package browser

import (
	"context"
	"net/http"

	"inst/internal/logging"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

func setCookiesAction(cookies []*http.Cookie) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		if logging.ShouldLog(logging.DEBUG) {
			logging.DebugLogger.Printf("Установка %d кук", len(cookies))
		}
		for _, c := range cookies {
			domain := c.Domain
			if domain == "" {
				domain = ".instagram.com"
			}
			path := c.Path
			if path == "" {
				path = "/"
			}
			err := network.SetCookie(c.Name, c.Value).
				WithDomain(domain).
				WithPath(path).
				WithHTTPOnly(c.HttpOnly).
				WithSecure(c.Secure).
				Do(ctx)
			if err != nil {
				logging.WarnLogger.Printf("Ошибка установки куки %s: %v", c.Name, err)
			}
		}
		return nil
	})
}
