package scraper

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"inst/internal/browser"
	"inst/internal/logging"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

type loginFormInfo struct {
	UsernameSelector string
	PasswordSelector string
	ButtonSelector   string
}

func Login(ctx context.Context, pool *browser.Pool, username, password string) ([]*http.Cookie, error) {
	logging.InfoLogger.Println("Запуск процедуры входа в Instagram")

	tabCtx, done := pool.GetTab()
	if tabCtx == nil {
		logging.ErrorLogger.Println("Login: не удалось получить вкладку из пула")
		return nil, fmt.Errorf("не удалось получить вкладку")
	}
	defer func() {
		logging.DebugLogger.Println("Login: возврат вкладки в пул")
		done()
	}()

	loginCtx, cancel := context.WithTimeout(tabCtx, 180*time.Second)
	defer cancel()

	logging.InfoLogger.Println("Login: шаг 1 — переход на страницу логина")
	if err := chromedp.Run(loginCtx,
		chromedp.Navigate("https://www.instagram.com/accounts/login/"),
	); err != nil {
		logging.ErrorLogger.Printf("Login: ошибка открытия страницы логина: %v", err)
		return nil, fmt.Errorf("не удалось открыть страницу логина: %w", err)
	}

	logging.InfoLogger.Println("Login: ожидание загрузки страницы...")
	if err := chromedp.Run(loginCtx,
		chromedp.WaitReady("body"),
		chromedp.Sleep(2*time.Second),
	); err != nil {
		logging.ErrorLogger.Printf("Login: страница не загрузилась: %v", err)
		return nil, fmt.Errorf("страница не загрузилась: %w", err)
	}

	logging.DebugLogger.Println("Login: закрытие баннеров cookie")
	_ = chromedp.Run(loginCtx, dismissCookieBanners())

	logging.InfoLogger.Println("Login: шаг 2 — поиск формы входа")
	form, err := findLoginForm(loginCtx)
	if err != nil {
		saveDebugHTML(loginCtx, "login_form_not_found.html")
		logging.ErrorLogger.Printf("Login: не удалось найти форму входа: %v", err)
		return nil, fmt.Errorf("не удалось найти форму входа: %w", err)
	}
	logging.InfoLogger.Printf("Login: найдена форма — username='%s', password='%s', button='%s'",
		form.UsernameSelector, form.PasswordSelector, form.ButtonSelector)

	logging.InfoLogger.Println("Login: шаг 3 — ввод логина и пароля")
	if err := typeCredentialsHumanLike(loginCtx, form.UsernameSelector, form.PasswordSelector, username, password); err != nil {
		logging.ErrorLogger.Printf("Login: ошибка ввода учётных данных: %v", err)
		return nil, fmt.Errorf("ошибка ввода учётных данных: %w", err)
	}

	logging.InfoLogger.Println("Login: шаг 4 — отправка формы через Enter")
	if err := chromedp.Run(loginCtx,
		chromedp.Sleep(500*time.Millisecond),
		chromedp.SendKeys(form.PasswordSelector, "\n", chromedp.ByQuery),
	); err != nil {
		logging.WarnLogger.Println("Login: Enter не сработал, пробуем клик по кнопке")
		if err := clickLoginButtonHumanLike(loginCtx, form.ButtonSelector); err != nil {
			logging.ErrorLogger.Printf("Login: не удалось отправить форму: %v", err)
			return nil, fmt.Errorf("не удалось отправить форму: %w", err)
		}
	}

	logging.InfoLogger.Println("Login: шаг 5 — ожидание авторизационной куки...")
	cookies, err := waitForSession(loginCtx)
	if err != nil {
		logging.ErrorLogger.Printf("Login: ошибка ожидания сессии: %v", err)
		return nil, err
	}

	logging.InfoLogger.Printf("Login: успешно получено %d cookies", len(cookies))
	if logging.ShouldLog(logging.DEBUG) {
		for _, c := range cookies {
			valPreview := c.Value
			if len(valPreview) > 10 {
				valPreview = valPreview[:10] + "..."
			}
			logging.DebugLogger.Printf("Cookie: %s=%s (domain=%s, path=%s)", c.Name, valPreview, c.Domain, c.Path)
		}
	}
	return cookies, nil
}

func waitForSession(ctx context.Context) ([]*http.Cookie, error) {
	logging.DebugLogger.Println("waitForSession: начало ожидания сессии")
	deadline := time.Now().Add(120 * time.Second)
	for {
		if time.Now().After(deadline) {
			saveDebugHTML(ctx, "login_timeout.html")
			logging.ErrorLogger.Println("waitForSession: время ожидания сессии истекло")
			return nil, fmt.Errorf("время ожидания сессии истекло")
		}

		if isCaptchaPage(ctx) {
			logging.WarnLogger.Println("waitForSession: обнаружена страница с капчей")
			saveDebugHTML(ctx, "captcha_detected.html")
			logging.InfoLogger.Println("Ожидаю ручного прохождения капчи...")
			if err := waitForCaptcha(ctx); err != nil {
				logging.ErrorLogger.Printf("waitForSession: ошибка ожидания капчи: %v", err)
				return nil, err
			}
			continue
		}

		cookies := extractCookies(ctx)
		for _, c := range cookies {
			if c.Name == "sessionid" && c.Value != "" {
				logging.InfoLogger.Println("waitForSession: sessionid обнаружен!")
				return cookies, nil
			}
		}

		if errMsg := getErrorMessage(ctx); errMsg != "" {
			logging.ErrorLogger.Printf("waitForSession: ошибка входа: %s", errMsg)
			return nil, fmt.Errorf("ошибка входа: %s", errMsg)
		}

		var twoFactor bool
		_ = chromedp.Run(ctx, chromedp.Evaluate(`!!document.querySelector('input[name="verificationCode"]')`, &twoFactor))
		if twoFactor {
			logging.WarnLogger.Println("waitForSession: обнаружена двухфакторная аутентификация")
			return nil, fmt.Errorf("требуется двухфакторная аутентификация")
		}

		var currentURL string
		_ = chromedp.Run(ctx, chromedp.Location(&currentURL))
		if strings.Contains(currentURL, "challenge") {
			logging.WarnLogger.Printf("waitForSession: требуется прохождение challenge, URL=%s", currentURL)
			return nil, fmt.Errorf("требуется прохождение challenge")
		}

		_ = chromedp.Run(ctx, dismissPopups())

		if logging.ShouldLog(logging.DEBUG) {
			logging.DebugLogger.Printf("waitForSession: ожидание... текущий URL=%s", currentURL)
		}
		time.Sleep(3 * time.Second)
	}
}

func isCaptchaPage(ctx context.Context) bool {
	var currentURL string
	if err := chromedp.Run(ctx, chromedp.Location(&currentURL)); err == nil {
		if strings.Contains(currentURL, "/auth_platform/recaptcha") ||
			strings.Contains(currentURL, "captcha") {
			logging.DebugLogger.Printf("isCaptchaPage: URL содержит captcha: %s", currentURL)
			return true
		}
	}
	return hasCaptcha(ctx)
}

func hasCaptcha(ctx context.Context) bool {
	var exists bool
	_ = chromedp.Run(ctx, chromedp.Evaluate(`!!(
		document.querySelector('iframe[src*="recaptcha"]') ||
		document.querySelector('iframe[src*="captcha"]') ||
		document.querySelector('div#captcha') ||
		document.querySelector('div[data-captcha]') ||
		document.querySelector('div.g-recaptcha') ||
		document.querySelector('iframe[src*="arkose"]')
	)`, &exists))
	if exists {
		logging.DebugLogger.Println("hasCaptcha: найден DOM-элемент капчи")
		return true
	}

	var body string
	_ = chromedp.Run(ctx, chromedp.Evaluate(`document.body?.innerText || ''`, &body))
	body = strings.ToLower(body)
	if strings.Contains(body, "not a robot") ||
		strings.Contains(body, "капча") ||
		strings.Contains(body, "подтвердите, что вы") ||
		strings.Contains(body, "проверка безопасности") ||
		strings.Contains(body, "нажмите и удерживайте") {
		logging.DebugLogger.Println("hasCaptcha: ключевое слово найдено в тексте страницы")
		return true
	}
	return false
}

func waitForCaptcha(ctx context.Context) error {
	logging.InfoLogger.Println("waitForCaptcha: ожидание прохождения капчи...")
	dl := time.Now().Add(120 * time.Second)
	for {
		if time.Now().After(dl) {
			saveDebugHTML(ctx, "captcha_timeout.html")
			logging.ErrorLogger.Println("waitForCaptcha: время ожидания капчи истекло")
			return fmt.Errorf("время ожидания капчи истекло")
		}
		if !isCaptchaPage(ctx) {
			logging.InfoLogger.Println("waitForCaptcha: капча пройдена (или исчезла)")
			return nil
		}
		time.Sleep(3 * time.Second)
	}
}

func humanLikeMouseMove(targetX, targetY float64) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		steps := 15 + rng.Intn(10)
		var startX, startY float64 = 0, 0
		for i := 0; i <= steps; i++ {
			t := float64(i) / float64(steps)
			cx := startX + (targetX-startX)*t*t
			cy := startY + (targetY-startY)*math.Pow(t, 0.3)
			if i > 0 && i < steps {
				cx += (rng.Float64() - 0.5) * 2
				cy += (rng.Float64() - 0.5) * 2
			}
			if err := input.DispatchMouseEvent(input.MouseMoved, cx, cy).
				WithModifiers(input.ModifierNone).
				Do(ctx); err != nil {
				return err
			}
			time.Sleep(time.Duration(5+rng.Intn(5)) * time.Millisecond)
		}
		return nil
	})
}

func getElementCenter(ctx context.Context, selector string) (float64, float64, error) {
	var coords struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	}
	script := fmt.Sprintf(`(() => {
		const el = document.querySelector('%s');
		if (!el) return null;
		const rect = el.getBoundingClientRect();
		return {x: rect.x + rect.width/2, y: rect.y + rect.height/2};
	})()`, selector)

	if err := chromedp.Run(ctx, chromedp.Evaluate(script, &coords)); err != nil {
		logging.ErrorLogger.Printf("getElementCenter: ошибка получения координат для %s: %v", selector, err)
		return 0, 0, err
	}
	if coords.X == 0 && coords.Y == 0 {
		logging.ErrorLogger.Printf("getElementCenter: элемент не найден: %s", selector)
		return 0, 0, fmt.Errorf("элемент не найден: %s", selector)
	}
	if logging.ShouldLog(logging.DEBUG) {
		logging.DebugLogger.Printf("getElementCenter: %s -> (%.1f, %.1f)", selector, coords.X, coords.Y)
	}
	return coords.X, coords.Y, nil
}

func typeCredentialsHumanLike(ctx context.Context, userSel, passSel, username, password string) error {
	logging.DebugLogger.Println("typeCredentialsHumanLike: начало эмуляции ввода")

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	userX, userY, err := getElementCenter(ctx, userSel)
	if err != nil {
		return fmt.Errorf("координаты username: %w", err)
	}
	passX, passY, err := getElementCenter(ctx, passSel)
	if err != nil {
		return fmt.Errorf("координаты password: %w", err)
	}

	logging.DebugLogger.Println("typeCredentialsHumanLike: ввод логина")
	if err := chromedp.Run(ctx, humanLikeMouseMove(userX, userY)); err != nil {
		return err
	}
	if err := chromedp.Run(ctx, chromedp.Click(userSel, chromedp.ByQuery)); err != nil {
		return err
	}
	time.Sleep(time.Duration(100+rng.Intn(100)) * time.Millisecond)

	for _, ch := range username {
		if err := chromedp.Run(ctx, chromedp.SendKeys(userSel, string(ch), chromedp.ByQuery)); err != nil {
			return err
		}
		time.Sleep(time.Duration(50+rng.Intn(100)) * time.Millisecond)
	}

	logging.DebugLogger.Println("typeCredentialsHumanLike: ввод пароля")
	if err := chromedp.Run(ctx, humanLikeMouseMove(passX, passY)); err != nil {
		return err
	}
	if err := chromedp.Run(ctx, chromedp.Click(passSel, chromedp.ByQuery)); err != nil {
		return err
	}
	time.Sleep(time.Duration(100+rng.Intn(100)) * time.Millisecond)

	for _, ch := range password {
		if err := chromedp.Run(ctx, chromedp.SendKeys(passSel, string(ch), chromedp.ByQuery)); err != nil {
			return err
		}
		time.Sleep(time.Duration(50+rng.Intn(100)) * time.Millisecond)
	}
	logging.DebugLogger.Println("typeCredentialsHumanLike: ввод завершён")
	return nil
}

func clickLoginButtonHumanLike(ctx context.Context, buttonSelector string) error {
	logging.DebugLogger.Printf("clickLoginButtonHumanLike: ожидание активности кнопки %s", buttonSelector)
	if err := waitForEnabledButton(ctx, buttonSelector); err != nil {
		return err
	}
	btnX, btnY, err := getElementCenter(ctx, buttonSelector)
	if err != nil {
		return err
	}
	if err := chromedp.Run(ctx, humanLikeMouseMove(btnX, btnY)); err != nil {
		return err
	}
	logging.DebugLogger.Println("clickLoginButtonHumanLike: клик по кнопке")
	return chromedp.Run(ctx, chromedp.Click(buttonSelector, chromedp.ByQuery))
}

func waitForEnabledButton(ctx context.Context, selector string) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var disabled string
		_ = chromedp.Run(ctx, chromedp.Evaluate(
			fmt.Sprintf(`document.querySelector('%s')?.getAttribute('aria-disabled')`, selector),
			&disabled))
		if disabled != "true" {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	logging.ErrorLogger.Printf("waitForEnabledButton: кнопка %s всё ещё disabled", selector)
	return fmt.Errorf("кнопка всё ещё disabled")
}

func getErrorMessage(ctx context.Context) string {
	var msg string
	selectors := []string{
		`#slfErrorAlert`,
		`[role="alert"]`,
		`p[data-js="errorMessage"]`,
	}
	for _, sel := range selectors {
		_ = chromedp.Run(ctx, chromedp.Evaluate(
			fmt.Sprintf(`document.querySelector('%s')?.innerText || ''`, sel), &msg))
		if msg != "" {
			logging.WarnLogger.Printf("getErrorMessage: найдена ошибка через селектор %s: %s", sel, msg)
			return strings.TrimSpace(msg)
		}
	}
	_ = chromedp.Run(ctx, chromedp.Evaluate(`
		[...document.querySelectorAll('*')].find(el => el.innerText?.includes('incorrect'))?.innerText || ''
	`, &msg))
	if msg != "" {
		logging.WarnLogger.Printf("getErrorMessage: найдена ошибка в тексте: %s", msg)
	}
	return strings.TrimSpace(msg)
}

func extractCookies(ctx context.Context) []*http.Cookie {
	getCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	var cookies []*http.Cookie
	err := chromedp.Run(getCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		cdpCookies, err := network.GetCookies().WithURLs([]string{
			"https://www.instagram.com",
			"https://.instagram.com",
		}).Do(ctx)
		if err != nil {
			return err
		}
		for _, c := range cdpCookies {
			cookies = append(cookies, &http.Cookie{
				Name:   c.Name,
				Value:  c.Value,
				Domain: c.Domain,
				Path:   c.Path,
			})
		}
		return nil
	}))
	if err != nil {
		logging.DebugLogger.Printf("extractCookies: временная ошибка извлечения кук: %v", err)
		return nil
	}
	if logging.ShouldLog(logging.DEBUG) {
		logging.DebugLogger.Printf("extractCookies: извлечено %d кук", len(cookies))
	}
	return cookies
}

func CheckSession(jar http.CookieJar) bool {
	if jar == nil {
		logging.DebugLogger.Println("CheckSession: CookieJar равен nil")
		return false
	}
	u, _ := url.Parse("https://www.instagram.com")
	for _, c := range jar.Cookies(u) {
		if (c.Name == "sessionid" || c.Name == "ds_user_id") && c.Value != "" {
			logging.DebugLogger.Printf("CheckSession: найдена активная кука %s", c.Name)
			return true
		}
	}
	logging.DebugLogger.Println("CheckSession: активная сессия не найдена")
	return false
}

func findLoginForm(ctx context.Context) (loginFormInfo, error) {
	const script = `
		(() => {
			const form = document.querySelector('form#login_form');
			if (!form) return null;
			const username = form.querySelector('input[name="email"]') || form.querySelector('input[name="username"]');
			const password = form.querySelector('input[name="pass"]') || form.querySelector('input[name="password"]');
			if (!username || !password) return null;
			let btn = Array.from(document.querySelectorAll('div[role="button"]')).find(el => {
				const t = el.innerText.trim();
				return t === 'Войти' || t === 'Log In';
			});
			if (!btn) btn = form.querySelector('button[type="submit"]');
			if (!btn) return null;
			return {
				username: username.name === 'email' ? 'input[name="email"]' : 'input[name="username"]',
				password: password.name === 'pass' ? 'input[name="pass"]' : 'input[name="password"]',
				submit: btn.tagName.toLowerCase() === 'button' ? 'button[type="submit"]' : 'div[role="button"]'
			};
		})()
	`
	var res struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Submit   string `json:"submit"`
	}
	deadline := time.Now().Add(15 * time.Second)
	logging.DebugLogger.Println("findLoginForm: поиск формы входа...")
	for time.Now().Before(deadline) {
		if err := chromedp.Run(ctx, chromedp.Evaluate(script, &res)); err == nil && res.Username != "" {
			logging.DebugLogger.Println("findLoginForm: форма найдена")
			return loginFormInfo{
				UsernameSelector: res.Username,
				PasswordSelector: res.Password,
				ButtonSelector:   res.Submit,
			}, nil
		}
		time.Sleep(1 * time.Second)
	}
	logging.ErrorLogger.Println("findLoginForm: форма не появилась за 15 секунд")
	return loginFormInfo{}, fmt.Errorf("форма не появилась за 15 секунд")
}
