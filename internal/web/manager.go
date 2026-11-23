package web

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Ozzmosis94/happ-gateway/internal/check"
	"github.com/Ozzmosis94/happ-gateway/internal/device"
	"github.com/Ozzmosis94/happ-gateway/internal/profile"
	"github.com/Ozzmosis94/happ-gateway/internal/store"
	"github.com/Ozzmosis94/happ-gateway/internal/xray"
)

// Options controls how the management service exposes proxy listeners.
// The web service only supports proxy mode; it never modifies host routes.
type Options struct {
	Home         string
	ProxyListen  string
	AllowLAN     bool
	AllowedCIDRs []string
}

type Manager struct {
	mu         sync.Mutex
	recoveryMu sync.Mutex
	history map[string]nodeHistory
	events []GatewayEvent
 phase string
 checkedCandidates int
 totalCandidates int
 retryFailures int
 refreshFailures int
 refreshPending bool
 nextRefreshAt time.Time
	opts       Options
	store      *store.Store
	instance   *xray.Instance
	current    *Connection
	generation uint64
	health     Health
	failures   int
	autoRetryAt time.Time
}

type Subscription struct {
	Name      string            `json:"name"`
	Title     string            `json:"title"`
	Active    bool              `json:"active"`
	Count     int               `json:"count"`
	UpdatedAt string            `json:"updatedAt,omitempty"`
	UserInfo  *profile.UserInfo `json:"userInfo,omitempty"`
}

type Server struct {
	Index     int    `json:"index"`
	Tag       string `json:"tag"`
	Protocol  string `json:"protocol"`
	Address   string `json:"address"`
	Port      int    `json:"port"`
	Supported bool   `json:"supported"`
}

type Connection struct {
	Subscription string    `json:"subscription"`
	Index        int       `json:"index"`
	Tag          string    `json:"tag"`
	Address      string    `json:"address"`
	Port         int       `json:"port"`
	Protocol     string    `json:"protocol"`
	StartedAt    time.Time `json:"startedAt"`
}

type Health struct {
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
	Healthy   bool       `json:"healthy"`
	ExitIP    string     `json:"exitIp,omitempty"`
	Error     string     `json:"error,omitempty"`
}

type Status struct {
 Phase string `json:"phase"`
 Checked int `json:"checked"`
 Total int `json:"total"`
 RetryAt *time.Time `json:"retryAt,omitempty"`
 RefreshRetryAt *time.Time `json:"refreshRetryAt,omitempty"`
	AutoConnect bool `json:"autoConnect"`
	Failures int `json:"failures"`
	Running    bool        `json:"running"`
	Connection *Connection `json:"connection,omitempty"`
	Health     Health      `json:"health"`
	ProxySOCKS string      `json:"proxySocks"`
	ProxyHTTP  string      `json:"proxyHttp"`
}

func NewManager(opts Options) (*Manager, error) {
	if opts.Home == "" {
		return nil, errors.New("DATA_DIR is required")
	}
	if opts.ProxyListen == "" {
		opts.ProxyListen = "127.0.0.1"
	}
	if !opts.AllowLAN {
		if opts.ProxyListen != "127.0.0.1" || len(opts.AllowedCIDRs) > 0 {
			return nil, errors.New("non-loopback proxy or CIDRs require PROXY_ALLOW_LAN=true")
		}
	} else {
		ip, err := netip.ParseAddr(opts.ProxyListen)
		if err != nil || !ip.Is4() || !ip.IsPrivate() {
			return nil, errors.New("PROXY_LISTEN must be a specific private IPv4 address (not 0.0.0.0)")
		}
		for _, cidr := range opts.AllowedCIDRs {
			p, err := netip.ParsePrefix(cidr)
			if err != nil || !p.Addr().Is4() || !p.Addr().IsPrivate() || p.Bits() < 16 || p.Masked() != p {
				return nil, fmt.Errorf("invalid PROXY_ALLOW_CIDRS entry: %q", cidr)
			}
		}
	}
	st, err := store.Open(opts.Home)
	if err != nil {
		return nil, err
	}
	m := &Manager{opts: opts, store: st}
	m.loadHistory()
 m.loadEvents()
 return m,nil
}

func (m *Manager) Subscriptions() []Subscription {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := make([]Subscription, 0)
	for _, s := range m.store.Subscriptions() {
		items = append(items, Subscription{Name: s.Name, Title: s.Title, Active: s.Name == m.store.Active(), Count: len(s.Links), UpdatedAt: s.UpdatedAt, UserInfo: s.UserInfo})
	}
	return items
}

func (m *Manager) Servers(name string) ([]Server, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if name == "" {
		name = m.store.Active()
	}
	sub, ok := m.store.Find(name)
	if !ok {
		return nil, fmt.Errorf("subscription %q not found", name)
	}
	out := make([]Server, 0)
	for i, s := range sub.Servers() {
		out = append(out, Server{Index: i + 1, Tag: s.Tag, Protocol: s.Protocol, Address: s.Address, Port: s.Port, Supported: xray.Supported(s.Protocol)})
	}
	return out, nil
}

func validateSubscriptionURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return errors.New("enter a valid HTTP(S) subscription URL without credentials or fragment")
	}
	return nil
}

func (m *Manager) fetch(ctx context.Context, rawURL, name, userAgent string) (store.SubEntry, error) {
	if err := validateSubscriptionURL(rawURL); err != nil {
		return store.SubEntry{}, err
	}
	deviceID, err := device.Load(m.opts.Home)
	if err != nil {
		return store.SubEntry{}, fmt.Errorf("load device identity: %w", err)
	}
	if userAgent == "" {
		userAgent = profile.DefaultUserAgent
	}
	sub, err := profile.Fetch(ctx, rawURL, userAgent, deviceID.Headers())
	if err != nil {
		return store.SubEntry{}, err
	}
	links := make([]string, 0, len(sub.Servers))
	for _, s := range sub.Servers {
		links = append(links, s.Raw)
	}
	return store.SubEntry{Name: name, URL: rawURL, UserAgent: userAgent, Title: sub.Title, SupportURL: sub.SupportURL, UpdateInterval: sub.UpdateInterval, UserInfo: sub.UserInfo, UpdatedAt: time.Now().Format(time.RFC3339), Links: links}, nil
}

func (m *Manager) Add(ctx context.Context, name, rawURL string) error {
	name = strings.TrimSpace(name)
	if len(name) < 1 || len(name) > 64 || strings.ContainsAny(name, "/\\\x00\r\n") {
		return errors.New("subscription name must be 1–64 characters without slashes")
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	entry, err := m.fetch(ctx, rawURL, name, profile.DefaultUserAgent)
	if err != nil {
		return err
	}
	if len(entry.Links) == 0 {
		return errors.New("subscription has no supported servers")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.store.Find(name); exists {
		return fmt.Errorf("subscription %q already exists", name)
	}
	if err := m.store.Upsert(entry); err != nil { return err }
	return m.store.SetAutoConnect(true)
}

func (m *Manager) Update(ctx context.Context, name string, force bool) error {
	m.mu.Lock()
	old, ok := m.store.Find(name)
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("subscription %q not found", name)
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	next, err := m.fetch(ctx, old.URL, name, old.UserAgent)
	if err != nil {
		return err
	}
	if len(next.Links) == 0 {
		return errors.New("update returned zero servers; cached servers preserved")
	}
	if len(old.Links) > 0 && len(next.Links)*2 < len(old.Links) && !force {
		return ErrLargeDrop
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, stillExists := m.store.Find(name); !stillExists {
		return errors.New("subscription was removed while updating")
	}
	return m.store.Upsert(next)
}

var ErrLargeDrop = errors.New("server count dropped by more than half; confirm update")

func (m *Manager) Activate(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.store.SetActive(name)
}

func (m *Manager) Remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.store.Remove(name); err != nil {
		return err
	}
	if m.current != nil && m.current.Subscription == name {
		return m.stopLocked()
	}
	return nil
}

func (m *Manager) Connect(name string, index int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connectLocked(name,index)
}

func (m *Manager) connectLocked(name string,index int) error {
	if name == "" {
		name = m.store.Active()
	}
	sub, ok := m.store.Find(name)
	if !ok {
		return fmt.Errorf("subscription %q not found", name)
	}
	servers := sub.Servers()
	if index < 1 || index > len(servers) {
		return fmt.Errorf("server index %d is out of range", index)
	}
	s := servers[index-1]
	if !xray.Supported(s.Protocol) {
		return fmt.Errorf("protocol %q is not supported by xray-core", s.Protocol)
	}
	cfg, err := xray.BuildConfig(s, xray.Options{SocksPort: 10808, HTTPPort: 10809, DNSProxy: true, Listen: m.opts.ProxyListen, LANEnabled: m.opts.AllowLAN, LANCIDRs: m.opts.AllowedCIDRs})
	if err != nil {
		return err
	}
	raw, err := cfg.JSON()
	if err != nil {
		return err
	}
	if err := m.stopLocked(); err != nil {
		return fmt.Errorf("stop previous connection: %w", err)
	}
	inst, err := xray.Start(raw)
	if err != nil {
		return fmt.Errorf("start xray: %w", err)
	}
	preference := &store.ConnectionPreference{Subscription: name, LinkHash: store.HashLink(s.Raw)}
	if err := m.store.SetLastConnection(preference); err != nil {
		_ = inst.Close()
		return fmt.Errorf("persist selected server: %w", err)
	}
	m.instance = inst
	m.current = &Connection{Subscription: name, Index: index, Tag: s.Tag, Address: s.Address, Port: s.Port, Protocol: s.Protocol, StartedAt: time.Now()}
	m.health = Health{}
 m.failures=0
 m.retryFailures=0
 m.phase="connecting"
 m.generation++
	if err:=m.store.SetAutoConnect(true);err!=nil {log.Printf("cannot save automatic mode: %v",err)}
 m.appendEventLocked("connection","info",fmt.Sprintf("Запущен сервер %s (%s:%d), проверяется доступность",s.Tag,s.Address,s.Port))
	go m.checkOnce() // Check immediately after connecting; stale results are generation-guarded.
	return nil
}

func (m *Manager) stopLocked() error {
	m.current = nil
	m.health = Health{}
	m.generation++
	if m.instance == nil {
		return nil
	}
	instance := m.instance
	m.instance = nil
	return instance.Close()
}
// Disconnect deliberately forgets the selected server, so an explicit stop is
// respected across restarts. Close only stops the core and preserves selection.
func (m *Manager) Disconnect() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.store.SetAutoConnect(false); err != nil { return fmt.Errorf("persist automatic disconnect: %w", err) }
	if err := m.store.SetLastConnection(nil); err != nil {
		return fmt.Errorf("persist disconnect: %w", err)
	}
	m.phase="disabled"
 m.appendEventLocked("connection","info","Автоматическое подключение отключено пользователем")
 return m.stopLocked()
}
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopLocked()
}

// Restore reconnects the last explicitly selected server on startup. A stable
// hash of its original share link prevents accidentally selecting a different
// server if a subscription was reordered or updated.
func (m *Manager) Restore() error {
	m.mu.Lock()
	preference := m.store.LastConnection()
	if !m.store.AutoConnect() || preference == nil {
		m.mu.Unlock()
		return nil
	}
	sub, ok := m.store.Find(preference.Subscription)
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("restore: subscription %q no longer exists", preference.Subscription)
	}
	index := 0
	for i, server := range sub.Servers() {
		if store.HashLink(server.Raw) == preference.LinkHash {
			index = i + 1
			break
		}
	}
	m.mu.Unlock()
	if index == 0 {
		return fmt.Errorf("restore: previously selected server is missing from subscription %q; select another server manually", preference.Subscription)
	}
	return m.Connect(preference.Subscription, index)
}
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := Status{AutoConnect:m.store.AutoConnect(),Failures:m.failures,Phase:m.phase,Checked:m.checkedCandidates,Total:m.totalCandidates,Running:m.instance!=nil,Health:m.health,ProxySOCKS:m.opts.ProxyListen+":10808",ProxyHTTP:m.opts.ProxyListen+":10809"}
 if !m.autoRetryAt.IsZero() {retry:=m.autoRetryAt;result.RetryAt=&retry}
 if !m.nextRefreshAt.IsZero() {retry:=m.nextRefreshAt;result.RefreshRetryAt=&retry}
	if m.current != nil {
		cpy := *m.current
		result.Connection = &cpy
	}
	return result
}

// Monitor checks actual proxied egress, and recovers automatically after outages.
// No busy-loop: the background loop retries with a bounded interval.
func (m *Manager) Monitor(ctx context.Context) {
 ticker := time.NewTicker(30 * time.Second)
 defer ticker.Stop()
 refreshTicker:=time.NewTicker(6 * time.Hour)
 defer refreshTicker.Stop()
 m.monitorStep(ctx)
 for {
  select {
  case <-ctx.Done(): return
  case <-ticker.C: m.monitorStep(ctx)
  case <-refreshTicker.C: m.refreshSubscriptions(ctx)
  }
 }
}

func (m *Manager) monitorStep(ctx context.Context) {
 // checkOnce stores a result before the recovery decision is made.
 m.checkOnce()
 m.mu.Lock()
 enabled:=m.store.AutoConnect()
 running:=m.instance!=nil
 healthy:=m.health.Healthy
 checked:=m.health.CheckedAt!=nil
 if running && checked && !healthy {m.failures++} else if healthy {m.failures=0}
 failures:=m.failures
 retryAt:=m.autoRetryAt
 needsRefresh:=m.refreshPending && !time.Now().Before(m.nextRefreshAt)
 refreshName:=m.store.Active()
 m.mu.Unlock()
 if needsRefresh && refreshName!=""{m.refreshAfterFailure(ctx,refreshName)}
 if !enabled || (running && (healthy || failures<3)) || time.Now().Before(retryAt){return}
 if err:=m.recover(ctx);err!=nil{
  m.mu.Lock()
  // Do not reschedule an automatic reconnect after the user disabled it.
  if !m.store.AutoConnect(){m.mu.Unlock();return}
  m.retryFailures++
  delay:=retryDelay(m.retryFailures,maxRecoveryBackoff)
  m.autoRetryAt=time.Now().Add(delay)
  m.phase="retrying"
  m.appendEventLocked("recovery","error",fmt.Sprintf("Нет рабочего сервера; повтор через %s",delay.Round(time.Second)))
  m.mu.Unlock()
  log.Printf("Automatic recovery failed: %v",err)
 }
}

func (m *Manager) checkOnce() {
 m.mu.Lock()
 active:=m.instance!=nil
 generation:=m.generation
 var hash string
 if active && m.current!=nil{
  if sub,ok:=m.store.Find(m.current.Subscription);ok{
   servers:=sub.Servers()
   if m.current.Index>0 && m.current.Index<=len(servers){
    hash=store.HashLink(servers[m.current.Index-1].Raw)
   }
  }
 }
 m.mu.Unlock()
 if !active{return}
 start:=time.Now()
 ip,err:=check.ExternalIP("127.0.0.1:10808",8*time.Second)
 now:=time.Now()
 m.mu.Lock()
 if m.instance==nil || generation!=m.generation{m.mu.Unlock();return}
 previous:=m.health.Healthy
 hadPrevious:=m.health.CheckedAt!=nil
 m.health=Health{CheckedAt:&now,Healthy:err==nil}
 if err==nil {
  m.health.ExitIP=ip
  m.phase="connected"
  if hadPrevious && !previous {m.appendEventLocked("health","success","Соединение снова доступно")}
 }else{
  m.health.Error=err.Error()
  if previous{m.phase="degraded";m.appendEventLocked("health","warning",fmt.Sprintf("Проверка соединения не пройдена: %v",err))}
 }
 record:=err==nil && hash!="" && (!previous || now.Sub(m.history[hash].LastSuccess)>time.Hour)
 m.mu.Unlock()
 if record{m.recordProbe(hash,time.Since(start),true)}
}

func (m *Manager) refreshSubscriptions(ctx context.Context) {
 m.mu.Lock()
 names:=make([]string,0)
 for _,sub:=range m.store.Subscriptions(){names=append(names,sub.Name)}
 m.mu.Unlock()
 for _,name:=range names{
  if ctx.Err()!=nil{return}
  m.event("refresh","info",fmt.Sprintf("Плановое обновление подписки %s",name))
  err:=m.Update(ctx,name,false)
  if err!=nil{
   m.mu.Lock()
   if name==m.store.Active(){
    m.refreshFailures++
    delay:=retryDelay(m.refreshFailures,maxRefreshBackoff)
    m.nextRefreshAt=time.Now().Add(delay)
    m.refreshPending=true
    m.appendEventLocked("refresh","warning",fmt.Sprintf("Плановое обновление %s не удалось; повтор через %s",name,delay.Round(time.Second)))
   }
   m.mu.Unlock()
   log.Printf("Scheduled subscription refresh failed for %s: %v",name,err)
  }else{
   m.mu.Lock()
   if name==m.store.Active(){m.refreshFailures=0;m.refreshPending=false;m.nextRefreshAt=time.Now().Add(refreshCooldown)}
   m.mu.Unlock()
   m.event("refresh","success",fmt.Sprintf("Подписка %s обновлена",name))
  }
 }
}
