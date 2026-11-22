package web

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
 "path/filepath"
 "crypto/rand"
 "encoding/hex"
	"strings"
	"time"
)

// Serve starts the authenticated dashboard and embedded proxy manager.
func Serve(ctx context.Context) error {
	home := os.Getenv("DATA_DIR")
	if home == "" {
		home = "/data"
	}
 // Old installations must prove possession of the legacy token before claiming
 // their first password account. Fresh installations need no token.
 _, existingTokenErr:=os.Stat(filepath.Join(home,"web-token"))
 migrationRequired:=existingTokenErr==nil || os.Getenv("WEB_TOKEN")!=""
 token:=""
 if migrationRequired {
  var err error
  token,_,err=loadWebToken(home)
  if err!=nil{return err}
 }
 if err:=os.MkdirAll(home,0700);err!=nil{return err}
 creds,err:=newCredentialStore(home)
 if err!=nil{return err}
 // Stable, random session secret; never emit it in logs.
 sessionPath:=filepath.Join(home,"session-key")
 keyData,err:=os.ReadFile(sessionPath)
 if os.IsNotExist(err){
  buf:=make([]byte,32);if _,err=rand.Read(buf);err!=nil{return err}
  candidate:=[]byte(hex.EncodeToString(buf))
  if err=os.WriteFile(sessionPath,candidate,0600);err!=nil{return err}
  keyData=candidate
 }else if err!=nil{return err}
 sessionKey:=string(keyData)
 if len(sessionKey)<32{return fmt.Errorf("invalid session secret")}
	addr := os.Getenv("WEB_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8088"
	}
	listen := os.Getenv("PROXY_LISTEN")
	if listen == "" {
		listen = "127.0.0.1"
	}
	manager, err := NewManager(Options{
		Home:         home,
		ProxyListen:  listen,
		AllowLAN:     strings.EqualFold(os.Getenv("PROXY_ALLOW_LAN"), "true"),
		AllowedCIDRs: splitCIDRs(os.Getenv("PROXY_ALLOW_CIDRS")),
	})
	if err != nil {
		return err
	}
	defer manager.Close()
	if err := manager.Restore(); err != nil {
		log.Printf("Automatic connection restore failed: %v", err)
	} else if current := manager.Status().Connection; current != nil {
		log.Printf("Automatically reconnected to %s (subscription %s)", current.Tag, current.Subscription)
	}
	go manager.Monitor(ctx)

	server := &http.Server{
		Addr:              addr,
		Handler:           NewHandler(manager, token, creds, sessionKey, migrationRequired),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      35 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("server shutdown: %v", err)
		}
	}()
	log.Printf("Happ Gateway dashboard listening on %s", addr)
	err = server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve Happ Gateway: %w", err)
	}
	return nil
}

func splitCIDRs(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
