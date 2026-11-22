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
 "sync"
 "time"

 "github.com/Ozzmosis94/happ-gateway/internal/check"
 "github.com/Ozzmosis94/happ-gateway/internal/link"
 "github.com/Ozzmosis94/happ-gateway/internal/store"
 "github.com/Ozzmosis94/happ-gateway/internal/xray"
)

const (
 quickCandidateCount = 4
 concurrentProbes = 8
 probeTimeout = 5 * time.Second
 refreshCooldown = 5 * time.Minute
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
  if r.err!=nil{continue}
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
 log.Printf("Automatic failover selected %s server %d (verified %dms)",name,index,p.latency.Milliseconds())
 return nil
}

func (m *Manager) refreshAfterFailure(ctx context.Context,name string) {
 m.mu.Lock()
 if time.Since(m.lastFailureRefresh)<refreshCooldown{
  m.mu.Unlock();return
 }
 m.lastFailureRefresh=time.Now()
 m.mu.Unlock()
 log.Printf("No quick candidate online; refreshing subscription %s",name)
 if err:=m.Update(ctx,name,false);err!=nil{
  log.Printf("On-failure subscription refresh failed; using cached nodes: %v",err)
 }else{log.Printf("On-failure subscription refresh succeeded: %s",name)}
}

// Recover starts with four preferred/known-good nodes, refreshes the subscription
// after a failed quick pass, then checks all remaining nodes in groups of eight.
func (m *Manager) recover(ctx context.Context) error {
 m.recoveryMu.Lock()
 defer m.recoveryMu.Unlock()
 m.mu.Lock()
 if !m.store.AutoConnect(){m.mu.Unlock();return nil}
 generation:=m.generation
 name:=m.store.Active()
 if p:=m.store.LastConnection();p!=nil {name=p.Subscription}
 m.mu.Unlock()
 candidates,err:=m.rankedCandidates(name)
 if err!=nil{return err}
 if len(candidates)==0{return errors.New("no supported servers in active subscription")}
 quickCount:=quickCandidateCount
 if quickCount>len(candidates){quickCount=len(candidates)}
 quick:=candidates[:quickCount]
 quickCtx,cancel:=context.WithTimeout(ctx,probeTimeout+time.Second)
 best,ok:=m.probeBatch(quickCtx,quick)
 cancel()
 if ok{return m.commitCandidate(generation,name,best)}
 m.refreshAfterFailure(ctx,name)
 candidates,err=m.rankedCandidates(name)
 if err!=nil{return err}
 attempted:=make(map[string]bool,len(quick))
 for _,c:=range quick{attempted[c.hash]=true}
 rest:=make([]nodeCandidate,0,len(candidates))
 for _,c:=range candidates{if !attempted[c.hash]{rest=append(rest,c)}}
 scanCtx,scanCancel:=context.WithTimeout(ctx,time.Duration((len(rest)+concurrentProbes-1)/concurrentProbes)*(probeTimeout+time.Second))
 defer scanCancel()
 best,ok=m.probeBatch(scanCtx,rest)
 if ok{return m.commitCandidate(generation,name,best)}
 return fmt.Errorf("no usable servers after checking %d candidates of subscription %q",len(quick)+len(rest),name)
}
