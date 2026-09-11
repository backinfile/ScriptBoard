package web

import (
	"net/http"
)

type settingsEntry struct {
	URL, Icon, Title, Description string
	Drawer                        bool
}

func (a *App) settingsHubPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	group := request.URL.Path[len("/settings/"):]
	navigation := newSettingsNavigation(current, locale, group)
	if group == "integrations" && !navigation.CanManageSystem && !navigation.CanManageExecution {
		http.Error(response, "Forbidden", http.StatusForbidden)
		return
	}
	entries := []settingsEntry{}
	add := func(url, icon, title, description string, drawer bool) {
		entries = append(entries, settingsEntry{url, icon, title, description, drawer})
	}
	switch group {
	case "instance":
		add("/settings/name", "square-pen", "instance_name.title", "instance_name.description", true)
		add("/settings/memory", "memory-stick", "memory_settings.title", "memory_settings.description", true)
	case "integrations":
		if navigation.CanManageSystem {
			add("/settings/nodes", "key-round", "settings.node_tokens", "fleet.access_tokens_description", false)
			add("/settings/embedding", "app-window", "embedding.title", "embedding.description", true)
			add("/settings/notifications", "bell-ring", "settings.notification_status", "settings.notification_hint", false)
		}
		if navigation.CanManageExecution {
			add("/settings/mcp", "bot", "settings.mcp", "settings.mcp_hint", true)
		}
	case "maintenance":
		add("/settings/state-backups", "archive-restore", "state_backups.title", "state_backups.description", false)
		add("/settings/updates", "refresh-cw", "updates.title", "settings.update_hint", false)
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = settingsHubTemplate.Execute(response, struct {
		Locale             webLocale
		SettingsNavigation settingsNavigationData
		Title              string
		Entries            []settingsEntry
	}{locale, navigation, "settings." + group, entries})
}
