package web

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"scriptboard/internal/mysqlmanager"
)

type mysqlPlanDetails struct {
	Plan                          mysqlmanager.Plan
	NextFive                      []time.Time
	Operations                    []mysqlmanager.Operation
	Pagination                    mysqlPagination
	BackURL, PreviousURL, NextURL string
}

func (a *App) loadMySQLPlanDetails(response http.ResponseWriter, request *http.Request, data *mysqlDatabasesPageData) bool {
	id := request.URL.Query().Get("plan")
	if id == "" {
		return true
	}
	plan, err := a.mysql.Plan(request.Context(), id)
	if err != nil || data.Selected == nil || plan.InstanceID != data.Selected.ID {
		http.Error(response, "MySQL backup plan not found", http.StatusNotFound)
		return false
	}
	details := &mysqlPlanDetails{Plan: plan}
	details.NextFive, err = a.mysql.PlanNextFive(plan)
	if err != nil {
		http.Error(response, "Unable to preview backup plan", http.StatusInternalServerError)
		return false
	}
	page := mysqlRequestedNamedPage(request, "plan_page")
	var total int
	details.Operations, total, err = a.mysql.PlanOperationsPage(request.Context(), id, mysqlPageSize, (page-1)*mysqlPageSize)
	if err != nil {
		http.Error(response, "Unable to load backup plan history", http.StatusInternalServerError)
		return false
	}
	details.Pagination = newMySQLPagination(page, total)
	if details.Pagination.Page != page {
		details.Operations, _, err = a.mysql.PlanOperationsPage(request.Context(), id, mysqlPageSize, (details.Pagination.Page-1)*mysqlPageSize)
		if err != nil {
			http.Error(response, "Unable to load backup plan history", http.StatusInternalServerError)
			return false
		}
	}
	query := request.URL.Query()
	query.Set("tab", "plans")
	query.Del("plan")
	query.Del("plan_page")
	details.BackURL = "/resources/databases?" + query.Encode()
	query.Set("plan", id)
	makeURL := func(page int) string {
		q := url.Values{}
		for key, values := range query {
			q[key] = values
		}
		q.Set("plan_page", strconv.Itoa(page))
		return "/resources/databases?" + q.Encode()
	}
	details.PreviousURL = makeURL(details.Pagination.Previous)
	details.NextURL = makeURL(details.Pagination.Next)
	data.PlanDetails = details
	return true
}
