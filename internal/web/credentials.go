package web

import (
 "crypto/rand"
 "crypto/subtle"
 "encoding/base64"
 "encoding/json"
 "errors"
 "fmt"
 "os"
 "path/filepath"
 "strings"
 "sync"
 "time"

 "golang.org/x/crypto/argon2"
)

type credentials struct {
 Username string `json:"username"`
 Salt string `json:"salt"`
 Hash string `json:"hash"`
}
type credentialStore struct {
 mu sync.Mutex
 path string
 value *credentials
}
func newCredentialStore(home string) (*credentialStore,error) {
 c:=&credentialStore{path:filepath.Join(home,"admin.json")}
 data,err:=os.ReadFile(c.path)
 if errors.Is(err,os.ErrNotExist){return c,nil}
 if err!=nil{return nil,err}
 var value credentials
 if err:=json.Unmarshal(data,&value);err!=nil{return nil,err}
 if value.Username==""||value.Salt==""||value.Hash==""{return nil,errors.New("invalid administrator credentials")}
 c.value=&value
 return c,nil
}
func (c *credentialStore) configured() bool {
 c.mu.Lock();defer c.mu.Unlock();return c.value!=nil
}
func (c *credentialStore) verify(username,password string) bool {
 c.mu.Lock();v:=c.value;c.mu.Unlock()
 if v==nil {return false}
 salt,err1:=base64.StdEncoding.DecodeString(v.Salt)
 expected,err2:=base64.StdEncoding.DecodeString(v.Hash)
 if err1!=nil||err2!=nil||len(expected)!=32{return false}
 actual:=argon2.IDKey([]byte(password),salt,3,64*1024,4,32)
 return subtle.ConstantTimeCompare([]byte(username),[]byte(v.Username))==1 && subtle.ConstantTimeCompare(actual,expected)==1
}
func (c *credentialStore) setup(username,password string) error {
 username=strings.TrimSpace(username)
 if len(username)<3||len(username)>64||strings.ContainsAny(username,"\r\n\x00"){return errors.New("username must be 3–64 characters")}
 salt:=make([]byte,16)
 if _,err:=rand.Read(salt);err!=nil{return err}
 hash:=argon2.IDKey([]byte(password),salt,3,64*1024,4,32)
 value:=credentials{Username:username,Salt:base64.StdEncoding.EncodeToString(salt),Hash:base64.StdEncoding.EncodeToString(hash)}
 data,err:=json.Marshal(value);if err!=nil{return err}
 c.mu.Lock();defer c.mu.Unlock()
 if c.value!=nil{return errors.New("administrator already configured")}
 file,err:=os.OpenFile(c.path,os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600)
 if err!=nil{return fmt.Errorf("create admin file: %w",err)}
 if _,err=file.Write(data);err!=nil {file.Close();os.Remove(c.path);return err}
 if err=file.Close();err!=nil{return err}
 c.value=&value
 return nil
}
type loginThrottle struct {mu sync.Mutex; failures map[string]int; until map[string]time.Time}
func newLoginThrottle()*loginThrottle{return &loginThrottle{failures:map[string]int{},until:map[string]time.Time{}}}
func (l *loginThrottle) allowed(key string)bool{l.mu.Lock();defer l.mu.Unlock();return !time.Now().Before(l.until[key])}
func (l *loginThrottle) result(key string,success bool){
 l.mu.Lock();defer l.mu.Unlock()
 if success{delete(l.failures,key);delete(l.until,key);return}
 l.failures[key]++
 if l.failures[key]>=5 {l.until[key]=time.Now().Add(time.Minute);l.failures[key]=0}
}
