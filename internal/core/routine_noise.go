package core

import "strings"

// IsRoutineNoise is intentionally conservative. Only known routine framework
// messages at a non-severe level qualify. Retain unknown levels, warnings,
// failures and slow requests even if they mention framework cleanup.
func IsRoutineNoise(r LogRecord)bool{
 level:=r.Fields.Level
 switch level{case "DBG","INF","TRC","VRB":default:return false}
 switch normalizeLogLevel(r.Event.Level){case "ERR","WRN","FTL":return false}
 if IsSlowRequest(r.Fields)||r.Fields.StatusCode>=400{return false}
 raw:=strings.ToLower(r.Event.Message)
 for _,word:=range []string{"failed","failure","unhealthy","degraded","exception","error","timeout","timed out","canceled","cancelled"}{
  if strings.Contains(raw,word){return false}
 }
 if strings.Contains(raw,"httpmessagehandler") || strings.Contains(raw,"http message handler"){
  if strings.Contains(raw,"cleanup cycle")||strings.Contains(raw,"handler expired")||strings.Contains(raw,"handler expiry")||strings.Contains(raw,"starting cleanup")||strings.Contains(raw,"ending cleanup"){return true}
 }
 if strings.Contains(raw,"health check processing with combined status healthy completed after"){return true}
 return false
}
