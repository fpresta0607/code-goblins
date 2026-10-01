package supervisor

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// boardWriteRoutes reads every path the board serves to a method other than
// GET or HEAD out of ServeHTTP's route table in http.go, so an endpoint added
// later is held to the same rule as the ones here today. A prefix route is
// probed at a path under its prefix.
func boardWriteRoutes(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "http.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	literal := func(expr ast.Expr) string {
		value, ok := expr.(*ast.BasicLit)
		if !ok || value.Kind != token.STRING {
			return ""
		}
		text, err := strconv.Unquote(value.Value)
		if err != nil {
			t.Fatal(err)
		}
		return text
	}
	path := func(expr ast.Expr) string {
		switch match := expr.(type) {
		case *ast.BinaryExpr:
			if match.Op == token.EQL {
				return literal(match.Y)
			}
		case *ast.CallExpr:
			if function, ok := match.Fun.(*ast.SelectorExpr); ok && function.Sel.Name == "HasPrefix" && len(match.Args) == 2 {
				if prefix := literal(match.Args[1]); prefix != "" {
					return prefix + "probe/probe"
				}
			}
		}
		return ""
	}
	method := func(expr ast.Expr) string {
		if match, ok := expr.(*ast.BinaryExpr); ok && match.Op == token.EQL {
			if selector, ok := match.X.(*ast.SelectorExpr); ok && selector.Sel.Name == "Method" {
				return literal(match.Y)
			}
		}
		return ""
	}
	var routes []string
	ast.Inspect(file, func(node ast.Node) bool {
		clause, ok := node.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, expr := range clause.List {
			condition, ok := expr.(*ast.BinaryExpr)
			if !ok || condition.Op != token.LAND {
				continue
			}
			if route, verb := path(condition.X), method(condition.Y); route != "" && verb != "" && verb != "GET" && verb != "HEAD" {
				routes = append(routes, route)
			}
		}
		return true
	})
	return routes
}

// The credential save is the one board endpoint that takes a secret value.
// Every other endpoint that changes anything refuses a body that carries one,
// as a string, an object or a list under any field a value would travel in,
// before it reads anything else, and nothing of it reaches the home.
func TestNoBoardEndpointButTheCredentialSaveTakesASecretValue(t *testing.T) {
	// Arrange
	routes := boardWriteRoutes(t)
	for _, known := range []string{"/api/credentials/save", "/api/actions", "/api/terminal/input", "/api/connections/fix", "/api/tasks/start", "/api/setup/start"} {
		if !slices.Contains(routes, known) {
			t.Fatalf("the route table read as %v, without %s; the guard cannot see the board's endpoints", routes, known)
		}
	}
	store, h := testStore(t)
	handler := NewHTTP(&Service{Store: store, Instance: "test-instance"}, credentialBoardHost, nil)
	canary := newCanary(t)
	quoted := strconv.Quote(canary)
	carriers := []string{quoted, `{"NAME":` + quoted + `}`, `[` + quoted + `]`}

	post := func(route, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest("POST", "http://"+credentialBoardHost+route, strings.NewReader(body))
		request.RemoteAddr = "127.0.0.1:50000"
		request.Header.Set("Origin", "http://"+credentialBoardHost)
		request.Header.Set("X-CFO-Token", "test-instance")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	for _, route := range routes {
		if route == "/api/credentials/save" {
			continue
		}
		// A field no endpoint takes shows how this endpoint refuses a body it
		// cannot read. An endpoint that takes a value field reads past it
		// instead and answers something else, even when that is a 400 too.
		refused := post(route, `{"no_endpoint_takes_this_field":0}`)
		if refused.Code != http.StatusBadRequest {
			t.Errorf("%s answered a field it does not take with %d, want a bad request", route, refused.Code)
			continue
		}
		for _, field := range []string{"value", "values", "secret", "secrets", "password", "token", "credential", "credentials", "api_key", "key"} {
			for _, carrier := range carriers {
				// Act
				response := post(route, `{"`+field+`":`+carrier+`}`)

				// Assert
				if response.Code != refused.Code || response.Body.String() != refused.Body.String() {
					t.Errorf("%s reads a %q field: it answered %d %s, where a body it cannot read gets %d %s", route, field, response.Code, response.Body, refused.Code, refused.Body)
				}
				if strings.Contains(response.Body.String(), canary) {
					t.Errorf("%s repeated a %q value in its answer", route, field)
				}
			}
		}
	}
	if found := filesHolding(t, h.Root, canary); len(found) != 0 {
		t.Fatalf("a refused value reached the home: %v", found)
	}
}
