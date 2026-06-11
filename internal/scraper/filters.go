package scraper

import (
	"regexp"
	"strings"

	"inst/internal/logging"
)

func isUselessUsername(username string) bool {
	if logging.ShouldLog(logging.DEBUG) {
		logging.DebugLogger.Printf("isUselessUsername: проверка %q", username)
	}
	useless := []string{
		"Главная", "Сообщения", "Интересное", "Reels", "Профиль",
		"Условия", "Конфиденциальность", "Места", "Instagram Lite", "Meta Verified",
		"Поисковый запрос", "Дополнительно", "Создать", "Панель", "Ещё", "Настройки",
		"Нравится", "Ответить", "Действия с комментарием",
		"View replies", "Hide replies", "Посмотреть ответы", "Скрыть ответы",
		"Reply", "Ответить",
	}
	for _, u := range useless {
		if username == u {
			if logging.ShouldLog(logging.DEBUG) {
				logging.DebugLogger.Printf("isUselessUsername: %q признано бесполезным (совпало с %q)", username, u)
			}
			return true
		}
	}
	return false
}

func isUselessText(text string) bool {
	if logging.ShouldLog(logging.DEBUG) {
		logging.DebugLogger.Printf("isUselessText: проверка %q", text)
	}
	useless := []string{
		"Начните переписку.",
		"Ответить",
		"Нравится",
		"Действия с комментарием",
		"View replies", "Hide replies", "Посмотреть ответы", "Скрыть ответы",
	}
	for _, u := range useless {
		if text == u {
			if logging.ShouldLog(logging.DEBUG) {
				logging.DebugLogger.Printf("isUselessText: %q признано бесполезным (совпало с %q)", text, u)
			}
			return true
		}
	}
	return false
}

func isTimeOnlyString(s string) bool {
	if logging.ShouldLog(logging.DEBUG) {
		logging.DebugLogger.Printf("isTimeOnlyString: проверка %q", s)
	}
	patterns := []string{
		`^\d+\s*(нед|недели|недель|неделю|нед\.)`,
		`^\d+\s*(дн|день|дня|дней|дн\.)`,
		`^\d+\s*(час|часа|часов|ч\.)`,
		`^\d+\s*(мин|минуту|минут|мин\.)`,
		`^\d+\s*(сек|секунду|секунд|сек\.)`,
		`^\d+\s*(w|week|weeks)`,
		`^\d+\s*(d|day|days)`,
		`^\d+\s*(h|hour|hours)`,
		`^\d+\s*(min|mins|minute|minutes)`,
		`^\d+\s*(s|sec|secs|second|seconds)`,
		`ago$`,
		`назад$`,
	}
	for _, pat := range patterns {
		if matched, _ := regexp.MatchString(pat, strings.ToLower(s)); matched {
			if logging.ShouldLog(logging.DEBUG) {
				logging.DebugLogger.Printf("isTimeOnlyString: %q соответствует шаблону %q", s, pat)
			}
			return true
		}
	}
	return false
}
