package scraper

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"inst/internal/browser"
	"inst/internal/logging"
	"inst/internal/models"

	"github.com/chromedp/chromedp"
)

type CommentsCollector struct {
	browserPool *browser.Pool
	targetUsers []string
}

func NewCommentsCollector(pool *browser.Pool, targetUsers []string) *CommentsCollector {
	logging.InfoLogger.Printf("Создание CommentsCollector, фильтр по пользователям: %v", targetUsers)
	return &CommentsCollector{
		browserPool: pool,
		targetUsers: targetUsers,
	}
}

func (c *CommentsCollector) CollectCommentsForPost(ctx context.Context, postURL string) ([]models.Comment, error) {
	postCode := extractPostCode(postURL)
	logging.InfoLogger.Printf("Сбор комментариев для поста %s (%s)", postCode, postURL)
	start := time.Now()

	maxRetries := 3
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		logging.InfoLogger.Printf("Попытка %d/%d для поста %s", attempt, maxRetries, postCode)
		attemptStart := time.Now()
		comments, err := c.tryCollectPost(ctx, postURL, postCode)
		if err == nil {
			logging.InfoLogger.Printf("Попытка %d успешна, получено %d комментариев за %v", attempt, len(comments), time.Since(attemptStart))
			logging.InfoLogger.Printf("Сбор комментариев для %s завершён за %v, всего %d", postCode, time.Since(start), len(comments))
			return comments, nil
		}
		lastErr = err
		logging.WarnLogger.Printf("Попытка %d не удалась через %v: %v", attempt, time.Since(attemptStart), err)
		if attempt < maxRetries {
			wait := time.Duration(attempt*5) * time.Second
			logging.DebugLogger.Printf("Ожидание %v перед следующей попыткой", wait)
			time.Sleep(wait)
		}
	}
	logging.ErrorLogger.Printf("Все попытки для %s исчерпаны: %v", postCode, lastErr)
	return nil, fmt.Errorf("все попытки исчерпаны: %w", lastErr)
}

type rawCommentData struct {
	Username    string `json:"username"`
	Text        string `json:"text"`
	Likes       int    `json:"likes"`
	ProfileLink string `json:"profileLink"`
	CommentLink string `json:"commentLink"`
	Timestamp   int64  `json:"timestamp"`
}

func (c *CommentsCollector) tryCollectPost(ctx context.Context, postURL, postCode string) ([]models.Comment, error) {
	logging.DebugLogger.Printf("tryCollectPost: начало обработки %s", postCode)
	start := time.Now()

	tabCtx, release := c.browserPool.GetTab()
	defer func() {
		release()
		logging.DebugLogger.Printf("tryCollectPost: вкладка возвращена (пост %s)", postCode)
	}()

	postCtx, cancel := context.WithTimeout(tabCtx, 2*time.Minute)
	defer cancel()

	logging.DebugLogger.Printf("Шаг 1: закрытие предыдущих диалогов")
	if err := chromedp.Run(postCtx, closeAllDialogs()); err != nil {
		logging.WarnLogger.Printf("Ошибка при закрытии диалогов: %v", err)
	}

	logging.DebugLogger.Printf("Шаг 2: навигация к %s", postURL)
	if err := chromedp.Run(postCtx,
		chromedp.Navigate(postURL),
		checkLoginRedirect(),
	); err != nil {
		logging.ErrorLogger.Printf("Ошибка навигации: %v", err)
		return nil, fmt.Errorf("навигация: %w", err)
	}

	logging.DebugLogger.Printf("Шаг 3: ожидание загрузки контента")
	if err := chromedp.Run(postCtx, waitForPostContent()); err != nil {
		logging.ErrorLogger.Printf("Ошибка загрузки поста: %v", err)
		return nil, fmt.Errorf("загрузка поста: %w", err)
	}

	logging.DebugLogger.Printf("Шаг 4: закрытие всплывающих окон")
	_ = chromedp.Run(postCtx, dismissPopups())
	_ = chromedp.Run(postCtx, dismissCookieBanners())

	logging.InfoLogger.Println("Загружаем все комментарии…")
	if err := loadAllComments(postCtx); err != nil {
		logging.WarnLogger.Printf("Ошибка загрузки комментариев: %v", err)
	}

	logging.InfoLogger.Println("Раскрываем ответы…")
	if err := c.expandAllReplies(postCtx); err != nil {
		logging.WarnLogger.Printf("Ошибка раскрытия ответов: %v", err)
	}

	logging.InfoLogger.Println("Закрываем модальное окно...")
	if err := chromedp.Run(postCtx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			err := chromedp.Evaluate(`(function() {
                const d = document.querySelector('div[role="dialog"]');
                if (d) { d.remove(); return 'removed'; }
                return 'not found';
            })()`, nil).Do(ctx)
			if err != nil {
				return err
			}
			time.Sleep(1 * time.Second)
			return nil
		}),
	); err != nil {
		logging.WarnLogger.Printf("Ошибка при закрытии диалога: %v", err)
	}

	logging.DebugLogger.Printf("Шаг 8: финальное извлечение комментариев")
	allRaw, err := extractCommentsViaTimeElements(postCtx)
	if err != nil {
		logging.ErrorLogger.Printf("Ошибка извлечения комментариев: %v", err)
		return nil, fmt.Errorf("извлечение комментариев: %w", err)
	}

	logging.DebugLogger.Printf("Шаг 9: фильтрация (вход: %d сырых)", len(allRaw))
	filteredRaw := make([]rawCommentData, 0, len(allRaw))
	for _, r := range allRaw {
		if r.Username == "" || r.Text == "" {
			continue
		}
		if isTimeOnlyString(r.Text) {
			logging.DebugLogger.Printf("Пропущен комментарий с временной меткой: %s", r.Text)
			continue
		}
		if r.CommentLink != "" && !strings.Contains(r.CommentLink, postCode) {
			logging.DebugLogger.Printf("Пропущен комментарий, не принадлежащий посту %s: %s", postCode, r.CommentLink)
			continue
		}
		filteredRaw = append(filteredRaw, r)
	}
	logging.InfoLogger.Printf("После фильтрации осталось %d комментариев", len(filteredRaw))

	commentsMap := make(map[string]models.Comment)
	for _, r := range filteredRaw {
		commentID := models.GenerateCommentID(postCode, r.Username, r.Text, r.Timestamp)
		if _, exists := commentsMap[commentID]; exists {
			continue
		}
		if isUselessUsername(r.Username) || isUselessText(r.Text) {
			continue
		}
		commentsMap[commentID] = models.Comment{
			ID:        commentID,
			Username:  r.Username,
			Text:      r.Text,
			Timestamp: time.Unix(r.Timestamp, 0).UTC(),
			PostCode:  postCode,
			PostURL:   postURL,
		}
	}
	logging.DebugLogger.Printf("После дедупликации осталось %d уникальных комментариев", len(commentsMap))

	if len(commentsMap) == 0 {
		logging.WarnLogger.Printf("Пост %s: не собрано ни одного комментария, сохраняю HTML для отладки", postCode)
		var debugHTML string
		_ = chromedp.Run(postCtx, chromedp.OuterHTML("html", &debugHTML))
		if debugHTML != "" {
			fname := fmt.Sprintf("debug_post_%s_%d.html", postCode, time.Now().Unix())
			if err := os.WriteFile(fname, []byte(debugHTML), 0644); err != nil {
				logging.ErrorLogger.Printf("Ошибка сохранения debug HTML: %v", err)
			} else {
				logging.InfoLogger.Printf("Сохранён дебаг‑HTML: %s", fname)
			}
		}
	}

	result := make([]models.Comment, 0, len(commentsMap))
	for _, cmt := range commentsMap {
		result = append(result, cmt)
	}
	elapsed := time.Since(start)
	logging.InfoLogger.Printf("Пост %s: собрано %d комментариев за %v", postCode, len(result), elapsed)
	return result, nil
}

func loadAllComments(ctx context.Context) error {
	logging.DebugLogger.Println("loadAllComments: начало загрузки всех комментариев")
	lastCount := 0
	stableCount := 0
	const maxStable = 2
	const maxIter = 20
	const clickTimeout = 3 * time.Second
	const scrollWait = 1 * time.Second

	start := time.Now()
	for i := 0; i < maxIter; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		clickCtx, clickCancel := context.WithTimeout(ctx, clickTimeout)
		_ = chromedp.Run(clickCtx, clickLoadMoreComments())
		clickCancel()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}

		if err := scrollCommentContainer(ctx); err != nil {
			logging.WarnLogger.Printf("Ошибка прокрутки: %v", err)
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(scrollWait):
		}

		var count int
		countCtx, countCancel := context.WithTimeout(ctx, 3*time.Second)
		err := chromedp.Run(countCtx, chromedp.Evaluate(`document.querySelectorAll('time[datetime]').length`, &count))
		countCancel()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			logging.WarnLogger.Printf("Ошибка подсчёта <time>: %v", err)
			continue
		}

		logging.DebugLogger.Printf("loadAllComments: итерация %d, <time> = %d (за %v)", i+1, count, time.Since(start))

		if count == lastCount && count > 0 {
			stableCount++
		} else {
			stableCount = 0
		}
		lastCount = count

		if stableCount >= maxStable {
			logging.InfoLogger.Printf("loadAllComments: стабилизировано (%d за %v)", count, time.Since(start))
			return nil
		}
	}
	logging.WarnLogger.Printf("loadAllComments: достигнут лимит итераций (%d) за %v", maxIter, time.Since(start))
	return nil
}

func (c *CommentsCollector) expandAllReplies(ctx context.Context) error {
	logging.DebugLogger.Println("expandAllReplies: начинаем раскрытие ответов")
	start := time.Now()
	deadline := time.Now().Add(30 * time.Second)

	clickRepliesScript := `(function() {
		function isViewRepliesButton(text) {
			if (!text) return false;
			const t = text.trim();
			const englishPatterns = [
				/^view\s+all\s+replies/i,
				/^view\s+replies/i,
				/^show\s+all\s+replies/i,
				/^see\s+all\s+replies/i,
				/^view\s+all\s+\d+\s+replies/i,
				/^view\s+\d+\s+replies/i,
				/^show\s+all\s+\d+\s+replies/i,
				/^see\s+all\s+\d+\s+replies/i,
				/^view\s+all\s+comments/i
			];
			const russianPatterns = [
				/^смотреть\s+все\s+ответы/i,
				/^показать\s+все\s+ответы/i,
				/^смотреть\s+ответы/i,
				/^показать\s+ответы/i,
				/^смотреть\s+\d+\s+ответ[а-я]*/i,
				/^показать\s+\d+\s+ответ[а-я]*/i,
				/^смотреть\s+все\s+\d+\s+ответ[а-я]*/i,
				/^показать\s+все\s+\d+\s+ответ[а-я]*/i
			];
			const patterns = englishPatterns.concat(russianPatterns);
			for (let p of patterns) {
				if (p.test(t)) return true;
			}
			if (/^view\s+replies\s*\(\d+\)/i.test(t)) return true;
			if (/^смотреть\s+ответы\s*\(\d+\)/i.test(t)) return true;
			if (/^показать\s+ответы\s*\(\d+\)/i.test(t)) return true;
			return false;
		}

		const buttons = document.querySelectorAll('[role="button"]');
		for (let btn of buttons) {
			const text = (btn.innerText || '').trim();
			if (isViewRepliesButton(text)) {
				btn.scrollIntoView({block: 'center', behavior: 'smooth'});
				const ev = new MouseEvent('click', {bubbles: true, cancelable: true});
				btn.dispatchEvent(ev);
				return true;
			}
		}
		const allButtonsXPath = "//*[@role='button']";
		const result = document.evaluate(allButtonsXPath, document, null, XPathResult.ORDERED_NODE_SNAPSHOT_TYPE, null);
		for (let i = 0; i < result.snapshotLength; i++) {
			const btn = result.snapshotItem(i);
			const text = (btn.innerText || '').trim();
			if (isViewRepliesButton(text)) {
				btn.scrollIntoView({block: 'center', behavior: 'smooth'});
				btn.dispatchEvent(new MouseEvent('click', {bubbles: true}));
				return true;
			}
		}
		return false;
	})()`

	for {
		if time.Now().After(deadline) {
			logging.WarnLogger.Printf("expandAllReplies: таймаут (%v)", time.Since(start))
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		var clicked bool
		err := chromedp.Run(ctx, chromedp.Evaluate(clickRepliesScript, &clicked))
		if err != nil {
			logging.WarnLogger.Printf("expandAllReplies: ошибка выполнения скрипта: %v", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}

		if !clicked {
			logging.InfoLogger.Printf("expandAllReplies: больше кнопок раскрытия ответов не найдено (за %v)", time.Since(start))
			return nil
		}

		logging.DebugLogger.Println("expandAllReplies: клик выполнен, ожидаем загрузки ответов...")
		time.Sleep(500 * time.Millisecond)
	}
}

func scrollCommentContainer(ctx context.Context) error {
	scrollCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	script := `(function() {
        const times = document.querySelectorAll('time[datetime]');
        for (const time of times) {
            let el = time.parentElement;
            while (el && el !== document.body) {
                const style = window.getComputedStyle(el);
                const oy = style.getPropertyValue('overflow-y');
                if ((oy === 'auto' || oy === 'scroll') && el.scrollHeight > el.clientHeight) {
                    el.scrollTop += 500;
                    return true;
                }
                el = el.parentElement;
            }
        }
        const dia = document.querySelector('div[role="dialog"]');
        if (dia) { dia.scrollTop += 500; return true; }
        const pres = document.querySelector('div[role="presentation"]');
        if (pres) { pres.scrollTop += 500; return true; }
        window.scrollBy(0, 300);
        return false;
    })()`

	var found bool
	if err := chromedp.Run(scrollCtx, chromedp.Evaluate(script, &found)); err != nil {
		return fmt.Errorf("scrollCommentContainer: %w", err)
	}
	if !found {
		logging.WarnLogger.Println("scrollCommentContainer: использован fallback-скролл окна")
	}
	return nil
}

func extractCommentsViaTimeElements(ctx context.Context) ([]rawCommentData, error) {
	var result struct {
		Comments []rawCommentData `json:"comments"`
		Debug    struct {
			TotalTimeElems     int      `json:"totalTimeElems"`
			Processed          int      `json:"processed"`
			SkippedNoContainer int      `json:"skippedNoContainer"`
			SkippedNoProfile   int      `json:"skippedNoProfile"`
			SkippedNoText      int      `json:"skippedNoText"`
			SkippedServiceText int      `json:"skippedServiceText"`
			SkippedOther       int      `json:"skippedOther"`
			SampleContainers   []string `json:"sampleContainers"`
		} `json:"debug"`
	}

	script := `(function() {
    const results = [];
    const seenKeys = new Set();
    const debug = {
        totalTimeElems: 0,
        processed: 0,
        skippedNoContainer: 0,
        skippedNoProfile: 0,
        skippedNoText: 0,
        skippedServiceText: 0,
        skippedOther: 0,
        sampleContainers: []
    };

    const SERVICE_TEXTS = [
        '1 отметка "Нравится"',
        'Комментариев пока нет.',
        'Комментировать',
        'Нравится',
        'Ответить'
    ];

    function isTimeString(s) {
        return /^\d+[.,\s]*(min|h|d|w|ago|минут|минуту|мин\.?|часов|час|ч\.?|день|дня|дней|нед\.?|недели|недель|г\.?|г)\b\.?\s*$/i.test(s.trim());
    }

    function isServiceText(s) {
        return SERVICE_TEXTS.some(t => s.trim() === t);
    }

    function findBestText(container, username) {
        const profileLinks = container.querySelectorAll('a[href^="/"][role="link"]:not([href*="/p/"]):not([href*="/reel/"])');
        const profileNames = new Set();
        profileLinks.forEach(link => {
            const name = (link.querySelector('span') || link).innerText.trim();
            if (name) profileNames.add(name);
        });

        const textNodes = [];
        const walk = document.createTreeWalker(container, NodeFilter.SHOW_TEXT, null, false);
        while (walk.nextNode()) {
            const node = walk.currentNode;
            const parent = node.parentElement;
            if (parent.closest('a[role="link"]') === container.querySelector('a[role="link"]')) continue;
            if (parent.closest('button')) continue;
            if (parent.closest('[role="button"]')) continue;
            if (parent.closest('time')) continue;
            const t = node.textContent.trim();
            if (t && t !== username && !isTimeString(t) && !isServiceText(t)) {
                textNodes.push(t);
            }
        }

        const filtered = textNodes.filter(t => !profileNames.has(t));
        let best = '';
        for (const t of filtered) {
            if (t.length > best.length) best = t;
        }
        return best;
    }

    function findCommentContainer(timeEl) {
        let el = timeEl.parentElement;
        let steps = 0;
        while (el && el !== document.body && steps < 12) {
            const profileLink = el.querySelector('a[href^="/"][role="link"]:not([href*="/p/"]):not([href*="/reel/"])');
            if (profileLink) {
                const text = findBestText(el, '');
                if (text && text.length >= 2) return el;
            }
            el = el.parentElement;
            steps++;
        }
        return null;
    }

    const timeElems = document.querySelectorAll('time[datetime]');
    debug.totalTimeElems = timeElems.length;

    for (const timeEl of timeElems) {
        const container = findCommentContainer(timeEl);
        if (!container) {
            debug.skippedNoContainer++;
            if (debug.sampleContainers.length < 5) {
                let p = timeEl.parentElement;
                for (let i = 0; i < 3 && p; i++) {
                    debug.sampleContainers.push(p.className || p.tagName);
                    p = p.parentElement;
                }
            }
            continue;
        }

        const profileLink = container.querySelector('a[href^="/"][role="link"]:not([href*="/p/"]):not([href*="/reel/"])');
        if (!profileLink) { debug.skippedNoProfile++; continue; }
        const usernameElem = profileLink.querySelector('span') || profileLink;
        const username = usernameElem.innerText.trim();
        if (!username) { debug.skippedNoProfile++; continue; }

        const bestText = findBestText(container, username);
        if (!bestText || bestText.length < 2) { debug.skippedNoText++; continue; }
        if (isServiceText(bestText)) { debug.skippedServiceText++; continue; }

        let timestamp = 0;
        const dt = timeEl.getAttribute('datetime');
        if (dt) { const ts = Date.parse(dt); if (!isNaN(ts)) timestamp = Math.floor(ts / 1000); }

        let likes = 0;
        const allText = container.innerText || '';
        const match = allText.match(/Нравится:\s*(\d+)/);
        if (match) {
            likes = parseInt(match[1], 10);
        }

        let commentLink = '';
        const linkEl = container.querySelector('a[href*="/c/"]');
        if (linkEl) commentLink = linkEl.href;

        const uniqueKey = commentLink || (profileLink.href + '|' + bestText);
        if (seenKeys.has(uniqueKey)) continue;
        seenKeys.add(uniqueKey);

        results.push({
            username: username,
            text: bestText,
            likes: likes,
            profileLink: profileLink.href,
            commentLink: commentLink,
            timestamp: timestamp
        });
        debug.processed++;
    }

    return { comments: results, debug: debug };
})()`

	evalCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := chromedp.Run(evalCtx, chromedp.Evaluate(script, &result)); err != nil {
		return nil, fmt.Errorf("ошибка выполнения JS: %w", err)
	}

	logging.DebugLogger.Printf("Diagnostics: totalTime=%d processed=%d skippedNoContainer=%d skippedNoProfile=%d skippedNoText=%d skippedServiceText=%d",
		result.Debug.TotalTimeElems, result.Debug.Processed,
		result.Debug.SkippedNoContainer, result.Debug.SkippedNoProfile, result.Debug.SkippedNoText,
		result.Debug.SkippedServiceText)

	filtered := make([]rawCommentData, 0, len(result.Comments))
	for _, r := range result.Comments {
		if r.Username == "" || r.Text == "" || len(r.Text) < 2 {
			continue
		}
		if isUselessUsername(r.Username) || isUselessText(r.Text) {
			continue
		}
		filtered = append(filtered, r)
	}
	logging.DebugLogger.Printf("Найдено %d комментариев после фильтрации", len(filtered))
	return filtered, nil
}

func extractPostCode(postURL string) string {
	for _, sep := range []string{"/p/", "/reel/"} {
		if idx := strings.Index(postURL, sep); idx != -1 {
			rest := postURL[idx+len(sep):]
			var code string
			if slash := strings.IndexByte(rest, '/'); slash != -1 {
				code = rest[:slash]
			} else {
				code = rest
			}
			if code != "" {
				return code
			}
		}
	}
	return ""
}
