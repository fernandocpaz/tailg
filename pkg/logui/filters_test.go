package logui

import (
 "os"
 "path/filepath"
 "testing"
)
func TestInitializeSharedFilter(t *testing.T){
 path:=filepath.Join(t.TempDir(),"shared-filter.txt")
 if err:=InitializeSharedFilter(path,"level:ERR");err!=nil{t.Fatal(err)}
 raw,err:=os.ReadFile(path)
 if err!=nil{t.Fatal(err)}
 if len(raw)==0{t.Fatal("missing initial shared filter")}
 mode,err:=os.ReadFile(path+".mode")
 if err!=nil{t.Fatal(err)}
 if len(mode)==0{t.Fatal("missing F1 mode")}
}
