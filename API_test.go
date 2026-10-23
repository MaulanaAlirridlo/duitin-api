package main

import (
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
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
	secret := []byte("test-secret-that-is-at-least-thirty-two-bytes")
	Users(router, nil, secret)
	authenticate := authMiddleware(secret)
	Categories(router, nil, authenticate)
	Transactions(router, nil, authenticate)
	Dashboard(router, nil, authenticate)

	want := map[string]bool{
		"POST /auth/register":                 false,
		"POST /auth/login":                    false,
		"GET /users/me":                       false,
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

func TestAuthMiddlewareRequiresValidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := []byte("test-secret-that-is-at-least-thirty-two-bytes")
	router := gin.New()
	router.GET("/protected", authMiddleware(secret), func(c *gin.Context) {
		if got := c.GetInt64(userIDContextKey); got != 42 {
			t.Errorf("authenticated user ID = %d, want 42", got)
		}
		c.Status(204)
	})

	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest("GET", "/protected", nil))
	if unauthorized.Code != 401 {
		t.Fatalf("request without token status = %d, want 401", unauthorized.Code)
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, userClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    jwtIssuer,
			Subject:   "42",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		},
	})
	signedToken, err := token.SignedString(secret)
	if err != nil {
		t.Fatal(err)
	}
	authorizedRequest := httptest.NewRequest("GET", "/protected", nil)
	authorizedRequest.Header.Set("Authorization", "Bearer "+signedToken)
	authorized := httptest.NewRecorder()
	router.ServeHTTP(authorized, authorizedRequest)
	if authorized.Code != 204 {
		t.Fatalf("request with valid token status = %d, want 204", authorized.Code)
	}

	expiredToken := jwt.NewWithClaims(jwt.SigningMethodHS256, userClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    jwtIssuer,
			Subject:   "42",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
		},
	})
	signedExpiredToken, err := expiredToken.SignedString(secret)
	if err != nil {
		t.Fatal(err)
	}
	expiredRequest := httptest.NewRequest("GET", "/protected", nil)
	expiredRequest.Header.Set("Authorization", "Bearer "+signedExpiredToken)
	expired := httptest.NewRecorder()
	router.ServeHTTP(expired, expiredRequest)
	if expired.Code != 401 {
		t.Fatalf("request with expired token status = %d, want 401", expired.Code)
	}
}

func TestValidateRegisterUserInput(t *testing.T) {
	valid := registerUserInput{
		Name:     "Example User",
		Email:    "user@example.com",
		Password: "correct horse battery staple",
	}
	if message := validateRegisterUserInput(valid); message != "" {
		t.Fatalf("valid registration input rejected: %s", message)
	}

	invalidInputs := []registerUserInput{
		{Name: "", Email: valid.Email, Password: valid.Password},
		{Name: valid.Name, Email: "not-an-email", Password: valid.Password},
		{Name: valid.Name, Email: valid.Email, Password: "short"},
		{Name: valid.Name, Email: valid.Email, Password: string(make([]byte, 73))},
	}
	for _, input := range invalidInputs {
		if message := validateRegisterUserInput(input); message == "" {
			t.Errorf("validateRegisterUserInput(%+v) accepted invalid input", input)
		}
	}
}

func TestValidateCategoryInput(t *testing.T) {
	input := categoryInput{Name: "Makan", Type: "expense", Icon: "🍜", Color: "#64748b"}
	if message := validateCategoryInput(&input); message != "" {
		t.Fatalf("valid category input rejected: %s", message)
	}

	defaults := categoryInput{Name: "Gaji", Type: "income"}
	if message := validateCategoryInput(&defaults); message != "" {
		t.Fatalf("category without icon or color rejected: %s", message)
	}
	if defaults.Icon != "💰" || defaults.Color != "#10b981" {
		t.Fatalf("default icon and color = %q, %q", defaults.Icon, defaults.Color)
	}

	invalidInputs := []categoryInput{
		{Name: "Kategori", Type: "expense", Icon: strings.Repeat("a", 29), Color: "#64748b"},
		{Name: "Kategori", Type: "expense", Icon: "📦", Color: "blue"},
	}
	for _, invalid := range invalidInputs {
		if message := validateCategoryInput(&invalid); message == "" {
			t.Errorf("validateCategoryInput(%+v) accepted invalid input", invalid)
		}
	}
}
