package web

import (
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "log"
 "net"
 "os"
 "path/filepath"
 "sort"
 "math/rand"
 "sync"
 "time"

 "github.com/Ozzmosis94/happ-gateway/internal/check"
 "github.com/Ozzmosis94/happ-gateway/internal/link"
 "github.com/Ozzmosis94/happ-gateway/internal/store"
 "github.com/Ozzmosis94/happ-gateway/internal/xray"
)

const (
 concurrentProbes = 8
 probeTimeout = 5 * time.Second
 refreshCooldown = 5 * time.Minute
 maxRecoveryBackoff = 10 * time.Minute
 maxRefreshBackoff = 15 * time.Minute
)

type nodeHistory struct {
 LastSuccess time.Time `json:"lastSuccess"`
 LatencyMS int64 `json:"latencyMs"`
 Failures int `json:"failures"`
}

type nodeCandidate struct {
 index int
 hash string
 server *link.Server
 score nodeHistory
}

type probeResult struct {
 candidate nodeCandidate
 latency time.Duration
 ip string
 err error
}

func (m *Manager) historyPath() string {return filepath.Join(m.opts.Home,"node-history.json")}

func (m *Manager) loadHistory() {
 m.history = make(map[string]nodeHistory)
 data,err:=os.ReadFile(m.historyPath())
 if err==nil {
  if e:=json.Unmarshal(data,&m.history);e!=nil {
   log.Printf("Ignoring damaged node history: %v",e)
   m.history=make(map[string]nodeHistory)
  }
 }else if !errors.Is(err,os.ErrNotExist){log.Printf("Cannot read node history: %v",err)}
}

func (m *Manager) recordProbe(hash string,latency time.Duration, success bool) {
 m.mu.Lock()
 if m.history==nil {m.history=make(map[string]nodeHistory)}
 v:=m.history[hash]
 if success {v.LastSuccess=time.Now();v.LatencyMS=latency.Milliseconds();v.Failures=0}else{v.Failures++}
 m.history[hash]=v
 data,err:=json.MarshalIndent(m.history,"","  ")
 path:=m.historyPath()
 if err==nil {
  // Serialize snapshots with the manager lock to avoid stale overwrites.
  if err=os.MkdirAll(m.opts.Home,0700);err==nil {
   if err=os.WriteFile(path+".tmp",data,0600);err==nil {_=os.Rename(path+".tmp",path)}
  }
 }
 m.mu.Unlock()
}

func (m *Manager) rankedCandidates(name string) ([]nodeCandidate,error) {
 m.mu.Lock()
 defer m.mu.Unlock()
 sub,ok:=m.store.Find(name)
 if !ok{return nil,fmt.Errorf("subscription %q not found",name)}
 preferred:=""
 if p:=m.store.LastConnection();p!=nil && p.Subscription==name{preferred=p.LinkHash}
 servers:=sub.Servers()
 out:=make([]nodeCandidate,0,len(servers))
 for i,s:=range servers{
  if !xray.Supported(s.Protocol){continue}
  hash:=store.HashLink(s.Raw)
  out=append(out,nodeCandidate{index:i+1,hash:hash,server:s,score:m.history[hash]})
 }
 sort.SliceStable(out,func(i,j int)bool{
  a,b:=out[i],out[j]
  if a.hash==preferred{return true}
  if b.hash==preferred{return false}
  if !a.score.LastSuccess.Equal(b.score.LastSuccess){return a.score.LastSuccess.After(b.score.LastSuccess)}
  if a.score.Failures!=b.score.Failures{return a.score.Failures<b.score.Failures}
  if a.score.LatencyMS>0 && b.score.LatencyMS>0{return a.score.LatencyMS<b.score.LatencyMS}
  return a.index<b.index
 })
 return out,nil
}

// Each test starts its own Xray instance on a temporary localhost-only SOCKS
// listener. The active proxy remains untouched until a candidate has passed.
func probeNode(ctx context.Context,c nodeCandidate) probeResult {
 start:=time.Now()
 r:=probeResult{candidate:c}
 if err:=ctx.Err();err!=nil {r.err=err;return r}
 listener,err:=net.Listen("tcp","127.0.0.1:0")
 if err!=nil{r.err=err;return r}
 port:=listener.Addr().(*net.TCPAddr).Port
 _=listener.Close()
 cfg,err:=xray.BuildConfig(c.server,xray.Options{SocksPort:port,HTTPPort:0,Listen:"127.0.0.1",DNSProxy:true,LogLevel:"error"})
 if err!=nil{r.err=err;return r}
 raw,err:=cfg.JSON()
 if err!=nil{r.err=err;return r}
 inst,err:=xray.Start(raw)
 if err!=nil{r.err=err;return r}
 defer inst.Close()
 // ExternalIP uses two independent endpoints as fallbacks, with one shared
 // time budget enforced by the check helper.
 remain:=probeTimeout
 if deadline,ok:=ctx.Deadline();ok{
  if d:=time.Until(deadline);d<remain{remain=d}
 }
 if remain<=0{r.err=context.DeadlineExceeded;return r}
 r.ip,r.err=check.ExternalIP(net.JoinHostPort("127.0.0.1",fmt.Sprint(port)),remain)
 r.latency=time.Since(start)
 return r
}

// probeBatch bounds parallel instances and waits until every candidate has
// finished; it selects the fastest confirmed egress rather than first startup.
func (m *Manager) probeBatch(ctx context.Context,items []nodeCandidate) (probeResult,bool) {
 if len(items)==0{return probeResult{},false}
 jobs:=make(chan nodeCandidate)
 results:=make(chan probeResult,len(items))
 var wg sync.WaitGroup
 count:=concurrentProbes
 if len(items)<count{count=len(items)}
 for i:=0;i<count;i++ {
  wg.Add(1)
  go func(){
   defer wg.Done()
   for c:=range jobs{
    if ctx.Err()!=nil{return}
    results<-probeNode(ctx,c)
   }
  }()
 }
 go func(){
  defer close(jobs)
  for _,c:=range items{
   select {case <-ctx.Done():return;case jobs<-c:}
  }
 }()
 go func(){wg.Wait();close(results)}()
 best:=probeResult{}
 ok:=false
 for r:=range results{
  m.recordProbe(r.candidate.hash,r.latency,r.err==nil)
  m.mu.Lock();m.checkedCandidates++;m.mu.Unlock()
  if r.err!=nil{
   m.event("probe","warning",fmt.Sprintf("Сервер #%d не прошёл проверку",r.candidate.index))
   continue
  }
  m.event("probe","success",fmt.Sprintf("Сервер #%d доступен, задержка %d мс",r.candidate.index,r.latency.Milliseconds()))
  if !ok || r.latency<best.latency{best=r;ok=true}
 }
 return best,ok
}

func (m *Manager) commitCandidate(expectedGeneration uint64,name string,p probeResult) error {
 m.mu.Lock()
 defer m.mu.Unlock()
 if !m.store.AutoConnect() || m.generation!=expectedGeneration {
  return errors.New("connection changed while probing; discard automatic selection")
 }
 // Updating the subscription may reorder indexes; bind candidate to raw hash.
 sub,ok:=m.store.Find(name)
 if !ok{return errors.New("subscription was removed while probing")}
 index:=0
 for i,s:=range sub.Servers(){if store.HashLink(s.Raw)==p.candidate.hash{index=i+1;break}}
 if index==0{return errors.New("successful candidate disappeared after refresh")}
 if err:=m.connectLocked(name,index);err!=nil{return err}
 now:=time.Now()
 m.health=Health{CheckedAt:&now,Healthy:true,ExitIP:p.ip}
 m.failures=0
 m.autoRetryAt=time.Time{}
 m.retryFailures=0
 m.phase="connected"
 m.appendEventLocked("connection","success",fmt.Sprintf("Подключён %s / %s, задержка %d мс",name,p.candidate.server.Tag,p.latency.Milliseconds()))
 log.Printf("Automatic failover selected %s server %d (verified %dms)",name,index,p.latency.Milliseconds())
 return nil
}

// retryDelay applies exponential backoff with bounded jitter. The two retry
// streams (connection recovery and subscription refresh) have separate counters.
func retryDelay(failures int, cap time.Duration) time.Duration {
 if failures<1 {failures=1}
 base:=15*time.Second
 for n:=1;n<failures && base<cap;n++ {
  if base>cap/2 {base=cap;break}
  base*=2
 }
 if base>cap {base=cap}
 // 0..20% random extra delay; never exceed the configured cap.
 room:=cap-base
 jitter:=time.Duration(rand.Int63n(int64(base/5)+1))
 if jitter>room {jitter=room}
 return base+jitter
}

// refreshAfterFailure has its own exponential retry schedule; a failed fetch
// never clears the existing server cache or blocks attempts against that cache.
func (m *Manager) refreshAfterFailure(ctx context.Context,name string) bool {
 m.mu.Lock()
 if time.Now().Before(m.nextRefreshAt) {
  until:=m.nextRefreshAt
  m.mu.Unlock()
  m.event("refresh","info",fmt.Sprintf("Подписка: повторное обновление после %s",until.Format("15:04:05")))
  return false
 }
 m.mu.Unlock()
 m.event("refresh","info",fmt.Sprintf("Обновление подписки %s после сбоя",name))
 // Emergency refresh accepts a valid smaller subscription when the provider
 // legitimately rotates its fleet; empty/invalid responses are still rejected.
 err:=m.Update(ctx,name,true)
 m.mu.Lock()
 if err!=nil {
  m.refreshFailures++
  m.refreshPending=true
  delay:=retryDelay(m.refreshFailures,maxRefreshBackoff)
  m.nextRefreshAt=time.Now().Add(delay)
  m.mu.Unlock()
  m.event("refresh","error",fmt.Sprintf("Подписка %s: обновление не удалось, повтор через %s; старый список сохранён",name,delay.Round(time.Second)))
  log.Printf("On-failure subscription refresh failed for %s: %v",name,err)
  return false
 }
 m.refreshFailures=0
 m.refreshPending=false
 m.nextRefreshAt=time.Now().Add(refreshCooldown)
 m.mu.Unlock()
 m.event("refresh","success",fmt.Sprintf("Подписка %s обновлена, следующее аварийное обновление не раньше чем через %s",name,refreshCooldown))
 return true
}

// Recover state machine:
// 1. Test last selected server without modifying production traffic.
// 2. On failure try a subscription refresh (independent exponential retry).
// 3. Retry that same server if its original link is still present.
// 4. Check alternatives in bounded parallel batches, selecting verified egress.
// 5. On exhaustion return an error; caller schedules exponential retry.
func (m *Manager) recover(ctx context.Context) error {
 m.recoveryMu.Lock()
 defer m.recoveryMu.Unlock()
 m.mu.Lock()
 if !m.store.AutoConnect(){m.mu.Unlock();return nil}
 generation:=m.generation
 name:=m.store.Active()
 preferred:=""
 if p:=m.store.LastConnection();p!=nil {
  name=p.Subscription
  preferred=p.LinkHash
 }
 m.phase="checking"
 m.mu.Unlock()
 m.event("recovery","info",fmt.Sprintf("Поиск рабочего сервера: подписка %s",name))
 candidates,err:=m.rankedCandidates(name)
 if err!=nil{return err}
 if len(candidates)==0 {
  m.refreshAfterFailure(ctx,name)
  candidates,err=m.rankedCandidates(name)
  if err!=nil{return err}
 }
 m.mu.Lock()
 m.totalCandidates=len(candidates)
 m.checkedCandidates=0
 m.mu.Unlock()
 attempted:=map[string]bool{}
 // First try the exact saved node before changing any shared Xray listeners.
 if preferred!=""{
  for _,candidate:=range candidates {
   if candidate.hash!=preferred{continue}
   attempted[preferred]=true
   m.event("probe","info",fmt.Sprintf("Проверка сохранённого сервера: %s",candidate.server.Tag))
   result:=probeNode(ctx,candidate)
   m.recordProbe(candidate.hash,result.latency,result.err==nil)
   m.mu.Lock();m.checkedCandidates++;m.mu.Unlock()
   if result.err==nil {
    m.event("probe","success","Сохранённый сервер доступен")
    return m.commitCandidate(generation,name,result)
   }
   m.event("probe","warning","Сохранённый сервер недоступен")
   break
  }
 }
 if err:=ctx.Err();err!=nil{return err}
 m.mu.Lock();m.phase="refreshing";m.mu.Unlock()
 refreshed:=m.refreshAfterFailure(ctx,name)
 if refreshed {
  candidates,err=m.rankedCandidates(name)
  if err!=nil{return err}
  m.mu.Lock();m.totalCandidates=len(candidates);m.mu.Unlock()
  if preferred!=""{
   for _,candidate:=range candidates{
    if candidate.hash!=preferred{continue}
    m.event("probe","info",fmt.Sprintf("Повторная проверка после обновления: %s",candidate.server.Tag))
    result:=probeNode(ctx,candidate)
    m.recordProbe(candidate.hash,result.latency,result.err==nil)
    m.mu.Lock();m.checkedCandidates++;m.mu.Unlock()
    if result.err==nil{return m.commitCandidate(generation,name,result)}
    m.event("probe","warning","Повторная проверка сервера не прошла")
    break
   }
  }
 }
 if err:=ctx.Err();err!=nil{return err}
 m.mu.Lock();m.phase="scanning";m.mu.Unlock()
 // Freshly refreshed links are eligible even if they changed since the failed
 // probe. Avoid duplicate failures against exactly the same link.
 rest:=make([]nodeCandidate,0,len(candidates))
 for _,candidate:=range candidates {
  if candidate.hash==preferred{continue}
  if attempted[candidate.hash]{continue}
  rest=append(rest,candidate)
 }
 m.mu.Lock();m.totalCandidates=len(rest)+m.checkedCandidates;m.mu.Unlock()
 m.event("recovery","info",fmt.Sprintf("Проверка альтернатив: %d серверов, параллельно не более %d",len(rest),concurrentProbes))
 if len(rest)==0{return errors.New("нет других серверов для проверки")}
 // Parallel probes use their own ephemeral SOCKS ports. Wait for the bounded
 // scan to finish, then choose the lowest observed successful latency.
 scanCtx,cancel:=context.WithTimeout(ctx,time.Duration((len(rest)+concurrentProbes-1)/concurrentProbes)*(probeTimeout+time.Second))
 defer cancel()
 best,ok:=m.probeBatch(scanCtx,rest)
 if ok {return m.commitCandidate(generation,name,best)}
 return fmt.Errorf("no usable servers found in subscription %q",name)
}
