package desktopapp

import (
	"errors"
	"log"
	"sync/atomic"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// macOS notifications, through Wails (UNUserNotificationCenter). The first
// time, macOS asks whether TropicalPeer may show them.

const offerCategory = "offer"

var notesReady atomic.Bool

func (a *App) initNotes() {
	ctx := a.ctx
	if err := runtime.InitializeNotifications(ctx); err != nil {
		log.Printf("notifications: %v", err)
		return
	}
	runtime.OnNotificationResponse(ctx, func(r runtime.NotificationResult) {
		if r.Error == nil {
			a.onNote(r.Response.ID, r.Response.ActionIdentifier)
		}
	})
	a.registerOfferButtons()
	notesReady.Store(true)
	go func() {
		if ok, err := runtime.RequestNotificationAuthorization(ctx); err != nil {
			log.Printf("notifications: %v", err)
		} else if !ok {
			log.Print("notifications: not allowed in System Settings")
		}
	}()
}

func (a *App) sendNote(n note) error {
	if !notesReady.Load() {
		return errors.New("notifications aren't available")
	}
	opts := runtime.NotificationOptions{ID: n.id, Title: n.title, Body: n.body}
	if len(n.actions) > 0 {
		opts.CategoryID = offerCategory
		return runtime.SendNotificationWithActions(a.ctx, opts)
	}
	return runtime.SendNotification(a.ctx, opts)
}

func (a *App) removeNote(id string) {
	if !notesReady.Load() {
		return
	}
	_ = runtime.RemoveDeliveredNotification(a.ctx, id)
	_ = runtime.RemovePendingNotification(a.ctx, id)
}

func (a *App) notesLanguageChanged() {
	if notesReady.Load() {
		a.registerOfferButtons()
	}
}

// registerOfferButtons names the offer buttons in the app's language: on a
// Mac they belong to a category, not to each notification.
func (a *App) registerOfferButtons() {
	err := runtime.RegisterNotificationCategory(a.ctx, runtime.NotificationCategory{
		ID: offerCategory,
		Actions: []runtime.NotificationAction{
			{ID: "decline", Title: a.core.T("offer.decline"), Destructive: true},
			{ID: "accept", Title: a.core.T("offer.accept")},
		},
	})
	if err != nil {
		log.Printf("notifications: %v", err)
	}
}
