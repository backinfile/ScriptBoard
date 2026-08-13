package web

import (
	"context"
	"encoding/csv"
	"net/http"
	"strings"
	"time"

	"scriptboard/internal/logstream"
	"scriptboard/internal/secretredaction"
	"scriptboard/internal/servicelogs"
)

type serviceLogsPageData struct {
	Locale             webLocale
	SettingsNavigation settingsNavigationData
	Report             servicelogs.Report
	Service            string
	Range              string
	Severity           string
	Search             string
	Error              string
}

func (a *App) serviceLogsPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	locale := resolveWebLocale(request)
	query := serviceLogQuery(request)
	data := serviceLogsPageData{
		Locale: locale, SettingsNavigation: newSettingsNavigation(current, locale, "service-logs"),
		Service: query.Service, Range: query.Range, Severity: string(query.Severity), Search: query.Search,
	}
	ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
	report, err := a.serviceLogs.List(ctx, query)
	cancel()
	data.Report = report
	if err != nil {
		data.Error = secretredaction.String(err.Error())
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := serviceLogsTemplate.Execute(response, data); err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
	}
}

func (a *App) exportServiceLogs(response http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
	report, err := a.serviceLogs.List(ctx, serviceLogQuery(request))
	cancel()
	if err != nil {
		http.Error(response, "Service logs are temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/csv; charset=utf-8")
	response.Header().Set("Content-Disposition", `attachment; filename="scriptboard-service-logs.csv"`)
	writer := csv.NewWriter(response)
	_ = writer.Write([]string{"time", "service", "severity", "event_id", "source", "message"})
	for _, entry := range report.Entries {
		record := []string{entry.Time.UTC().Format(time.RFC3339Nano), entry.Service, string(entry.Severity), entry.EventID, entry.Source, entry.Message}
		for index := range record {
			record[index] = spreadsheetSafeCSVCell(secretredaction.String(record[index]))
		}
		if err := writer.Write(record); err != nil {
			return
		}
	}
	writer.Flush()
}

func serviceLogQuery(request *http.Request) servicelogs.Query {
	query := servicelogs.Query{
		Service:  strings.TrimSpace(request.URL.Query().Get("service")),
		Range:    strings.TrimSpace(request.URL.Query().Get("range")),
		Severity: logstream.Severity(strings.TrimSpace(request.URL.Query().Get("severity"))),
		Search:   strings.TrimSpace(request.URL.Query().Get("q")),
	}
	if query.Range != "7d" && query.Range != "30d" {
		query.Range = "24h"
	}
	return query
}

func serviceLogServiceLabel(locale webLocale, service string) string {
	key := "service_logs.service_" + service
	value := webText(locale, key)
	if value == key {
		return service
	}
	return value
}
