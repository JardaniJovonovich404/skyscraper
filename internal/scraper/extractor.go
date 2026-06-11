package scraper

import "fmt"

const countRepliesButtonsScript = `
(function() {
    const selectors = [
        '//button[contains(text(),"View replies")]',
        '//button[contains(text(),"Посмотреть ответы")]',
        '//div[@role="button"][contains(text(),"View replies")]',
        '//div[@role="button"][contains(text(),"Посмотреть ответы")]'
    ];
    let total = 0;
    for (const sel of selectors) {
        const elems = document.evaluate(sel, document, null, XPathResult.ORDERED_NODE_SNAPSHOT_TYPE, null);
        total += elems.snapshotLength;
    }
    return total;
})()
`

const extractDataScript = `
(function() {
    const results = {
        postLikes: 0,
        comments: []
    };
    const seenComments = new Set();

    try {
        const scripts = document.querySelectorAll('script[type="application/json"]');
        for (const script of scripts) {
            const text = script.textContent;
            if (text.includes('"like_count"')) {
                const match = text.match(/"like_count":(\d+)/);
                if (match) {
                    results.postLikes = parseInt(match[1], 10);
                    break;
                }
            }
        }
    } catch(e) { /* ignore */ }

    if (!results.postLikes) {
        const likeButton = document.querySelector('article button[aria-label="Нравится"], article div[role="button"] svg[aria-label="Нравится"]');
        if (likeButton) {
            const container = likeButton.closest('div[role="button"]') || likeButton;
            const sibling = container.nextElementSibling;
            if (sibling && /^\d+$/.test(sibling.innerText.trim())) {
                results.postLikes = parseInt(sibling.innerText.trim(), 10);
            } else {
                const span = container.querySelector('span:not(svg)');
                if (span && /^\d+$/.test(span.innerText.trim())) {
                    results.postLikes = parseInt(span.innerText.trim(), 10);
                }
            }
        }
    }

    document.querySelectorAll('time[datetime]').forEach(timeEl => {
        let commentContainer = timeEl;
        for (let i = 0; i < 10; i++) {
            commentContainer = commentContainer.parentElement;
            if (!commentContainer) return;
            if (commentContainer.querySelector('a[href^="/"][role="link"]:not([href*="/p/"])')) break;
        }

        const usernameLink = commentContainer.querySelector('a[href^="/"][role="link"]:not([href*="/p/"])');
        if (!usernameLink) return;
        const username = usernameLink.innerText.trim();
        if (!username) return;

        let commentText = '';
        const textCandidates = commentContainer.querySelectorAll('[dir="auto"]');
        for (const el of textCandidates) {
            const text = el.innerText.trim();
            if (text && text !== username && !el.querySelector('time') && !el.closest('a[href*="/c/"]')) {
                commentText = text;
                break;
            }
        }
        if (!commentText) {
            const lines = commentContainer.innerText.split('\n');
            for (const line of lines) {
                const trimmed = line.trim();
                if (trimmed && trimmed !== username && !trimmed.includes('·') && !/^\d+[.,\s]*(минут|час|день|нед|ago)/i.test(trimmed)) {
                    commentText = trimmed;
                    break;
                }
            }
        }
        if (!commentText) return;

        const datetime = timeEl.getAttribute('datetime');
        let timestamp = 0;
        if (datetime) {
            const ts = Date.parse(datetime);
            if (!isNaN(ts)) timestamp = Math.floor(ts / 1000);
        }

        let commentLikes = 0;
        const likeSvg = commentContainer.querySelector('svg[aria-label="Нравится"], svg[aria-label="Like"]');
        if (likeSvg) {
            const likeButton = likeSvg.closest('div[role="button"], button');
            if (likeButton) {
                let next = likeButton.nextElementSibling;
                if (next && /^\d+$/.test(next.innerText.trim())) {
                    commentLikes = parseInt(next.innerText.trim(), 10);
                } else {
                    const numberSpan = likeButton.querySelector('span:not(svg)');
                    if (numberSpan && /^\d+$/.test(numberSpan.innerText.trim())) {
                        commentLikes = parseInt(numberSpan.innerText.trim(), 10);
                    }
                }
            }
        }

        let commentLink = '';
        const linkEl = commentContainer.querySelector('a[href*="/c/"]');
        if (linkEl) commentLink = linkEl.href;

        let profileLink = usernameLink.href;

        const uniqueKey = profileLink + '|' + commentText;
        if (seenComments.has(uniqueKey)) return;
        seenComments.add(uniqueKey);

        results.comments.push({
            username: username,
            text: commentText,
            likes: commentLikes,
            profileLink: profileLink,
            commentLink: commentLink,
            timestamp: timestamp
        });
    });

    return results;
})();
`

const extractPostLikesScript = `
(() => {
    try {
        const scripts = document.querySelectorAll('script[type="application/json"]');
        for (const s of scripts) {
            const m = s.textContent.match(/"like_count":(\d+)/);
            if (m) return parseInt(m[1], 10);
        }
    } catch(e) {}
    const btn = document.querySelector('article button[aria-label="Нравится"], article div[role="button"] svg[aria-label="Нравится"]');
    if (btn) {
        const parent = btn.closest('div[role="button"]') || btn;
        const sibling = parent.nextElementSibling;
        if (sibling && /^\d+$/.test(sibling.innerText)) return parseInt(sibling.innerText, 10);
        const span = parent.querySelector('span:not(svg)');
        if (span && /^\d+$/.test(span.innerText)) return parseInt(span.innerText, 10);
    }
    return 0;
})();
`

const scrollToLoadCommentsScript = `
(async function() {
    let container = document.querySelector('div[role="dialog"]');
    if (!container) {
        const scrollables = document.querySelectorAll('*');
        for (const el of scrollables) {
            const style = window.getComputedStyle(el);
            if ((style.overflowY === 'auto' || style.overflowY === 'scroll') && el.scrollHeight > el.clientHeight) {
                if (el.querySelector('time')) {
                    container = el;
                    break;
                }
            }
        }
    }
    if (!container) container = document.documentElement;

    let lastHeight = 0;
    for (let i = 0; i < 20; i++) {
        const currentHeight = container.scrollHeight;
        if (currentHeight === lastHeight) break;
        lastHeight = currentHeight;
        container.scrollTo(0, container.scrollHeight);
        await new Promise(r => setTimeout(r, 800));
    }
})();
`

func clickRepliesButtonScript(index int) string {
	return `(function(idx) {
        const selectors = [
            '//button[contains(text(),"View replies")]',
            '//button[contains(text(),"Посмотреть ответы")]',
            '//div[@role="button"][contains(text(),"View replies")]',
            '//div[@role="button"][contains(text(),"Посмотреть ответы")]'
        ];
        let c = 0;
        for (const sel of selectors) {
            const elems = document.evaluate(sel, document, null, XPathResult.ORDERED_NODE_SNAPSHOT_TYPE, null);
            for (let j = 0; j < elems.snapshotLength; j++) {
                if (c === idx) {
                    elems.snapshotItem(j).click();
                    return;
                }
                c++;
            }
        }
    })()` + fmt.Sprintf("(%d)", index)
}
