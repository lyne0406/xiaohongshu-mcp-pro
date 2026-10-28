package main

import (
 "bytes"
 "context"
 "crypto/subtle"
 "embed"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "log"
 "net/http"
 "os"
 "regexp"
 "strings"
 "sync"
 "time"
)

//go:embed index.html
var assets embed.FS
var phoneRE=regexp.MustCompile("^1[3-9][0-9]{9}$")
var codeRE=regexp.MustCompile("^[0-9]{6}$")
var accessKey=os.Getenv("LOGIN_GATE_KEY")
var mcpURL=os.Getenv("PRO_MCP_URL")
var client=&http.Client{Timeout:175*time.Second}
var guard sync.Mutex
var busy bool
var phase="idle"
var notice="尚未开始"
var shot=""

type replyBody struct { OK bool `json:"ok"`; Phase string `json:"phase,omitempty"`; Message string `json:"message,omitempty"`; Image string `json:"image,omitempty"`; Text string `json:"text,omitempty"` }
type rpcReply struct { Error json.RawMessage `json:"error"`; Result json.RawMessage `json:"result"` }
type part struct {Type string `json:"type"`; Text string `json:"text"`; Data string `json:"data"`}
type toolReply struct {IsError bool `json:"isError"`; Content []part `json:"content"`}

func sendJSON(w http.ResponseWriter,status int,data replyBody){
 w.Header().Set("Content-Type","application/json; charset=utf-8")
 w.Header().Set("Cache-Control","no-store")
 w.Header().Set("X-Content-Type-Options","nosniff")
 w.WriteHeader(status)
 _=json.NewEncoder(w).Encode(data)
}
func reject(w http.ResponseWriter,status int,msg string){sendJSON(w,status,replyBody{Message:msg})}
func auth(r *http.Request)bool{
 supplied:=r.Header.Get("X-Lab-Key")
 return len(accessKey)>0&&len(supplied)==len(accessKey)&&subtle.ConstantTimeCompare([]byte(supplied),[]byte(accessKey))==1
}
func input(r *http.Request,field string)(string,error){
 defer r.Body.Close()
 var m map[string]json.RawMessage
 if err:=json.NewDecoder(io.LimitReader(r.Body,1024)).Decode(&m);err!=nil{return "",err}
 var v string
 if err:=json.Unmarshal(m[field],&v);err!=nil{return "",err}
 return strings.TrimSpace(v),nil
}
func rpc(ctx context.Context,id int,method string,params any)(rpcReply,error){
 result:=rpcReply{}
 b,_:=json.Marshal(map[string]any{"jsonrpc":"2.0","id":id,"method":method,"params":params})
 req,err:=http.NewRequestWithContext(ctx,"POST",mcpURL,bytes.NewReader(b))
 if err!=nil{return result,err}
 req.Header.Set("Content-Type","application/json")
 req.Header.Set("Accept","application/json, text/event-stream")
 res,err:=client.Do(req)
 if err!=nil{return result,err}
 defer res.Body.Close()
 if res.StatusCode!=200&&res.StatusCode!=202{return result,fmt.Errorf("upstream HTTP %d",res.StatusCode)}
 data,err:=io.ReadAll(io.LimitReader(res.Body,11<<20))
 if err!=nil{return result,err}
 if strings.Contains(res.Header.Get("Content-Type"),"text/event-stream"){
  for _,line:=range strings.Split(string(data),"\n"){
   if strings.HasPrefix(line,"data:"){data=[]byte(strings.TrimSpace(strings.TrimPrefix(line,"data:")))}
  }
 }
 if err:=json.Unmarshal(data,&result);err!=nil{return result,err}
 if len(result.Error)>0&&string(result.Error)!="null"{return result,errors.New("upstream RPC error")}
 return result,nil
}
func call(ctx context.Context,name string,args map[string]string)(toolReply,error){
 val:=toolReply{}
 hello:=map[string]any{"protocolVersion":"2025-03-26","capabilities":map[string]any{},"clientInfo":map[string]string{"name":"miner-login-ui","version":"0.1"}}
 if _,err:=rpc(ctx,1,"initialize",hello);err!=nil{return val,err}
 r,err:=rpc(ctx,2,"tools/call",map[string]any{"name":name,"arguments":args})
 if err!=nil{return val,err}
 if err:=json.Unmarshal(r.Result,&val);err!=nil{return val,err}
 if val.IsError{return val,errors.New("小红书登录页面返回错误")}
 return val,nil
}
func toReply(res toolReply)replyBody{
 d:=replyBody{OK:true}
 for _,v:=range res.Content{
  if v.Type=="text"{d.Text+=v.Text+"\n"}
  if v.Type=="image"&&len(v.Data)<9<<20{d.Image=v.Data}
 }
 d.Text=strings.TrimSpace(d.Text)
 return d
}
func start(w http.ResponseWriter,r *http.Request){
 if r.Method!="POST"{reject(w,405,"请使用 POST");return}
 phone,err:=input(r,"phone")
 if err!=nil||!phoneRE.MatchString(phone){reject(w,400,"请填写11位中国大陆手机号");return}
 guard.Lock()
 if busy{guard.Unlock();reject(w,409,"正在处理上一项操作");return}
 busy=true;phase="sending";notice="正在请求短信";shot="";guard.Unlock()
 ctx,cancel:=context.WithTimeout(r.Context(),70*time.Second)
 defer cancel()
 data,err:=call(ctx,"creator_phone_login",map[string]string{"phone":phone})
 guard.Lock()
 busy=false
 if err!=nil{phase="failed";notice="短信请求失败";guard.Unlock();reject(w,502,"短信请求未完成，请检查状态后重试");return}
 phase="awaiting-code";notice="已点击发送，等待确认实际短信或页面提示";guard.Unlock()
 out:=toReply(data);out.Phase="awaiting-code"
 out.Message="已点击发送验证码，是否发送成功尚未确认。请先查看截图上的官方提示。"
 if out.Text!=""{out.Message=out.Text}
 sendJSON(w,200,out)
}
func finish(w http.ResponseWriter,r *http.Request){
 if r.Method!="POST"{reject(w,405,"请使用 POST");return}
 code,err:=input(r,"code")
 if err!=nil||!codeRE.MatchString(code){reject(w,400,"请填写6位短信验证码");return}
 guard.Lock()
 if busy||phase!="awaiting-code"{guard.Unlock();reject(w,409,"请先请求短信验证码，不要重复提交");return}
 busy=true;phase="verifying";notice="云端正在确认登录";guard.Unlock()
 go func(){
  ctx,cancel:=context.WithTimeout(context.Background(),165*time.Second)
  defer cancel()
  data,err:=call(ctx,"creator_verify_otp",map[string]string{"otp":code})
  guard.Lock()
  defer guard.Unlock()
  busy=false
  if err!=nil{phase="failed";notice="验证码验证未完成，可能需要重新申请短信或额外安全验证";return}
  phase="completed";notice="登录步骤已返回成功，请继续检查登录状态"
  for _,v:=range data.Content{if v.Type=="image"&&len(v.Data)<9<<20{shot=v.Data}}
 }()
 sendJSON(w,202,replyBody{OK:true,Phase:"verifying",Message:"验证码已提交。请点击查看进度；如果触发设备验证，可能需要约两分钟"})
}
func progress(w http.ResponseWriter,r *http.Request){
 if r.Method!="GET"{reject(w,405,"请使用 GET");return}
 guard.Lock();defer guard.Unlock()
 sendJSON(w,200,replyBody{OK:true,Phase:phase,Message:notice,Image:shot})
}
func status(w http.ResponseWriter,r *http.Request){
 if r.Method!="GET"{reject(w,405,"请使用 GET");return}
 guard.Lock()
 if busy{guard.Unlock();reject(w,409,"登录操作进行中，请稍后检查");return}
 guard.Unlock()
 ctx,cancel:=context.WithTimeout(r.Context(),45*time.Second);defer cancel()
 data,err:=call(ctx,"check_login_status",map[string]string{})
 if err!=nil{reject(w,502,"登录状态检查暂不可用");return}
 out:=toReply(data);out.Image=""
 sendJSON(w,200,out)
}
func main(){
 if accessKey==""||mcpURL==""{log.Fatal("LOGIN_GATE_KEY and PRO_MCP_URL are required")}
 mux:=http.NewServeMux()
 mux.HandleFunc("/healthz",func(w http.ResponseWriter,r *http.Request){
  if r.Method!="GET"{w.WriteHeader(405);return}
  sendJSON(w,200,replyBody{OK:true,Message:"healthy"})
 })
 mux.HandleFunc("/",func(w http.ResponseWriter,r *http.Request){
  if r.URL.Path!="/"||r.Method!="GET"{http.NotFound(w,r);return}
  w.Header().Set("Content-Type","text/html; charset=utf-8")
  w.Header().Set("Cache-Control","no-store")
  w.Header().Set("Content-Security-Policy","default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
  body,_:=assets.ReadFile("index.html");_,_=w.Write(body)
 })
 withAuth:=func(next http.HandlerFunc)http.HandlerFunc{return func(w http.ResponseWriter,r *http.Request){
  if !auth(r){reject(w,401,"实验室访问口令不正确");return}
  next(w,r)
 }}
 mux.HandleFunc("/api/send",withAuth(start))
 mux.HandleFunc("/api/verify",withAuth(finish))
 mux.HandleFunc("/api/progress",withAuth(progress))
 mux.HandleFunc("/api/status",withAuth(status))
 port:=os.Getenv("PORT");if port==""{port="3000"}
 log.Print("miner-login gateway listening; MCP private")
 log.Fatal((&http.Server{Addr:":"+port,Handler:mux,ReadHeaderTimeout:10*time.Second,IdleTimeout:60*time.Second}).ListenAndServe())
}
