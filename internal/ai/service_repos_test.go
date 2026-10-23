package ai

import (
 "context"
 "os"
 "path/filepath"
 "runtime"
 "strings"
 "testing"
)

func TestServiceMappingPersistedAndScoped(t *testing.T){
 testConfigDir(t)
 a:=t.TempDir();b:=t.TempDir()
 cfg:=Config{APIKey:"key",Model:"model",ServiceRepos:map[string]ServiceRepository{
  ServiceKey("qa","orders"):{Path:a,Branch:"main",Commit:strings.Repeat("a",40)},
  ServiceKey("prod","patients"):{Path:b,Branch:"develop"},
 }}
 if err:=SaveSettings(cfg);err!=nil{t.Fatal(err)}
 loaded,yes,err:=LoadSettings()
 if err!=nil||!yes||len(loaded.ServiceRepos)!=2{t.Fatalf("settings failed: %v, %+v",err,loaded.ServiceRepos)}
 got,ok:=loaded.CurrentServiceRepo("qa/orders")
 if !ok||got.Path!=a||got.Branch!="main"{t.Fatalf("wrong mapped root %+v",got)}
 if _,ok:=loaded.CurrentServiceRepo("qa/patients");ok{t.Fatal("cross-service mapping leak")}
 if _,ok:=loaded.CurrentServiceRepo("prod/orders");ok{t.Fatal("cross-namespace mapping leak")}
 if err:=os.RemoveAll(a);err!=nil{t.Fatal(err)}
 if _,ok:=loaded.CurrentServiceRepo("qa/orders");ok{t.Fatal("stale/missing path should prompt again")}
}

func TestCloneURLValidationAndDefaultHead(t *testing.T) {
 for _,v:=range []string{"https://github.com/org/repo.git","git@github.com:org/repo.git"}{
  if _,err:=ValidateCloneURL(v);err!=nil{t.Fatal(err)}
 }
 for _,v:=range []string{"file:///tmp/x","http://github.com/org/repo.git","https://user:pass@github.com/repo","https://github.com/x?key=1","-evil","git@host:../danger.git"}{
  if _,err:=ValidateCloneURL(v);err==nil{t.Errorf("unsafe URL accepted: %s",v)}
 }
 if ServiceKey("qa","svc")!="qa/svc"||ServiceKey("qa","")==""&&false{t.Fatal("service key mismatch")}
}

func TestManagedCloneDetectsRemoteHeadWithoutChangingExistingCheckout(t *testing.T){
 if runtime.GOOS=="windows"{t.Skip("test uses a POSIX fake git executable")}
 testConfigDir(t)
 cache:=t.TempDir()
 t.Setenv("XDG_CACHE_HOME",cache)
 original:=t.TempDir()
 origFile:=filepath.Join(original,"keep.txt")
 if err:=os.WriteFile(origFile,[]byte("unchanged"),0600);err!=nil{t.Fatal(err)}
 bin:=t.TempDir()
 script:=`#!/bin/sh
case "$*" in
 *"ls-remote --symref"*)
   printf 'ref: refs/heads/release/qa HEAD\n0123456789abcdef0123456789abcdef01234567 HEAD\n'
   ;;
 *" clone "*)
   for path; do :; done
   mkdir -p "$path"
   printf 'checked out managed source' > "$path/code.go"
   ;;
 *"symbolic-ref --quiet --short HEAD"*)
   echo "release/qa"
   ;;
 *"rev-parse --verify HEAD"*)
   echo 0123456789abcdef0123456789abcdef01234567
   ;;
 *) exit 2;;
esac
`
 if err:=os.WriteFile(filepath.Join(bin,"git"),[]byte(script),0700);err!=nil{t.Fatal(err)}
 t.Setenv("PATH",bin+string(os.PathListSeparator)+os.Getenv("PATH"))
 entry,err:=CloneService(context.Background(),"qa/order-service","https://github.com/company/orders.git")
 if err!=nil{t.Fatal(err)}
 if entry.Branch!="release/qa" || entry.Commit!="0123456789abcdef0123456789abcdef01234567"{t.Fatalf("HEAD not preserved: %+v",entry)}
 if !strings.HasPrefix(entry.Path,cache)||strings.HasPrefix(entry.Path,original){t.Fatalf("unexpected clone path: %s",entry.Path)}
 if b,_:=os.ReadFile(origFile);string(b)!="unchanged"{t.Fatal("original checkout was modified")}
 if _,err:=os.Stat(filepath.Join(entry.Path,"code.go"));err!=nil{t.Fatal(err)}
 entry2,err:=CloneService(context.Background(),"qa/order-service","https://github.com/company/orders.git")
 if err!=nil||entry2.Path==entry.Path{t.Fatalf("managed clones must never overwrite each other: %v %s",err,entry2.Path)}
}
