package web

import (
 "fmt"
 "encoding/json"
 "context"
 "os"
 "path/filepath"
 "testing"
 "time"

 "github.com/Ozzmosis94/happ-gateway/internal/store"
)

func TestRankedCandidatesPreferPreviousAndHealthy(t *testing.T) {
 manager,err:=NewManager(Options{Home:t.TempDir()})
 if err!=nil{t.Fatal(err)}
 links:=make([]string,6)
 for i:=range links{links[i]=fmt.Sprintf("trojan://secret@node-%d.example.com:443#node%d",i,i)}
 if err:=manager.store.Upsert(store.SubEntry{Name:"main",URL:"https://example.org/sub",Links:links});err!=nil{t.Fatal(err)}
 manager.history[store.HashLink(links[4])]=nodeHistory{LastSuccess:time.Now(),LatencyMS:50}
 manager.history[store.HashLink(links[3])]=nodeHistory{LastSuccess:time.Now().Add(-time.Hour),LatencyMS:80}
 if err:=manager.store.SetLastConnection(&store.ConnectionPreference{Subscription:"main",LinkHash:store.HashLink(links[1])});err!=nil{t.Fatal(err)}
 got,err:=manager.rankedCandidates("main")
 if err!=nil{t.Fatal(err)}
 if len(got)!=len(links){t.Fatalf("got %d candidates, want %d",len(got),len(links))}
 if got[0].index!=2{t.Errorf("preferred server at %d, want 2",got[0].index)}
 if got[1].index!=5{t.Errorf("known healthy node at %d, want 5",got[1].index)}
 if got[2].index!=4{t.Errorf("next healthy node at %d, want 4",got[2].index)}
}

func TestRecoverWithoutSubscriptionsFails(t *testing.T) {
 manager,err:=NewManager(Options{Home:t.TempDir()})
 if err!=nil{t.Fatal(err)}
 got,err:=manager.rankedCandidates("missing")
 if err==nil||got!=nil{t.Fatalf("missing subscription should fail: %v %v",got,err)}
}

func TestRecoveryExponentialBackoff(t *testing.T) {
 previous:=time.Duration(0)
 for n:=1;n<=20;n++{
  current:=retryDelay(n,maxRecoveryBackoff)
  if current<previous {t.Fatalf("retry %d went backwards: %v < %v",n,current,previous)}
  if current>maxRecoveryBackoff{t.Fatalf("retry exceeds cap: %v",current)}
  previous=current
 }
 if retryDelay(100,maxRefreshBackoff)>maxRefreshBackoff{t.Fatal("refresh delay exceeds cap")}
}

func TestEventsAreBoundedAndPersisted(t *testing.T) {
 dir:=t.TempDir()
 manager,err:=NewManager(Options{Home:dir})
 if err!=nil{t.Fatal(err)}
 for i:=0;i<maxGatewayEvents+10;i++{manager.event("test","info",fmt.Sprintf("step %d",i))}
 events:=manager.Events()
 if len(events)!=maxGatewayEvents{t.Fatalf("got %d events, expected %d",len(events),maxGatewayEvents)}
 if events[0].Message!="step 129"{t.Fatalf("latest event=%q",events[0].Message)}
 data,err:=os.ReadFile(filepath.Join(dir,"gateway-events.json"))
 if err!=nil{t.Fatal(err)}
 var fileEvents []GatewayEvent
 if err:=json.Unmarshal(data,&fileEvents);err!=nil{t.Fatal(err)}
 if len(fileEvents)!=maxGatewayEvents{t.Fatalf("history persisted %d events",len(fileEvents))}
 restarted,err:=NewManager(Options{Home:dir})
 if err!=nil{t.Fatal(err)}
 if len(restarted.Events())!=maxGatewayEvents{t.Fatal("history was lost on restart")}
}

func TestScanCandidatesStopsOnFirstHealthyBatch(t *testing.T) {
 candidates:=make([]nodeCandidate,8)
 for i:=range candidates {candidates[i].index=i+1}
 calls:=0
 run:=func(ctx context.Context,group []nodeCandidate)(probeResult,bool){
  calls++
  if len(group)>concurrentProbes {t.Fatalf("batch size=%d",len(group))}
  if calls==2{return probeResult{candidate:group[0]},true}
  return probeResult{},false
 }
 result,ok:=scanCandidates(context.Background(),candidates,run)
 if !ok || result.candidate.index!=4 {t.Fatalf("result=%v ok=%v",result.candidate.index,ok)}
 if calls!=2{t.Fatalf("ran %d batches; expected 2",calls)}
}

func TestScanCandidatesHonorsCancellation(t *testing.T) {
 ctx,cancel:=context.WithCancel(context.Background())
 cancel()
 calls:=0
 _,ok:=scanCandidates(ctx,[]nodeCandidate{{index:1}},func(context.Context,[]nodeCandidate)(probeResult,bool){
  calls++
  return probeResult{},false
 })
 if ok || calls!=0{t.Fatalf("cancelled scan probed %d times",calls)}
}

func TestReconcileIndexAfterSubscriptionRefresh(t *testing.T) {
 manager,err:=NewManager(Options{Home:t.TempDir()})
 if err!=nil{t.Fatal(err)}
 old:=[]string{
  "trojan://token@first.example.com:443#first",
  "trojan://token@second.example.com:443#second",
 }
 if err:=manager.store.Upsert(store.SubEntry{Name:"main",URL:"https://example.org/sub",Links:old});err!=nil{t.Fatal(err)}
 if err:=manager.store.SetLastConnection(&store.ConnectionPreference{Subscription:"main",LinkHash:store.HashLink(old[1])});err!=nil{t.Fatal(err)}
 manager.current=&Connection{Subscription:"main",Index:2,Tag:"second"}
 reordered:=[]string{old[1],old[0]}
 if err:=manager.store.Upsert(store.SubEntry{Name:"main",URL:"https://example.org/sub",Links:reordered});err!=nil{t.Fatal(err)}
 manager.reconcileCurrentIndexLocked("main")
 if manager.current.Index!=1{t.Fatalf("reordered index=%d; want 1",manager.current.Index)}
 if err:=manager.store.Upsert(store.SubEntry{Name:"main",URL:"https://example.org/sub",Links:[]string{old[0]}});err!=nil{t.Fatal(err)}
 manager.reconcileCurrentIndexLocked("main")
 if manager.current.Index!=0{t.Fatalf("removed node index=%d; want 0",manager.current.Index)}
}
