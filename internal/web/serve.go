package web

import (
 "context"
 "errors"
 "fmt"
 "log"
 "net/http"
 "os"
 "strings"
 "time"
)

// Serve starts the authenticated dashboard and embedded proxy manager.
func Serve(ctx context.Context) error {
 token:=os.Getenv("WEB_TOKEN")
 if len(token)<24 { return errors.New("WEB_TOKEN must contain at least 24 characters") }
 home:=os.Getenv("DATA_DIR"); if home=="" {home="/data"}
 addr:=os.Getenv("WEB_ADDR"); if addr=="" {addr="127.0.0.1:8088"}
 listen:=os.Getenv("PROXY_LISTEN"); if listen=="" {listen="127.0.0.1"}
 manager,err:=NewManager(Options{Home:home,ProxyListen:listen,
   AllowLAN:strings.EqualFold(os.Getenv("PROXY_ALLOW_LAN"),"true"),
   AllowedCIDRs:splitCIDRs(os.Getenv("PROXY_ALLOW_CIDRS"))})
 if err!=nil {return err}
 defer manager.Close()
 go manager.Monitor(ctx)
 server:=&http.Server{Addr:addr,Handler:NewHandler(manager,token),
   ReadHeaderTimeout:5*time.Second,ReadTimeout:15*time.Second,
   WriteTimeout:35*time.Second,IdleTimeout:60*time.Second}
 go func(){
  <-ctx.Done()
  shutdownCtx,cancel:=context.WithTimeout(context.Background(),10*time.Second)
  defer cancel()
  if err:=server.Shutdown(shutdownCtx);err!=nil{log.Printf("server shutdown: %v",err)}
 }()
 log.Printf("Happ Gateway dashboard listening on %s",addr)
 err=server.ListenAndServe()
 if err!=nil&&!errors.Is(err,http.ErrServerClosed){return fmt.Errorf("serve Happ Gateway: %w",err)}
 return nil
}

func splitCIDRs(value string) []string {
 if strings.TrimSpace(value)==""{return nil}
 var out []string
 for _,part:=range strings.Split(value,","){if value:=strings.TrimSpace(part);value!=""{out=append(out,value)}}
 return out
}
