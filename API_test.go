package main

import (
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestParsePageOptions(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantPage   int64
		wantLimit  int64
		wantOffset int64
		wantOK     bool
	}{
		{name: "defaults", wantPage: 1, wantLimit: 20, wantOK: true},
		{name: "requested page", query: "?page=3&limit=10", wantPage: 3, wantLimit: 10, wantOffset: 20, wantOK: true},
		{name: "limit above maximum", query: "?limit=101", wantOK: false},
		{name: "non-positive page", query: "?page=0", wantOK: false},
		{name: "offset overflow", query: "?page=9223372036854775807", wantOK: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = httptest.NewRequest("GET", "/items"+test.query, nil)

			got, ok := parsePageOptions(context)
			if ok != test.wantOK {
				t.Fatalf("parsePageOptions() ok = %t, want %t", ok, test.wantOK)
			}
			if !ok {
				return
			}
			if got.Page != test.wantPage || got.Limit != test.wantLimit || got.Offset != test.wantOffset {
				t.Errorf("parsePageOptions() = %+v", got)
			}
		})
	}
}

func TestParseDateRange(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest("GET", "/dashboard?start_date=2026-01-01&end_date=2026-01-31", nil)
	got, ok := parseDateRange(context, false)
	if !ok || got.Start != "2026-01-01" || got.End != "2026-01-31" {
		t.Fatalf("parseDateRange() = %+v, %t", got, ok)
	}

	context, _ = gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest("GET", "/dashboard?start_date=2026-02-01&end_date=2026-01-31", nil)
	if _, ok := parseDateRange(context, false); ok {
		t.Fatal("parseDateRange() accepted a start date after the end date")
	}
}

func TestSQLFiltersUseBoundParameters(t *testing.T) {
	filters := sqlFilters{}
	filters.add("type = ?", "expense")
	filters.add("category_id = ?", int64(9))
	filters.addSearch("title", "' OR true --")

	if got, want := filters.where(), " WHERE type = $1 AND category_id = $2 AND POSITION(LOWER($3) IN LOWER(title)) > 0"; got != want {
		t.Fatalf("where() = %q, want %q", got, want)
	}
	if got, want := filters.args, []any{"expense", int64(9), "' OR true --"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestRouterRegistersAllAPIEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := Router()
	Categories(router, nil)
	Transactions(router, nil)
	Dashboard(router, nil)

	want := map[string]bool{
		"POST /categories":                    false,
		"GET /categories":                     false,
		"GET /categories/:id":                 false,
		"PUT /categories/:id":                 false,
		"DELETE /categories/:id":              false,
		"POST /transactions":                  false,
		"GET /transactions":                   false,
		"GET /transactions/:id":               false,
		"PUT /transactions/:id":               false,
		"DELETE /transactions/:id":            false,
		"GET /dashboard/summary":              false,
		"GET /dashboard/cashflow":             false,
		"GET /dashboard/expenses-by-category": false,
		"GET /dashboard/recent-transactions":  false,
	}

	for _, route := range router.Routes() {
		key := route.Method + " " + route.Path
		if _, exists := want[key]; exists {
			want[key] = true
		}
	}
	for route, registered := range want {
		if !registered {
			t.Errorf("route %q is not registered", route)
		}
	}
}
