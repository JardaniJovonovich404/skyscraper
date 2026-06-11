package proxy

import (
	"net/http"
	"net/url"
	"sync"

	"inst/internal/logging"
)

type Manager struct {
	proxies []string
	mu      sync.Mutex
	index   int
}

func NewManager(list []string) *Manager {
	m := &Manager{proxies: list}
	if len(list) == 0 {
		logging.InfoLogger.Println("Менеджер прокси инициализирован без прокси-серверов")
	} else {
		logging.InfoLogger.Printf("Менеджер прокси инициализирован с %d прокси", len(list))
		if logging.ShouldLog(logging.DEBUG) {
			for i, p := range list {
				logging.DebugLogger.Printf("Прокси #%d: %s", i+1, p)
			}
		}
	}
	return m
}

func (m *Manager) NextProxy() func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if len(m.proxies) == 0 {
			logging.DebugLogger.Println("NextProxy: список прокси пуст, возвращаю nil")
			return nil, nil
		}
		proxyStr := m.proxies[m.index]
		currentIndex := m.index
		m.index = (m.index + 1) % len(m.proxies)
		u, err := url.Parse(proxyStr)
		if err != nil {
			logging.ErrorLogger.Printf("Ошибка парсинга прокси %q: %v", proxyStr, err)
			return nil, err
		}
		if logging.ShouldLog(logging.DEBUG) {
			logging.DebugLogger.Printf("Выдан прокси #%d: %s", currentIndex+1, proxyStr)
		}
		return u, nil
	}
}
