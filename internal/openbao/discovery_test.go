package openbao

import (
 "context"
 "encoding/json"
 "net/http"
 "reflect"
 "strings"
 "testing"
)

func TestDiscoverProjectKVKeysAndFields(t *testing.T) {
 clientSocket:=socketServer(t, func(w http.ResponseWriter, r *http.Request) {
   if r.Header.Get("X-Vault-Token")!="" {t.Error("client must not send OpenBao credentials")}
   switch r.URL.Path {
   case "/v1/kv/metadata/projects/myapp":
     if r.Method!=http.MethodGet || r.URL.Query().Get("list")!="true" {t.Errorf("wrong listing request: %v",r.URL)}
     _=json.NewEncoder(w).Encode(map[string]any{"data":map[string]any{"keys":[]string{"db/","api"}}})
   case "/v1/kv/metadata/projects/myapp/db":
     _=json.NewEncoder(w).Encode(map[string]any{"data":map[string]any{"keys":[]string{"postgres","redis"}}})
   case "/v1/kv/data/projects/myapp/db/postgres":
     _=json.NewEncoder(w).Encode(map[string]any{"data":map[string]any{
       "data":map[string]any{"password":"must-not-leak","username":"deploy","count":8},
       "metadata":map[string]int{"version":3},
     }})
   default:
     t.Errorf("unexpected project access: %s",r.URL.Path)
     w.WriteHeader(http.StatusForbidden)
   }
 })
 c,err:=newClientAtSocket(clientSocket)
 if err!=nil{t.Fatal(err)}
 keys,err:=c.ListKeys(context.Background(),"myapp","kv")
 if err!=nil{t.Fatal(err)}
 expected:=[]string{"api","db/postgres","db/redis"}
 if !reflect.DeepEqual(keys,expected){t.Fatalf("keys: %v",keys)}
 fields,err:=c.ListFields(context.Background(),"myapp","kv","db/postgres")
 if err!=nil{t.Fatal(err)}
 if !reflect.DeepEqual(fields,[]string{"password","username"}){t.Fatalf("fields: %v",fields)}
 if strings.Contains(strings.Join(fields," "),"must-not-leak"){t.Fatal("exposed secret value")}
}

func TestListingCannotEscapeProject(t *testing.T) {
 sock:=socketServer(t,func(w http.ResponseWriter,r *http.Request){
  _=json.NewEncoder(w).Encode(map[string]any{"data":map[string]any{"keys":[]string{"../other"}}})
 })
 c,err:=newClientAtSocket(sock)
 if err!=nil{t.Fatal(err)}
 if _,err:=c.ListKeys(context.Background(),"myapp","kv");err==nil{t.Fatal("accepted traversal in server listing")}
 if _,err:=c.ListKeys(context.Background(),"../other","kv");err==nil{t.Fatal("accepted project traversal")}
 if _,err:=c.ListFields(context.Background(),"myapp","kv","../other");err==nil{t.Fatal("accepted KV traversal")}
}

func TestListPermissionErrorsOmitServerResponses(t *testing.T) {
 sock:=socketServer(t,func(w http.ResponseWriter,r *http.Request){
   w.WriteHeader(http.StatusForbidden)
   _,_=w.Write([]byte("sensitive response"))
 })
 c,err:=newClientAtSocket(sock)
 if err!=nil{t.Fatal(err)}
 _,err=c.ListKeys(context.Background(),"myapp","kv")
 if err==nil||strings.Contains(err.Error(),"sensitive response"){t.Fatalf("unsafe list error: %v",err)}
}
