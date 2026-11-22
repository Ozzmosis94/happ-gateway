package web

import (
 "fmt"
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
