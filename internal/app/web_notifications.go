package app

import (
	"net/http"

	"scriptboard/internal/securityevents"
)

type notificationsPageData struct {
	Locale             webLocale
	SettingsNavigation settingsNavigationData
	Status             securityevents.Status
	Error              string
}

func (a *App) notificationsPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	data := notificationsPageData{Locale: locale, SettingsNavigation: newSettingsNavigation(current, locale, "notifications")}
	if a.securityEvents == nil {
		data.Error = "Security event status is temporarily unavailable"
	} else if status, err := a.securityEvents.Status(); err != nil {
		data.Error = "Security event status is temporarily unavailable"
	} else {
		data.Status = status
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := notificationsTemplate.Execute(response, data); err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
	}
}
