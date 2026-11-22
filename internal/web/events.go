package web

import (
 "encoding/json"
 "log"
 "os"
 "path/filepath"
 "time"
)

const maxGatewayEvents=120

type GatewayEvent struct {
 Time time.Time `json:"time"`
 Kind string `json:"kind"`
 Level string `json:"level"`
 Message string `json:"message"`
}

func (m *Manager) eventsPath() string {return filepath.Join(m.opts.Home,"gateway-events.json")}

// loadEvents only runs during manager construction.
func (m *Manager) loadEvents() {
 var stored []GatewayEvent
 data,err:=os.ReadFile(m.eventsPath())
 if err==nil {
  if e:=json.Unmarshal(data,&stored);e!=nil{log.Printf("Invalid event history ignored: %v",e)}
 }
 if len(stored)>maxGatewayEvents{stored=stored[len(stored)-maxGatewayEvents:]}
 m.events=stored
}

func (m *Manager) appendEventLocked(kind,level,message string) {
 // Called while manager state is locked; never store share links, request
 // URLs, passwords or Xray configuration in the event history.
 e:=GatewayEvent{Time:time.Now(),Kind:kind,Level:level,Message:message}
 m.events=append(m.events,e)
 if len(m.events)>maxGatewayEvents{m.events=m.events[len(m.events)-maxGatewayEvents:]}
 data,err:=json.Marshal(m.events)
 if err==nil{
  path:=m.eventsPath()
  if err=os.MkdirAll(m.opts.Home,0700);err==nil{
   if err=os.WriteFile(path+".tmp",data,0600);err==nil {_=os.Rename(path+".tmp",path)}
  }
 }
 log.Printf("Gateway event %s/%s: %s",kind,level,message)
}

func (m *Manager) event(kind,level,message string) {
 m.mu.Lock()
 defer m.mu.Unlock()
 m.appendEventLocked(kind,level,message)
}

func (m *Manager) Events() []GatewayEvent {
 m.mu.Lock()
 defer m.mu.Unlock()
 out:=make([]GatewayEvent,len(m.events))
 // Newest-first, useful for the web timeline.
 for i,e:=range m.events{out[len(out)-1-i]=e}
 return out
}
