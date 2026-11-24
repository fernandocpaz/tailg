package logui

import (
 "context"
 "github.com/fernandocpaz/tailg/internal/tui"
)

// UpdateOffer passes a provider-specific verified installer to tailg's
// original update dialog, without selecting another app's release assets.
type UpdateOffer struct {
 Application string
 Installed string
 Latest string
 Install func(context.Context)error
}

func PromptUpdate(ctx context.Context,offer UpdateOffer)(installed,quit bool,err error){
 return tui.PromptExternalUpdate(ctx,tui.ExternalUpdateOffer{
  Application:offer.Application,Installed:offer.Installed,
  Latest:offer.Latest,Install:offer.Install,
 })
}
