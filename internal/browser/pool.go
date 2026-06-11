package browser

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"inst/internal/logging"
	"inst/internal/proxy"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

type Tab struct {
	Ctx  context.Context
	inst *Instance
}

type Instance struct {
	id          int32
	browserCtx  context.Context
	cancel      context.CancelFunc
	allocCancel context.CancelFunc
	userDataDir string
	mu          sync.Mutex
	closed      bool
	maxTabs     int
	activeTabs  int
	useCount    int
	tabs        []*Tab
	firstTab    *Tab
}

type Pool struct {
	tabs              chan *Tab
	maxTotalTabs      int
	maxTabsPerBrowser int
	maxUses           int
	proxyMgr          *proxy.Manager
	userAgents        []string
	cookies           []*http.Cookie

	mu           sync.Mutex
	instances    []*Instance
	allInstances []*Instance
	nextID       int32
	closed       bool
	totalTabs    int
	wg           sync.WaitGroup
}

func NewPool(size int, proxyMgr *proxy.Manager, maxUses int, userAgents []string, cookies []*http.Cookie, maxTabsPerBrowser int) *Pool {
	if size <= 0 {
		size = 10
	}
	if maxTabsPerBrowser <= 0 {
		maxTabsPerBrowser = 2
	}

	pool := &Pool{
		tabs:              make(chan *Tab, size),
		maxTotalTabs:      size,
		maxTabsPerBrowser: maxTabsPerBrowser,
		maxUses:           maxUses,
		proxyMgr:          proxyMgr,
		userAgents:        userAgents,
		cookies:           cookies,
	}

	logging.InfoLogger.Printf("Создание пула браузеров: макс. вкладок=%d, maxUses=%d, вкладок на браузер=%d",
		size, maxUses, maxTabsPerBrowser)
	return pool
}

func (p *Pool) GetTab() (context.Context, func()) {
	if p.isClosed() {
		logging.WarnLogger.Println("GetTab: пул закрыт")
		return nil, func() {}
	}

	select {
	case tab := <-p.tabs:
		if tab == nil {
			logging.WarnLogger.Println("GetTab: канал закрыт, пул остановлен")
			return nil, func() {}
		}
		tab.inst.mu.Lock()
		tab.inst.activeTabs++
		if p.maxUses > 0 {
			tab.inst.useCount++
		}
		tab.inst.mu.Unlock()
		return tab.Ctx, func() { p.releaseTab(tab) }
	default:
	}

	p.mu.Lock()
	if p.totalTabs < p.maxTotalTabs {
		tab, err := p.createTabInExistingOrNewBrowser()
		if err != nil {
			p.mu.Unlock()
			logging.ErrorLogger.Printf("GetTab: ошибка создания вкладки: %v", err)
			goto waitForRelease
		}
		p.totalTabs++
		p.mu.Unlock()

		tab.inst.mu.Lock()
		tab.inst.activeTabs++
		if p.maxUses > 0 {
			tab.inst.useCount++
		}
		tab.inst.mu.Unlock()
		return tab.Ctx, func() { p.releaseTab(tab) }
	}
	p.mu.Unlock()

waitForRelease:
	logging.DebugLogger.Println("GetTab: достигнут лимит вкладок, ожидание освобождения...")
	tab := <-p.tabs
	if tab == nil {
		return nil, func() {}
	}
	tab.inst.mu.Lock()
	tab.inst.activeTabs++
	if p.maxUses > 0 {
		tab.inst.useCount++
	}
	tab.inst.mu.Unlock()
	return tab.Ctx, func() { p.releaseTab(tab) }
}

func (p *Pool) releaseTab(tab *Tab) {
	inst := tab.inst
	inst.mu.Lock()

	if inst.closed {
		inst.mu.Unlock()
		return
	}

	inst.activeTabs--

	destroy := false
	if p.maxUses > 0 && inst.useCount >= p.maxUses && inst.activeTabs == 0 {
		destroy = true
	}

	if destroy {
		inst.closed = true
		inst.mu.Unlock()

		p.mu.Lock()
		for i, in := range p.instances {
			if in == inst {
				p.instances = append(p.instances[:i], p.instances[i+1:]...)
				break
			}
		}
		tabsInInst := len(inst.tabs)
		if inst.firstTab != nil {
			tabsInInst++
		}
		p.totalTabs -= tabsInInst
		p.mu.Unlock()

		go p.destroyInstance(inst)
		return
	}

	inst.mu.Unlock()

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	select {
	case p.tabs <- tab:
	default:
		logging.WarnLogger.Println("releaseTab: канал вкладок переполнен, вкладка потеряна")
	}
	p.mu.Unlock()
}

func (p *Pool) createTabInExistingOrNewBrowser() (*Tab, error) {
	for _, inst := range p.instances {
		if inst.closed {
			continue
		}
		inst.mu.Lock()
		total := len(inst.tabs)
		if inst.firstTab != nil {
			total++
		}
		canCreate := total < inst.maxTabs
		inst.mu.Unlock()
		if canCreate {
			return p.createTabInInstance(inst)
		}
	}

	inst, err := p.createInstance()
	if err != nil {
		return nil, err
	}
	p.instances = append(p.instances, inst)
	return p.createTabInInstance(inst)
}

func (p *Pool) createTabInInstance(inst *Instance) (*Tab, error) {
	inst.mu.Lock()
	defer inst.mu.Unlock()

	if inst.firstTab != nil {
		tab := inst.firstTab
		inst.firstTab = nil
		return tab, nil
	}

	tabCtx, err := createTabInBrowser(inst.browserCtx, p.cookies)
	if err != nil {
		return nil, err
	}
	tab := &Tab{
		Ctx:  tabCtx,
		inst: inst,
	}
	inst.tabs = append(inst.tabs, tab)
	return tab, nil
}

func createTabInBrowser(browserCtx context.Context, cookies []*http.Cookie) (context.Context, error) {
	tabCtx, _ := chromedp.NewContext(browserCtx)
	if len(cookies) > 0 {
		if err := chromedp.Run(tabCtx, setCookiesAction(cookies)); err != nil {
			return nil, fmt.Errorf("установка кук: %w", err)
		}
	}
	return tabCtx, nil
}

func (p *Pool) createInstance() (*Instance, error) {
	// Вызывается при удержании p.mu
	id := atomic.AddInt32(&p.nextID, 1)
	tmpDir := filepath.Join(os.TempDir(), fmt.Sprintf("inst-scraper-%d-%d", id, time.Now().UnixNano()))

	logging.InfoLogger.Printf("Создание инстанса #%d, userDataDir: %s", id, tmpDir)

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-features", "TranslateUI,OptimizationHints"),
		chromedp.Flag("disable-notifications", true),
		chromedp.Flag("disable-extensions", false),
		chromedp.Flag("disable-default-apps", true),
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("disable-infobars", true),
		chromedp.Flag("disable-breakpad", true),
		chromedp.Flag("disable-component-update", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("enable-features", "NetworkService,NetworkServiceInProcess"),
		chromedp.Flag("user-data-dir", tmpDir),
		chromedp.Flag("window-size", "1280,768"),
		chromedp.Flag("window-position", "0,0"),
		chromedp.UserAgent(randomUserAgent(p.userAgents)),
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
	)

	if p.proxyMgr != nil {
		if proxyFunc := p.proxyMgr.NextProxy(); proxyFunc != nil {
			req, _ := http.NewRequest("GET", "http://example.com", nil)
			if proxyURL, err := proxyFunc(req); err == nil && proxyURL != nil {
				opts = append(opts, chromedp.ProxyServer(proxyURL.String()))
			}
		}
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)

	hideWebDriver := `
		Object.defineProperty(navigator, 'webdriver', {get: () => undefined});
		window.chrome = { runtime: {} };
		Object.defineProperty(navigator, 'plugins', {get: () => [1,2,3,4,5]});
		Object.defineProperty(navigator, 'languages', {get: () => ['ru-RU','ru','en-US','en']});
		Object.defineProperty(navigator, 'hardwareConcurrency', {get: () => 4});
		Object.defineProperty(navigator, 'deviceMemory', {get: () => 8});
		delete navigator.__proto__.webdriver;
	`
	if err := chromedp.Run(browserCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		_, err := page.AddScriptToEvaluateOnNewDocument(hideWebDriver).Do(ctx)
		return err
	})); err != nil {
		allocCancel()
		browserCancel()
		return nil, fmt.Errorf("hideWebdriver: %w", err)
	}

	var firstTab *Tab
	if err := chromedp.Run(browserCtx, func() chromedp.Action {
		return chromedp.ActionFunc(func(ctx context.Context) error {
			targets, err := target.GetTargets().Do(ctx)
			if err != nil {
				return err
			}
			for _, t := range targets {
				if t.Type == "page" {
					tabCtx, _ := chromedp.NewContext(browserCtx, chromedp.WithTargetID(t.TargetID))
					if len(p.cookies) > 0 {
						if err := chromedp.Run(tabCtx, setCookiesAction(p.cookies)); err != nil {
							return fmt.Errorf("куки на первую вкладку: %w", err)
						}
					}
					firstTab = &Tab{Ctx: tabCtx}
					break
				}
			}
			return nil
		})
	}()); err != nil {
		allocCancel()
		browserCancel()
		return nil, fmt.Errorf("первая вкладка: %w", err)
	}

	if firstTab == nil {
		allocCancel()
		browserCancel()
		return nil, fmt.Errorf("не найдено ни одной вкладки после запуска")
	}

	inst := &Instance{
		id:          id,
		browserCtx:  browserCtx,
		cancel:      browserCancel,
		allocCancel: allocCancel,
		userDataDir: tmpDir,
		maxTabs:     p.maxTabsPerBrowser,
		firstTab:    firstTab,
	}
	firstTab.inst = inst

	p.allInstances = append(p.allInstances, inst)
	logging.InfoLogger.Printf("Инстанс #%d добавлен в allInstances (всего: %d)", id, len(p.allInstances))
	logging.InfoLogger.Printf("Инстанс #%d создан, первая вкладка захвачена", id)
	return inst, nil
}

func (p *Pool) destroyInstance(inst *Instance) {
	inst.mu.Lock()
	if inst.closed {
		logging.InfoLogger.Printf("destroyInstance: инстанс #%d уже закрыт, выход", inst.id)
		inst.mu.Unlock()
		return
	}
	inst.closed = true
	id := inst.id
	userDir := inst.userDataDir
	logging.InfoLogger.Printf("destroyInstance: начало уничтожения инстанса #%d", id)

	var tabsToCancel []context.Context
	for _, tab := range inst.tabs {
		tabsToCancel = append(tabsToCancel, tab.Ctx)
	}
	if inst.firstTab != nil {
		tabsToCancel = append(tabsToCancel, inst.firstTab.Ctx)
	}
	inst.mu.Unlock()

	logging.InfoLogger.Printf("destroyInstance: отмена контекстов %d вкладок для инстанса #%d", len(tabsToCancel), id)
	for _, tabCtx := range tabsToCancel {
		chromedp.Cancel(tabCtx)
	}

	logging.InfoLogger.Printf("destroyInstance: отмена browserCtx и allocCancel для инстанса #%d", id)
	inst.cancel()
	inst.allocCancel()

	p.wg.Add(1)
	go func(dir string, id int32) {
		defer p.wg.Done()
		time.Sleep(500 * time.Millisecond)
		if err := os.RemoveAll(dir); err != nil {
			logging.WarnLogger.Printf("destroyInstance: инстанс #%d: не удалось удалить временную папку %s: %v", id, dir, err)
		} else {
			logging.DebugLogger.Printf("destroyInstance: инстанс #%d: временная папка %s удалена", id, dir)
		}
	}(userDir, id)

	logging.InfoLogger.Printf("destroyInstance: инстанс #%d уничтожен", id)
}

func (p *Pool) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

func (p *Pool) Close() {
	p.mu.Lock()
	if p.closed {
		logging.InfoLogger.Println("Close: пул уже закрыт")
		p.mu.Unlock()
		return
	}
	p.closed = true
	logging.InfoLogger.Printf("Close: начало закрытия пула. Текущие инстансы: %d, allInstances: %d", len(p.instances), len(p.allInstances))
	instances := p.instances
	p.instances = nil
	all := append([]*Instance{}, p.allInstances...)
	p.mu.Unlock()

	close(p.tabs)
	var remainingTabs []*Tab
	for tab := range p.tabs {
		if tab != nil {
			remainingTabs = append(remainingTabs, tab)
		}
	}
	logging.InfoLogger.Printf("Close: после осушения канала tabs, получено %d оставшихся вкладок", len(remainingTabs))

	destroyed := make(map[*Instance]bool)
	for _, inst := range instances {
		if !destroyed[inst] {
			p.destroyInstance(inst)
			destroyed[inst] = true
		}
	}
	for _, inst := range all {
		if !destroyed[inst] {
			p.destroyInstance(inst)
			destroyed[inst] = true
		}
	}

	for _, tab := range remainingTabs {
		inst := tab.inst
		inst.mu.Lock()
		if !inst.closed {
			chromedp.Cancel(tab.Ctx)
			inst.closed = true
			inst.mu.Unlock()
			p.destroyInstance(inst)
			destroyed[inst] = true
		} else {
			inst.mu.Unlock()
		}
	}

	killAllChromeProcesses()

	p.wg.Wait()
	logging.InfoLogger.Printf("Close: пул закрыт, уничтожено %d инстансов", len(destroyed))
}

func KillAllChromeProcesses() {
	killAllChromeProcesses()
}

func killAllChromeProcesses() {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("powershell", "-Command",
			`Get-CimInstance Win32_Process -Filter "CommandLine like '%inst-scraper%'" | ForEach-Object { Stop-Process -Id $_.ProcessId -Force }`)
		if err := cmd.Run(); err != nil {
			logging.WarnLogger.Printf("killAllChromeProcesses: ошибка завершения процессов: %v", err)
		} else {
			logging.InfoLogger.Println("killAllChromeProcesses: все процессы Chrome с 'inst-scraper' завершены")
		}
	} else {
		cmd := exec.Command("pkill", "-f", "inst-scraper")
		if err := cmd.Run(); err != nil {
			logging.WarnLogger.Printf("killAllChromeProcesses: ошибка завершения процессов: %v", err)
		} else {
			logging.InfoLogger.Println("killAllChromeProcesses: все процессы Chrome с 'inst-scraper' завершены")
		}
	}
}

func randomUserAgent(agents []string) string {
	if len(agents) == 0 {
		return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	}
	return agents[rand.Intn(len(agents))]
}
