package scraper

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"

	"inst/internal/logging"
)

func LoadCookies(path string) (*cookiejar.Jar, error) {
	logging.InfoLogger.Printf("Загрузка кук из файла: %s", path)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	str := string(data)
	if str == "null" || str == "[]" || str == "" {
		logging.DebugLogger.Println("Файл кук содержит null или пустой массив, создаю новый jar")
		jar, _ := cookiejar.New(nil)
		return jar, nil
	}

	var cookies []*http.Cookie
	if err := json.Unmarshal(data, &cookies); err != nil {
		return nil, err
	}

	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse("https://www.instagram.com")
	jar.SetCookies(u, cookies)

	logging.DebugLogger.Printf("Загружено %d кук из %s", len(cookies), path)
	return jar, nil
}

func SaveCookies(path string, cookies []*http.Cookie) error {
	if len(cookies) == 0 {
		logging.DebugLogger.Println("SaveCookies: список кук пуст, файл не будет создан/обновлён")
		_ = os.Remove(path)
		return nil
	}

	logging.InfoLogger.Printf("Сохранение %d кук в файл: %s", len(cookies), path)
	data, err := json.Marshal(cookies)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func CookiesFromJar(jar *cookiejar.Jar, urlStr string) []*http.Cookie {
	if jar == nil {
		return nil
	}
	u, _ := url.Parse(urlStr)
	return jar.Cookies(u)
}
