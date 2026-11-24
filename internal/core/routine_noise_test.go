package core

import "testing"

func TestRoutineNoiseFilterIsReversibleAndProtectsFailures(t *testing.T){
 s:=NewFilterState(100)
 s.SetHideRoutineNoise(true)
 messages:=[]string{
  "[09:11:32 DBG] Starting HttpMessageHandler cleanup cycle with 1 items",
  "[09:11:32 DBG] Ending HttpMessageHandler cleanup cycle after 0.2ms",
  "[09:11:32 DBG] Health check processing with combined status Healthy completed after 0.2965ms",
  "[09:11:32 WRN] HttpMessageHandler cleanup cycle delayed",
  "[09:11:32 ERR] HttpMessageHandler cleanup cycle failed",
  "[09:11:32 INF] HttpMessageHandler cleanup cycle threw exception",
  "[09:11:32 INF] Saving encounter",
 }
 s.Append(messages...)
 if s.VisibleCount()!=4||len(s.AllRecords())!=7{t.Fatalf("visible=%d retained=%d",s.VisibleCount(),len(s.AllRecords()))}
 s.SetHideRoutineNoise(false)
 if s.VisibleCount()!=7{t.Fatal("raw toggle did not recover hidden records")}
 s.SetHideRoutineNoise(true)
 s.SetFilter("cleanup cycle")
 if s.VisibleCount()!=5{t.Fatalf("explicit search should expose matching routine messages, got %d",s.VisibleCount())}
 s.SetFilter("")
 if s.VisibleCount()!=4{t.Fatal("clearing search did not restore clean mode")}
}
