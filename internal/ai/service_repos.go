package ai

import (
 "context"
 "crypto/sha256"
 "encoding/hex"
 "errors"
 "fmt"
 "net/url"
 "os"
 "os/exec"
 "path/filepath"
 "regexp"
 "strings"
 "time"
)

// ServiceRepository binds a Kubernetes namespace/workload to exactly one source
// checkout. Branch and commit identify the inspected revision, not a guess.
type ServiceRepository struct {
 Path string `json:"path"`
 URL string `json:"url,omitempty"`
 Branch string `json:"branch,omitempty"`
 Commit string `json:"commit,omitempty"`
}

// ServiceKey intentionally omits the cluster so the same namespaced service
// across QA/prod can reuse the same source. Distinct namespaces stay distinct.
func ServiceKey(namespace, workload string) string {
 namespace=strings.TrimSpace(namespace); workload=strings.TrimSpace(workload)
 if namespace=="" { namespace="default" }
 if workload=="" { return "" }
 return namespace+"/"+workload
}

// CurrentServiceRepo returns a usable path only for an explicitly mapped service.
func (c Config) CurrentServiceRepo(key string) (ServiceRepository,bool) {
 entry,ok:=c.ServiceRepos[key]
 if !ok || entry.Path=="" {return ServiceRepository{},false}
 info,err:=os.Stat(entry.Path)
 return entry,err==nil && info.IsDir()
}

var invalidSlug=regexp.MustCompile(`[^a-zA-Z0-9._-]+`)
func cleanSlug(s string)string {
 s=invalidSlug.ReplaceAllString(s,"-")
 s=strings.Trim(s,".-")
 if len(s)>48{s=s[:48]}
 if s=="" {s="service"}
 return s
}

func checkoutBase() (string,error) {
 base,err:=os.UserCacheDir()
 if err!=nil{return "",err}
 return filepath.Join(base,"tailg","ai-repos"),nil
}

func ValidateCloneURL(raw string) (string,error) {
 raw=strings.TrimSpace(raw)
 if raw=="" {return "",errors.New("enter a Git clone URL")}
 if strings.HasPrefix(raw,"git@") {
  parts:=strings.SplitN(raw[4:],":",2)
  if len(parts)!=2 || parts[0]=="" || strings.HasPrefix(parts[1],"-") || strings.ContainsAny(raw," \t\r\n") ||
    strings.ContainsAny(parts[0],"@/\\") || strings.Contains(parts[1],"..") {
    return "",errors.New("use an SSH Git URL such as git@github.com:org/repo.git")
  }
  return raw,nil
 }
 u,err:=url.Parse(raw)
 if err!=nil || u.Scheme!="https" || u.Hostname()=="" || u.User!=nil || u.RawQuery!="" || u.Fragment!="" {
  return "",errors.New("use an HTTPS clone URL without embedded credentials, or an SSH git@host:path URL")
 }
 return raw,nil
}

// DefaultBranch consults the remote's advertised symbolic HEAD rather than
// assuming main or master. It never runs git code from the repository.
func DefaultBranch(ctx context.Context, remote string) (string,error) {
 var err error
 remote,err=ValidateCloneURL(remote)
 if err!=nil{return "",err}
 out,err:=gitCommand(ctx,"ls-remote","--symref",remote,"HEAD")
 if err!=nil{return "",fmt.Errorf("unable to detect remote HEAD: %w",err)}
 for _,line:=range strings.Split(out,"\n") {
  fields:=strings.Fields(line)
  if len(fields)==3 && fields[0]=="ref:" && fields[2]=="HEAD" && strings.HasPrefix(fields[1],"refs/heads/") {
    name:=strings.TrimPrefix(fields[1],"refs/heads/")
    if validBranch(name){return name,nil}
  }
 }
 return "",errors.New("remote does not advertise a default branch (HEAD); choose a repository that has a configured default branch")
}

func validBranch(branch string)bool {
 if branch==""||strings.HasPrefix(branch,"-")||strings.HasPrefix(branch,"/")||strings.Contains(branch,"..")||strings.Contains(branch,"@{")||strings.HasSuffix(branch,"/")||strings.HasSuffix(branch,".lock"){return false}
 return !strings.ContainsAny(branch," ~^:?*[\\\r\n\t")
}

func gitCommand(ctx context.Context,args ...string)(string,error){
 git,err:=exec.LookPath("git")
 if err!=nil{return "",errors.New("Git is not installed or not on PATH; choose an existing local folder instead")}
 // Explicitly disable protocol.ext and untrusted repository hooks, while using
 // the user's existing Git credentials (SSH agent/credential helpers).
 argv:=append([]string{"-c","protocol.ext.allow=never","-c","core.hooksPath="+os.DevNull},args...)
 cmd:=exec.CommandContext(ctx,git,argv...)
 cmd.Env=append(os.Environ(),"GIT_TERMINAL_PROMPT=0","GIT_LFS_SKIP_SMUDGE=1")
 data,err:=cmd.CombinedOutput()
 if err!=nil{
   // Git stderr can include credential-bearing URL strings. Never surface it.
   if errors.Is(ctx.Err(),context.DeadlineExceeded){return "",errors.New("Git operation timed out")}
   return "",errors.New("Git operation failed; verify URL, network access and Git authentication")
 }
 return strings.TrimSpace(string(data)),nil
}

// CloneService creates a NEW checkout under the tailg cache directory.
// It never updates, resets, deletes or checks out a user's existing clone.
// A staging directory is used so failed clones cannot become mapped sources.
func CloneService(parent context.Context,service,remote string)(ServiceRepository,error){
 if strings.TrimSpace(service)==""{return ServiceRepository{},errors.New("service is required")}
 remote,err:=ValidateCloneURL(remote);if err!=nil{return ServiceRepository{},err}
 ctx,cancel:=context.WithTimeout(parent,90*time.Second);defer cancel()
 branch,err:=DefaultBranch(ctx,remote);if err!=nil{return ServiceRepository{},err}
 root,err:=checkoutBase();if err!=nil{return ServiceRepository{},err}
 if err:=os.MkdirAll(root,0700);err!=nil{return ServiceRepository{},err}
 info,err:=os.Lstat(root)
 if err!=nil || !info.IsDir() || info.Mode()&os.ModeSymlink!=0{return ServiceRepository{},errors.New("checkout directory must be a real directory")}
 digest:=sha256.Sum256([]byte(service+"\x00"+remote))
 name:=cleanSlug(strings.ReplaceAll(service,"/","-"))+"-"+hex.EncodeToString(digest[:6])
 // MkdirTemp atomically reserves a new isolated directory instead of
 // renaming over any possibly-existing user data (including races).
 staging,err:=os.MkdirTemp(root,name+"-")
 if err!=nil{return ServiceRepository{},err}
 success:=false
 defer func(){if !success{_ = os.RemoveAll(staging)}}()
 target:=filepath.Join(staging,"checkout")
 _,err=gitCommand(ctx,"clone","--quiet","--depth","1","--single-branch","--branch",branch,"--",remote,target)
 if err!=nil{return ServiceRepository{},err}
 actual,err:=gitCommand(ctx,"-C",target,"symbolic-ref","--quiet","--short","HEAD")
 if err!=nil||actual!=branch{return ServiceRepository{},errors.New("cloned checkout branch did not match advertised HEAD")}
 commit,err:=gitCommand(ctx,"-C",target,"rev-parse","--verify","HEAD")
 if err!=nil||len(commit)<40{return ServiceRepository{},errors.New("could not verify checkout commit")}
 success=true
 return ServiceRepository{Path:target,URL:remote,Branch:branch,Commit:commit},nil
}

func GitAvailable() bool {_,err:=exec.LookPath("git");return err==nil}

func LocalServiceRepo(path string)(ServiceRepository,error){
 abs,err:=filepath.Abs(strings.TrimSpace(path))
 if err!=nil{return ServiceRepository{},err}
 info,err:=os.Stat(abs)
 if err!=nil||!info.IsDir(){return ServiceRepository{},errors.New("local repository folder does not exist")}
 if _,err:=os.Lstat(abs);err!=nil{return ServiceRepository{},err}
 result:=ServiceRepository{Path:abs}
 if GitAvailable(){
  ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel()
  if branch,e:=gitCommand(ctx,"-C",abs,"symbolic-ref","--quiet","--short","HEAD");e==nil{result.Branch=branch}
  if commit,e:=gitCommand(ctx,"-C",abs,"rev-parse","--verify","HEAD");e==nil{result.Commit=commit}
 }
 return result,nil
}
