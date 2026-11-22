package store

import "testing"

func TestAutoConnectPreference(t *testing.T) {
 s,err:=Open(t.TempDir())
 if err!=nil{t.Fatal(err)}
 if s.AutoConnect(){t.Fatal("empty installation must not auto connect")}
 if err:=s.SetAutoConnect(true);err!=nil{t.Fatal(err)}
 if !s.AutoConnect(){t.Fatal("enabled auto mode not saved")}
 reopened,err:=Open(s.dir)
 if err!=nil{t.Fatal(err)}
 if !reopened.AutoConnect(){t.Fatal("auto mode not restored")}
 if err:=reopened.SetAutoConnect(false);err!=nil{t.Fatal(err)}
 next,err:=Open(s.dir)
 if err!=nil{t.Fatal(err)}
 if next.AutoConnect(){t.Fatal("explicit disconnect not preserved")}
}
