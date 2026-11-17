package main

import (
	"testing"

	"github.com/antresol-app/antresol/internal/addons"
)

func TestNotifyTopicsOf(t *testing.T) {
	topic := []addons.NotifyTopic{{Topic: "news", Title: addons.Text{RU: "Новости", EN: "News"}, Audience: "adults"}}
	list := []addons.Installed{
		{ID: "dl", Enabled: true, Manifest: addons.Manifest{Permissions: []string{"events", "notify"}, Notify: topic}},
		{ID: "mute", Enabled: true, Manifest: addons.Manifest{Permissions: []string{"events"}, Notify: topic}}, // cannot post
		{ID: "off", Enabled: false, Manifest: addons.Manifest{Permissions: []string{"notify"}, Notify: topic}},
	}
	got := notifyTopicsOf(list)
	if len(got) != 1 || got[0].Addon != "dl" || got[0].Topic != "news" || got[0].Audience != "adults" || got[0].Title["en"] != "News" {
		t.Fatalf("%+v", got)
	}
}
